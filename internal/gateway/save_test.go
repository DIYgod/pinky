package gateway

import (
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShotName(t *testing.T) {
	ok := map[string]string{
		"onboarding-1":          "onboarding-1.png",
		"onboarding-1.png":      "onboarding-1.png",
		"bugs/login.PNG":        "bugs/login.PNG",
		"photo.jpg":             "photo.jpg",
		"photo.jpeg":            "photo.jpeg",
		" Pinky/shots/a.png ":   "shots/a.png",
		"2026-11-09 login page": "2026-11-09 login page.png",
	}
	for in, want := range ok {
		if got, why := shotName(in); why != "" || got != want {
			t.Errorf("shotName(%q) = %q, %q; want %q", in, got, why, want)
		}
	}
	for _, in := range []string{"", "../a.png", ".a.png", "a.gif", "a.webp", "shots/", "/tmp/a.png", "a:b.png"} {
		if got, why := shotName(in); why == "" {
			t.Errorf("shotName(%q) = %q, accepted", in, got)
		}
	}
}

func decodeFile(t *testing.T, p string, dec func(*os.File) (image.Image, error)) image.Image {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := dec(f)
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return img
}

func TestObserveSave(t *testing.T) {
	ctx := context.Background()
	g, dev := newTestGateway(t, func(o *Options) { o.DataDir = t.TempDir() })
	s, _ := connect(t, g, "Report a bug")
	dir := g.FilesDir("pixel")

	// A screenshot as a file, covered as the agent's are, without an image
	// in the result.
	dev.Show("messages")
	res := s.Observe(ctx, ObserveRequest{Save: "bugs/messages"})
	mustStatus(t, res, StatusSettled)
	want := filepath.Join(dir, "bugs", "messages.png")
	if res.Screenshot != nil || len(res.Saved) != 1 || res.Saved[0].Path != want || res.Saved[0].Name != "bugs/messages.png" ||
		!strings.Contains(res.Message, "Saved the screenshot as bugs/messages.png (1080×2400, ") ||
		!strings.Contains(res.Message, "One-time codes on the screen are covered in the screenshot.") {
		t.Fatalf("save: %+v", res)
	}
	img := decodeFile(t, want, func(f *os.File) (image.Image, error) { return png.Decode(f) })
	if b := img.Bounds(); b.Dx() != 1080 || b.Dy() != 2400 {
		t.Fatalf("saved at %v, not the screen's full size", b)
	}
	if !covered(img.At(codeRow.X, codeRow.Y)) {
		t.Fatal("the saved screenshot shows the one-time code")
	}
	if fi, err := os.Stat(want); err != nil || fi.Size() != res.Saved[0].Size {
		t.Fatalf("size %d, %v", res.Saved[0].Size, err)
	}

	// Again under the same name, with the image too: the file is replaced.
	dev.Show("settings")
	res = s.Observe(ctx, ObserveRequest{Save: "bugs/messages.png", Screenshot: true})
	if res.Screenshot == nil || len(res.Saved) != 1 || !strings.Contains(res.Message, "Saved the screenshot over bugs/messages.png") {
		t.Fatalf("replace: %+v", res)
	}
	if img := decodeFile(t, want, func(f *os.File) (image.Image, error) { return png.Decode(f) }); covered(img.At(codeRow.X, codeRow.Y)) {
		t.Fatal("the file was not replaced")
	}
	res = s.Observe(ctx, ObserveRequest{Save: "settings.jpg"})
	if len(res.Saved) != 1 {
		t.Fatalf("jpg: %+v", res)
	}
	decodeFile(t, filepath.Join(dir, "settings.jpg"), func(f *os.File) (image.Image, error) { return jpeg.Decode(f) })

	// Nothing of a blocked app, nor of a locked phone.
	dev.Show("wallet")
	res = s.Observe(ctx, ObserveRequest{Save: "wallet.png"})
	if len(res.Saved) != 0 || !strings.Contains(res.Message, "Nothing was saved.") {
		t.Fatalf("blocked app saved: %+v", res)
	}
	dev.Show("settings")
	dev.SetLocked(true)
	res = s.Observe(ctx, ObserveRequest{Save: "locked.png"})
	if len(res.Saved) != 0 || !strings.Contains(res.Message, "Nothing was saved.") {
		t.Fatalf("locked phone saved: %+v", res)
	}
	dev.SetLocked(false)
	for _, name := range []string{"wallet.png", "locked.png"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s written: %v", name, err)
		}
	}

	// Names that cannot be a file in the folder write nothing.
	for _, name := range []string{"../out.png", ".hidden.png", "clip.gif", "login v1.2"} {
		res := s.Observe(ctx, ObserveRequest{Save: name})
		if res.Error != ErrInvalidArgument {
			t.Errorf("save %q: %+v", name, res)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "out.png")); !os.IsNotExist(err) {
		t.Fatalf("wrote out of the folder: %v", err)
	}

	var texts []string
	for _, st := range taskSteps(g, s) {
		if st.Tool == "phone_observe" {
			texts = append(texts, st.Text)
		}
	}
	if got := strings.Join(texts, "\n"); got != "Saved a screenshot as “bugs/messages.png”\nSaved a screenshot as “bugs/messages.png”\nSaved a screenshot as “settings.jpg”" {
		t.Fatalf("steps %q", texts)
	}
}

// Saving touches nothing on the phone, so read-only and watch-only agents
// and read-only mode may.
func TestObserveSaveReadOnly(t *testing.T) {
	ctx := context.Background()
	g, dev := newTestGateway(t, func(o *Options) { o.DataDir = t.TempDir() })
	g.SetReadOnly("pixel", true)
	s := g.OpenSession(agent)
	t.Cleanup(s.Close)
	mustStatus(t, s.Connect(ctx, ConnectRequest{Task: "Look around", ReadOnly: true}), StatusSettled)
	dev.Show("settings")
	if res := s.Observe(ctx, ObserveRequest{Save: "settings.png"}); len(res.Saved) != 1 {
		t.Fatalf("read-only save: %+v", res)
	}
	if res := s.Release(ctx, ReleaseRequest{Export: true}); len(res.Saved) != 2 {
		t.Fatalf("read-only export: %+v", res)
	}

	g.SetReadOnly("pixel", false)
	g.SetAgentScope(agent.Name, Scope{Mode: ScopeWatchOnly})
	s2 := g.OpenSession(agent)
	t.Cleanup(s2.Close)
	mustStatus(t, s2.Connect(ctx, ConnectRequest{Task: "Watch"}), StatusSettled)
	if res := s2.Observe(ctx, ObserveRequest{Save: "watch.png"}); len(res.Saved) != 1 {
		t.Fatalf("watch-only save: %+v", res)
	}
}

func TestObserveSaveWithoutDataDir(t *testing.T) {
	g, _ := newTestGateway(t)
	s, _ := connect(t, g, "Report a bug")
	if res := s.Observe(context.Background(), ObserveRequest{Save: "a.png"}); res.Error != ErrUnsupported {
		t.Fatalf("%+v", res)
	}
	if res := s.Release(context.Background(), ReleaseRequest{Export: true}); res.Error != ErrUnsupported {
		t.Fatalf("%+v", res)
	}
}

func TestReleaseExport(t *testing.T) {
	ctx := context.Background()
	v := newVideoDev("pixel")
	g := newVideoGateway(t, v, func(o *Options) { o.NoScreenshots = false })
	g.SetRecording(true)
	s, _ := connect(t, g, "Reorder lunch")
	id := taskID(t, g, s)
	st := newStreamer(t, v.open(t))
	st.config()
	st.pictures(10, true)
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "key", Key: "home"}), StatusSettled)
	res := s.Release(ctx, ReleaseRequest{Summary: "Done.", Export: true})
	mustStatus(t, res, StatusSettled)
	dir := g.FilesDir("pixel")
	if len(res.Saved) != 2 {
		t.Fatalf("saved %+v", res)
	}
	html, js := res.Saved[0], res.Saved[1]
	if filepath.Dir(html.Path) != dir || !strings.HasPrefix(html.Name, "pinky-task-") || !strings.HasSuffix(html.Name, ".html") ||
		js.Name != strings.TrimSuffix(html.Name, ".html")+".json" {
		t.Fatalf("names %+v", res.Saved)
	}
	if !strings.Contains(res.Message, "Released Pixel 9. Saved the report as "+html.Name+" (its steps, with the screenshots Pinky kept) and "+js.Name) ||
		!strings.Contains(res.Message, "Its screen recording stays on Pinky's computer") {
		t.Fatalf("message %q", res.Message)
	}
	b, err := os.ReadFile(html.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Reorder lunch", "Opened Dinely", "data:image/jpeg;base64,", "report for the agent", "Finished: done"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the report lacks %q", want)
		}
	}
	// The recording does not cover one-time codes, so it stays here.
	if strings.Contains(string(b), "<video") {
		t.Error("the report plays the recording")
	}
	var doc struct {
		Tasks []exportTask `json:"tasks"`
	}
	if b, err := os.ReadFile(js.Path); err != nil || json.Unmarshal(b, &doc) != nil || len(doc.Tasks) != 1 || doc.Tasks[0].ID != id ||
		doc.Tasks[0].Recording != nil || !strings.Contains(string(b), exportStepText) {
		t.Fatalf("json %+v, %v", doc, err)
	}
	if mp4s, _ := filepath.Glob(filepath.Join(dir, "*.mp4")); len(mp4s) != 0 {
		t.Fatalf("recordings in the folder: %v", mp4s)
	}
	// The person's own export still has it.
	g.Flush()
	if _, err := g.Recording(ctx, id); err != nil {
		t.Fatalf("recording: %v", err)
	}

	// The task's history says it, in plain words.
	steps := taskSteps(g, s)
	if n := len(steps); n < 2 || steps[n-2].Text != exportStepText || steps[n-1].Text != "Finished: done" {
		t.Fatalf("steps %+v", steps)
	}
}

// A task without a recording exports its report, and says nothing of one.
func TestReleaseExportWithoutRecording(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	g := newExtraGateway(t, x)
	s, _ := connect(t, g, "Look around")
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "key", Key: "home"}), StatusSettled)
	res := s.Release(ctx, ReleaseRequest{Export: true})
	if len(res.Saved) != 2 || strings.Contains(res.Message, "recording") {
		t.Fatalf("%+v", res)
	}
	// Another export the same second gets names of its own.
	s2, _ := connect(t, g, "Look again")
	res2 := s2.Release(ctx, ReleaseRequest{Export: true})
	if len(res2.Saved) != 2 || res2.Saved[0].Path == res.Saved[0].Path {
		t.Fatalf("%+v", res2)
	}
}

// A finance app the person allowed for the task is never saved: Pinky
// keeps no picture of it.
func TestObserveSaveAllowedFinanceApp(t *testing.T) {
	ctx := context.Background()
	g, dev := newTestGateway(t, func(o *Options) { o.DataDir = t.TempDir(); o.NoScreenshots = false })
	s, _ := connect(t, g, "Bookkeeping")
	go func() {
		g.Decide(waitNeed(t, g).ID, AllowTask, "")
	}()
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Google Wallet"}), StatusSettled)
	if dev.Current() != "wallet" {
		t.Fatalf("on %s", dev.Current())
	}
	res := s.Observe(ctx, ObserveRequest{Save: "wallet.png"})
	if len(res.Saved) != 0 || !strings.Contains(res.Message, "Nothing was saved.") {
		t.Fatalf("saved: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(g.FilesDir("pixel"), "wallet.png")); !os.IsNotExist(err) {
		t.Fatalf("written: %v", err)
	}
}
