package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLocal(t *testing.T, dir, rel, data string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func fileWhere(res *Result) map[string]string {
	out := map[string]string{}
	for _, f := range res.Files {
		out[f.Path] = f.Where
	}
	return out
}

func TestPhoneFilesListAndPull(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	then := time.Date(2026, 11, 1, 9, 30, 0, 0, time.Local)
	x.files["notes/todo.txt"] = memFile{data: []byte("milk"), mtime: then}
	x.files["photo.jpg"] = memFile{data: []byte("jpeg"), mtime: then}
	g := newExtraGateway(t, x)
	s, _ := connect(t, g, "Copy notes")
	dir := g.FilesDir("pixel")
	writeLocal(t, dir, "photo.jpg", "jpeg", then)
	writeLocal(t, dir, "draft.md", "# plan", then)

	res := s.Files(ctx, FilesRequest{})
	mustStatus(t, res, StatusSettled)
	if res.Folder != dir {
		t.Fatalf("folder %q, want %q", res.Folder, dir)
	}
	want := map[string]string{"notes/todo.txt": FilePhone, "photo.jpg": FileBoth, "draft.md": FileComputer}
	if got := fileWhere(res); len(got) != 3 || got["notes/todo.txt"] != want["notes/todo.txt"] ||
		got["photo.jpg"] != want["photo.jpg"] || got["draft.md"] != want["draft.md"] {
		t.Fatalf("list %+v", res.Files)
	}

	res = s.Files(ctx, FilesRequest{Action: "pull"})
	mustStatus(t, res, StatusSettled)
	if len(res.Files) != 1 || res.Files[0].Path != "notes/todo.txt" {
		t.Fatalf("pulled %+v, want only the file the computer lacks", res.Files)
	}
	b, err := os.ReadFile(filepath.Join(dir, "notes", "todo.txt"))
	if err != nil || string(b) != "milk" {
		t.Fatalf("pulled file: %q, %v", b, err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "notes", "todo.txt")); !fi.ModTime().Equal(then) {
		t.Fatalf("pulled file's time %v, want %v", fi.ModTime(), then)
	}
	res = s.Files(ctx, FilesRequest{Action: "pull", Paths: []string{"nope.txt"}})
	if res.Error != ErrNotFound {
		t.Fatalf("pull of a missing file: %+v", res)
	}
	res = s.Files(ctx, FilesRequest{Action: "pull", Paths: []string{"../../etc/passwd"}})
	if res.Error != ErrInvalidArgument {
		t.Fatalf("a path out of the folder: %+v", res)
	}
	var texts []string
	for _, st := range taskSteps(g, s) {
		if st.Tool == "phone_files" {
			texts = append(texts, st.Text)
		}
	}
	if len(texts) < 2 || texts[0] != "Listed the phone's Pinky folder (2 files)" || texts[1] != "Copied 1 file from the phone: notes/todo.txt" {
		t.Fatalf("steps %q", texts)
	}
}

// A link the agent planted where a pulled file goes is replaced, not
// followed: nothing outside the folder is written.
func TestPhoneFilesPullReplacesLinks(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	x.files["notes.txt"] = memFile{data: []byte("from the phone"), mtime: time.Now()}
	g := newExtraGateway(t, x)
	s, _ := connect(t, g, "Copy notes")
	dir := g.FilesDir("pixel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "bashrc")
	if err := os.WriteFile(outside, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "notes.txt")); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	mustStatus(t, s.Files(ctx, FilesRequest{Action: "pull"}), StatusSettled)
	if b, _ := os.ReadFile(outside); string(b) != "keep me" {
		t.Fatalf("the pull wrote through the link: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes.txt")); string(b) != "from the phone" {
		t.Fatalf("pulled %q", b)
	}
}

func TestPhoneFilesPushAsks(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	g := newExtraGateway(t, x)
	s, _ := connect(t, g, "Send a plan")
	dir := g.FilesDir("pixel")
	writeLocal(t, dir, "plan.md", "# plan", time.Now())
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "hosts")); err != nil {
		t.Log("no symbolic links:", err)
	}

	// Denied: nothing reaches the phone.
	done := make(chan *Result, 1)
	go func() { done <- s.Files(ctx, FilesRequest{Action: "push"}) }()
	n := waitNeedKind(t, g, NeedApproval)
	if n.Rule != filesRule || n.Title != "Copy “plan.md” to Pixel 9?" {
		t.Fatalf("approval %+v", n)
	}
	if err := g.Decide(n.ID, Deny, "not now"); err != nil {
		t.Fatal(err)
	}
	res := <-done
	mustStatus(t, res, StatusDenied)
	if len(x.files) != 0 {
		t.Fatalf("pushed after Deny: %v", x.files)
	}

	// Allowed for the task: this push and the next one go through.
	go func() { done <- s.Files(ctx, FilesRequest{Action: "push"}) }()
	n = waitNeedKind(t, g, NeedApproval)
	if err := g.Decide(n.ID, AllowTask, ""); err != nil {
		t.Fatal(err)
	}
	res = <-done
	mustStatus(t, res, StatusSettled)
	if string(x.files["plan.md"].data) != "# plan" {
		t.Fatalf("phone files %v", x.files)
	}
	if _, ok := x.files["hosts"]; ok {
		t.Fatal("a link was pushed")
	}
	writeLocal(t, dir, "plan.md", "# plan, v2", time.Now().Add(time.Minute))
	res = s.Files(ctx, FilesRequest{Action: "push", Paths: []string{"plan.md"}})
	mustStatus(t, res, StatusSettled)
	if string(x.files["plan.md"].data) != "# plan, v2" {
		t.Fatalf("second push %q", x.files["plan.md"].data)
	}
	// Nothing new: nothing to ask about.
	res = s.Files(ctx, FilesRequest{Action: "push"})
	mustStatus(t, res, StatusSettled)
	if !strings.Contains(res.Message, "nothing was copied") {
		t.Fatalf("push of nothing: %+v", res)
	}
}

func TestPhoneFilesReadOnly(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	x.files["a.txt"] = memFile{data: []byte("a"), mtime: time.Now()}
	g := newExtraGateway(t, x)
	g.SetAgentScope(agent.Name, Scope{Mode: ScopeReadOnly})
	s, _ := connect(t, g, "Read files")
	writeLocal(t, g.FilesDir("pixel"), "b.txt", "b", time.Now())
	mustStatus(t, s.Files(ctx, FilesRequest{Action: "pull"}), StatusSettled)
	if res := s.Files(ctx, FilesRequest{Action: "push"}); res.Error != ErrScopeDenied {
		t.Fatalf("read-only agent push: %+v", res)
	}
	g.SetAgentScope(agent.Name, Scope{Mode: ScopeEverything})
	if err := g.SetReadOnly("pixel", true); err != nil {
		t.Fatal(err)
	}
	if res := s.Files(ctx, FilesRequest{Action: "push"}); res.Status != StatusBlocked || res.Rule != "Read-only" {
		t.Fatalf("push in read-only mode: %+v", res)
	}
	if len(x.files) != 1 {
		t.Fatalf("phone files %v", x.files)
	}
}

func TestPhoneFilesUnsupported(t *testing.T) {
	g, _ := newTestGateway(t)
	s, _ := connect(t, g, "Files")
	if res := s.Files(context.Background(), FilesRequest{}); res.Error != ErrUnsupported {
		t.Fatalf("a phone without a Pinky folder: %+v", res)
	}
}
