//go:build e2e && !windows

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/telemetry"
)

// TestTelemetryInTheApp drives the e2e build of the desktop app over MyGo's
// automation socket, with a fake backend: the note about usage statistics,
// Help › Send Feedback…, a crash of the app (SIGQUIT) offered at the next
// start and sent, and a clean quit that leaves nothing behind. It opens
// windows, so it needs a display with the screen unlocked:
//
//	go test -tags e2e -run TestTelemetryInTheApp ./cmd/pinky
func TestTelemetryInTheApp(t *testing.T) {
	if testing.Short() {
		t.Skip("opens the app")
	}
	bin := filepath.Join(t.TempDir(), "pinky")
	build := exec.Command("go", "build", "-tags", "e2e", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := pinkyHome(t)
	backend := newFakeBackend(t)
	data := filepath.Join(home, "data")

	// The first run: the note, then feedback, then a crash.
	app := startApp(t, bin, home, backend.URL)
	win := app.window(t)
	app.waitText(t, win, "Pinky shares anonymous usage statistics")
	if runs, _ := filepath.Glob(filepath.Join(data, "crashes", "app-*.run")); len(runs) != 1 {
		t.Fatalf("crash files while running: %v", runs)
	}
	app.press(t, win, "OK")
	waitFile(t, filepath.Join(data, "telemetry.json"), `"notice_shown"`)

	app.call(t, "menu", map[string]any{"window": win, "path": []string{"Help", "Send Feedback…"}}, nil)
	app.waitText(t, win, "Send feedback")
	app.setValue(t, win, "fb-message", "Sent by the e2e test.")
	app.waitEnabled(t, win, "Send")
	app.press(t, win, "Send")
	fb := backend.wait(t, telemetry.PathFeedback)
	var f telemetry.Feedback
	if err := json.Unmarshal(fb, &f); err != nil || f.Message != "Sent by the e2e test." || !strings.Contains(f.Diagnostics, "Phones (") {
		t.Fatalf("feedback %s: %v", fb, err)
	}

	app.cmd.Process.Signal(syscall.SIGQUIT)
	app.wait(t)

	// The next run offers the crash and sends it.
	app = startApp(t, bin, home, backend.URL)
	win = app.window(t)
	app.waitText(t, win, "Pinky quit unexpectedly")
	app.press(t, win, "Send Report")
	cr := backend.wait(t, telemetry.PathCrash)
	var r telemetry.CrashReport
	if err := json.Unmarshal(cr, &r); err != nil || r.Process != "app" || !strings.HasPrefix(r.Reason, "SIGQUIT") || !strings.Contains(r.Stack, "goroutine ") {
		t.Fatalf("crash report %s: %v", cr, err)
	}
	if strings.Contains(r.Stack, os.Getenv("HOME")+"/") {
		t.Fatalf("the stack keeps the home folder:\n%s", r.Stack)
	}

	// A clean quit leaves no crash file, and the next run finds nothing.
	run(t, bin, home, "quit")
	app.wait(t)
	if left, _ := filepath.Glob(filepath.Join(data, "crashes", "*")); len(left) != 0 {
		t.Fatalf("left after a clean quit: %v", left)
	}
	app = startApp(t, bin, home, backend.URL)
	win = app.window(t)
	app.waitText(t, win, "Pixel 9 (fake)")
	if app.hasText(t, win, "quit unexpectedly") || app.hasText(t, win, "Pinky shares anonymous usage statistics") {
		t.Fatal("asked again after a clean quit")
	}
	run(t, bin, home, "quit")
	app.wait(t)
}

// fakeBackend keeps the bodies posted to it, by path.
type fakeBackend struct {
	*httptest.Server
	mu     sync.Mutex
	bodies map[string][][]byte
}

func newFakeBackend(t *testing.T) *fakeBackend {
	b := &fakeBackend{bodies: map[string][][]byte{}}
	b.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		b.mu.Lock()
		b.bodies[r.URL.Path] = append(b.bodies[r.URL.Path], body)
		b.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(b.Close)
	return b
}

func (b *fakeBackend) wait(t *testing.T, path string) []byte {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		b.mu.Lock()
		got := b.bodies[path]
		b.mu.Unlock()
		if len(got) > 0 {
			return got[len(got)-1]
		}
	}
	t.Fatalf("nothing posted to %s", path)
	return nil
}

type uiApp struct {
	cmd  *exec.Cmd
	conn net.Conn
	r    *bufio.Reader
	id   int
	done chan struct{} // closed when the app has exited
	out  *syncBuffer
}

// startApp runs the e2e build of the app with fake phones, a backend at
// url and an automation socket.
func startApp(t *testing.T, bin, home, url string) *uiApp {
	t.Helper()
	sock := filepath.Join(home, "ui.sock")
	os.Remove(sock)
	cmd := exec.Command(bin, "--fake")
	cmd.Env = append(env(home), "MYGO_AUTOMATION="+sock, telemetry.EndpointEnv+"="+url, "DO_NOT_TRACK=")
	out := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	a := &uiApp{cmd: cmd, done: make(chan struct{}), out: out}
	go func() {
		cmd.Wait()
		close(a.done)
	}()
	t.Cleanup(func() {
		if a.conn != nil {
			a.conn.Close()
		}
		select {
		case <-a.done:
		default:
			cmd.Process.Kill()
			<-a.done
		}
	})
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			a.conn, a.r = conn, bufio.NewReader(conn)
			return a
		}
		if time.Now().After(deadline) {
			t.Fatalf("no automation socket: %v\n%s", err, out.String())
		}
	}
}

// wait waits for the app to exit.
func (a *uiApp) wait(t *testing.T) {
	t.Helper()
	select {
	case <-a.done:
	case <-time.After(20 * time.Second):
		t.Fatalf("the app did not exit\n%s", a.out.String())
	}
	if a.conn != nil {
		a.conn.Close()
		a.conn = nil
	}
}

func (a *uiApp) call(t *testing.T, method string, params any, result any) {
	t.Helper()
	if err := a.try(method, params, result); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func (a *uiApp) try(method string, params any, result any) error {
	a.id++
	req, _ := json.Marshal(map[string]any{"id": a.id, "method": method, "params": params})
	a.conn.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := a.conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := a.r.ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp struct {
		Result json.RawMessage
		Error  *struct{ Code, Message string }
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
	}
	if result != nil {
		return json.Unmarshal(resp.Result, result)
	}
	return nil
}

// window returns the main window once it shows.
func (a *uiApp) window(t *testing.T) int {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		var res struct {
			Windows []struct {
				ID      int
				Title   string
				Visible bool
			}
		}
		a.call(t, "windows", nil, &res)
		for _, w := range res.Windows {
			if w.Visible && w.Title == "Pinky" {
				return w.ID
			}
		}
	}
	t.Fatalf("no window\n%s", a.out.String())
	return 0
}

type uiNode struct {
	ID, Role, Key, Label, Value string
	States                      []string
}

func (a *uiApp) nodes(t *testing.T, win int) []uiNode {
	t.Helper()
	var res struct{ Nodes []uiNode }
	a.call(t, "tree", map[string]any{"window": win}, &res)
	return res.Nodes
}

func (a *uiApp) hasText(t *testing.T, win int, text string) bool {
	for _, n := range a.nodes(t, win) {
		if strings.Contains(n.Label, text) || strings.Contains(n.Value, text) {
			return true
		}
	}
	return false
}

func (a *uiApp) waitText(t *testing.T, win int, text string) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if a.hasText(t, win, text) {
			return
		}
	}
	var labels []string
	for _, n := range a.nodes(t, win) {
		labels = append(labels, n.Label)
	}
	t.Fatalf("no %q in %q", text, labels)
}

// find returns the button labelled label, or the element with that key.
func (a *uiApp) find(t *testing.T, win int, label string) (uiNode, bool) {
	for _, n := range a.nodes(t, win) {
		if n.Key == label || (n.Label == label && n.Role == "button") {
			return n, true
		}
	}
	return uiNode{}, false
}

func (a *uiApp) press(t *testing.T, win int, label string) {
	t.Helper()
	n, ok := a.find(t, win, label)
	if !ok {
		t.Fatalf("no %q to press", label)
	}
	a.call(t, "act", map[string]any{"window": win, "id": n.ID, "action": "press"}, nil)
}

func (a *uiApp) setValue(t *testing.T, win int, key, text string) {
	t.Helper()
	a.call(t, "act", map[string]any{"window": win, "key": key, "action": "setValue", "text": text}, nil)
}

func (a *uiApp) waitEnabled(t *testing.T, win int, label string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if n, ok := a.find(t, win, label); ok && !contains(n.States, "disabled") {
			return
		}
	}
	t.Fatalf("%q stays disabled", label)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func waitFile(t *testing.T, path, text string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), text) {
			return
		}
	}
	b, _ := os.ReadFile(path)
	t.Fatalf("%s lacks %q:\n%s", path, text, b)
}
