package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/ios/standalone/agent"
)

const self = "cc.diygod.pinky.standalone"

// fakePhone is a phone with a home screen, Pinky's app and Calculator.
type fakePhone struct {
	mu      sync.Mutex
	app     string
	display string
	inputs  []string
	onHome  func()
	noTree  bool // reads fail, as when remoteAXService does not answer
}

func (f *fakePhone) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noTree {
		return nil, errors.New("remote accessibility: timed out")
	}
	s := &screen.Screen{Width: 1080, Height: 2340, App: screen.App{ID: f.app}, Orientation: "portrait", CapturedAt: time.Now()}
	switch f.app {
	case "com.apple.calculator":
		s.App.Name = "Calculator"
		s.Elements = append(s.Elements, screen.Element{Role: screen.RoleText, Label: f.display, Bounds: screen.Rect{X: 0, Y: 400, W: 1080, H: 200}})
		for i, l := range []string{"AC", "1", "2", "3", "4", "5", "6", "7", "8", "9"} {
			s.Elements = append(s.Elements, screen.Element{Role: screen.RoleButton, Label: l, Bounds: screen.Rect{X: (i % 4) * 270, Y: 1000 + (i/4)*270, W: 270, H: 270}})
		}
	case self:
		s.App.Name = "Pinky"
		s.Elements = append(s.Elements, screen.Element{Role: screen.RoleButton, Label: "Approve", Bounds: screen.Rect{X: 100, Y: 1800, W: 400, H: 120}})
	default:
		s.App = screen.App{Name: "Home Screen", ID: "com.apple.springboard"}
	}
	return s, nil
}

func (f *fakePhone) Tap(ctx context.Context, p screen.Point, opt driver.TapOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, fmt.Sprintf("tap %d,%d", p.X, p.Y))
	if f.app == "com.apple.calculator" && p.Y >= 1000 {
		i := (p.Y-1000)/270*4 + p.X/270
		labels := []string{"AC", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
		if i < len(labels) {
			if labels[i] == "AC" {
				f.display = ""
			} else {
				f.display += labels[i]
			}
		}
	}
	return nil
}

func (f *fakePhone) Swipe(ctx context.Context, from, to screen.Point, dur time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, "swipe")
	return nil
}

func (f *fakePhone) Type(ctx context.Context, text string, opt driver.TypeOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, "type "+text)
	return nil
}

func (f *fakePhone) Key(ctx context.Context, k driver.Key) error {
	f.mu.Lock()
	f.inputs = append(f.inputs, "key "+string(k))
	if k == driver.KeyHome {
		f.app = "com.apple.springboard"
	}
	h := f.onHome
	f.mu.Unlock()
	if k == driver.KeyHome && h != nil {
		h()
	}
	return nil
}

func (f *fakePhone) LaunchID(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, "launch "+id)
	f.app = id
	return nil
}

func (f *fakePhone) ScreenshotPNG(ctx context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []byte("\x89PNG " + f.app + f.display), nil
}

func (f *fakePhone) Close() error { return nil }

func (f *fakePhone) taken() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.inputs...)
}

func newTest(t *testing.T, app string) (*Engine, *fakePhone) {
	t.Helper()
	e := New(Config{Self: []string{self}, Settle: time.Millisecond})
	f := &fakePhone{app: app}
	f.onHome = func() { e.SetUIActive(false) }
	e.SetPhone(f)
	return e, f
}

func TestGuard(t *testing.T) {
	ctx := context.Background()
	e, f := newTest(t, self)

	e.SetUIActive(true)
	if err := e.Tap(ctx, screen.Point{X: 1, Y: 1}, false); !errors.Is(err, ErrOwnUI) {
		t.Fatalf("tap with Pinky's UI active: %v", err)
	}
	e.SetUIActive(false)
	// The app has not reported yet, but the screen shows Pinky's app.
	if err := e.Tap(ctx, screen.Point{X: 300, Y: 1860}, false); !errors.Is(err, ErrOwnUI) {
		t.Fatalf("tap on Pinky's screen: %v", err)
	}
	if err := e.Type(ctx, "x", driver.TypeOptions{}); !errors.Is(err, ErrOwnUI) {
		t.Fatalf("type on Pinky's screen: %v", err)
	}
	if err := e.Launch(ctx, self); !errors.Is(err, ErrOwnLaunch) {
		t.Fatalf("launch Pinky: %v", err)
	}
	if got := f.taken(); len(got) != 0 {
		t.Fatalf("inputs reached the phone: %v", got)
	}

	f.mu.Lock()
	f.app = "com.apple.calculator"
	f.mu.Unlock()
	e.mu.Lock()
	e.last = nil
	e.mu.Unlock()
	if err := e.Tap(ctx, screen.Point{X: 10, Y: 10}, false); err != nil {
		t.Fatalf("tap in Calculator: %v", err)
	}

	// No input while an approval waits.
	ctx2, cancel := context.WithCancel(ctx)
	go e.RequestApproval(ctx2, "send it")
	waitFor(t, func() bool { return e.Pending() != nil })
	if err := e.Tap(ctx, screen.Point{X: 10, Y: 10}, false); !errors.Is(err, ErrApproval) {
		t.Fatalf("tap during an approval: %v", err)
	}
	cancel()
	waitFor(t, func() bool { return e.Pending() == nil })
	if got := f.taken(); len(got) != 1 {
		t.Fatalf("inputs = %v, want one tap", got)
	}
}

func TestStale(t *testing.T) {
	// A tap aimed at Calculator does nothing once another app replaced it,
	// as when the person approves in Pinky and then goes home.
	ctx := context.Background()
	e, f := newTest(t, "com.apple.springboard")
	if err := e.tapIn(ctx, "com.apple.calculator", screen.Point{X: 10, Y: 1010}, false); !errors.Is(err, ErrStale) {
		t.Fatalf("tap aimed at Calculator on the home screen: %v", err)
	}
	if got := f.taken(); len(got) != 0 {
		t.Fatalf("inputs reached the phone: %v", got)
	}
	f.mu.Lock()
	f.app = "com.apple.calculator"
	f.mu.Unlock()
	e.mu.Lock()
	e.last = nil
	e.mu.Unlock()
	if err := e.tapIn(ctx, "com.apple.calculator", screen.Point{X: 10, Y: 1010}, false); err != nil {
		t.Fatalf("tap in Calculator: %v", err)
	}
	if got := f.taken(); len(got) != 1 {
		t.Fatalf("inputs = %v, want one tap", got)
	}
}

func TestApprovalBack(t *testing.T) {
	// The person opens Pinky's app to approve. Afterwards the engine leaves
	// it and opens Calculator again, so the agent can go on.
	ctx := context.Background()
	e, f := newTest(t, "com.apple.calculator")
	tl := &tools{e: e}
	if r := tl.Call(ctx, "observe", nil); r.IsError {
		t.Fatal(r.Text)
	}
	got := make(chan agent.Result, 1)
	go func() { got <- tl.Call(ctx, "request_approval", map[string]any{"action": "press ="}) }()
	waitFor(t, func() bool { return e.Pending() != nil })
	f.mu.Lock()
	f.app = self
	f.mu.Unlock()
	e.SetUIActive(true)
	if err := e.Answer(e.Pending().ID, true); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.IsError || !strings.Contains(r.Text, "approved") || !strings.Contains(r.Text, "went back to com.apple.calculator") {
		t.Fatalf("result: %s", r.Text)
	}
	if in := f.taken(); strings.Join(in, ",") != "key home,launch com.apple.calculator" {
		t.Fatalf("inputs = %v", in)
	}
	if r := tl.Call(ctx, "tap", map[string]any{"x": 300, "y": 1010}); r.IsError {
		t.Fatalf("tap after going back: %s", r.Text)
	}

	// Answered from the notification, with Pinky's app not in front: the
	// engine stays where it is.
	go func() { got <- tl.Call(ctx, "request_approval", map[string]any{"action": "press ="}) }()
	waitFor(t, func() bool { return e.Pending() != nil })
	if err := e.Answer(e.Pending().ID, false); err != nil {
		t.Fatal(err)
	}
	if r := <-got; strings.Contains(r.Text, "went back") {
		t.Fatalf("result: %s", r.Text)
	}
	if in := f.taken(); len(in) != 3 {
		t.Fatalf("inputs = %v", in)
	}
}

func TestNoPhone(t *testing.T) {
	e := New(Config{Self: []string{self}})
	if err := e.Tap(context.Background(), screen.Point{}, false); !errors.Is(err, ErrNoPhone) {
		t.Fatalf("tap: %v", err)
	}
	var r struct {
		OK     bool   `json:"ok"`
		Result Status `json:"result"`
	}
	if err := json.Unmarshal(e.Handle(context.Background(), []byte(`{"op":"status"}`)), &r); err != nil || !r.OK || r.Result.State != "connecting" {
		t.Fatalf("status = %+v, %v", r, err)
	}
	if b := e.Handle(context.Background(), []byte(`{"op":"nope"}`)); !strings.Contains(string(b), `"ok":false`) {
		t.Fatalf("unknown op: %s", b)
	}
}

func TestProbe(t *testing.T) {
	e, f := newTest(t, self)
	e.SetUIActive(true)
	out, err := e.Probe(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["foreground"] != "com.apple.calculator" || out["screenshot_bytes"] != len("\x89PNG com.apple.calculator") {
		t.Fatalf("probe = %v", out)
	}
	changes, _ := out["changes"].([]string)
	if len(changes) == 0 || !strings.Contains(strings.Join(changes, "\n"), `"7"`) {
		t.Fatalf("changes = %v", changes)
	}
	want := []string{"key home", "launch com.apple.calculator"}
	if got := f.taken(); len(got) != 3 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("inputs = %v", got)
	}
}

func TestProbeWithoutTree(t *testing.T) {
	e, f := newTest(t, "com.apple.springboard")
	f.noTree = true
	// Without a point it reads pixels and taps nothing.
	out, err := e.Probe(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["observe_error"] == nil || out["screenshot_bytes"] == 0 || out["tap_ms"] != nil {
		t.Fatalf("probe = %v", out)
	}
	// With one it taps there and compares screenshots.
	out, err = e.Probe(context.Background(), "", &screen.Point{X: 7*270%1080 + 10, Y: 1000 + 7/4*270 + 10})
	if err != nil {
		t.Fatal(err)
	}
	if out["pixels_changed"] != true || out["tap_error"] != nil {
		t.Fatalf("probe = %v", out)
	}
	if f.display != "7" {
		t.Fatalf("display = %q", f.display)
	}
}

func TestTapBlindNeedsALaunch(t *testing.T) {
	e, f := newTest(t, "com.apple.calculator")
	if err := e.tapBlind(context.Background(), screen.Point{X: 10, Y: 1010}, time.Now().Add(-time.Minute)); err == nil {
		t.Fatal("tapped long after the launch")
	}
	e.SetUIActive(true)
	if err := e.tapBlind(context.Background(), screen.Point{X: 10, Y: 1010}, time.Now()); !errors.Is(err, ErrOwnUI) {
		t.Fatalf("tapped over Pinky's UI: %v", err)
	}
	if got := f.taken(); len(got) != 0 {
		t.Fatalf("inputs = %v", got)
	}
}

func TestSoakWithoutTree(t *testing.T) {
	e, f := newTest(t, "com.apple.springboard")
	f.noTree = true
	if err := e.StartSoak(SoakConfig{Tap: true, Every: time.Second, Minutes: 1}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s := e.Soak(); return s != nil && s.Pixels >= 2 })
	e.StopTask()
	waitFor(t, func() bool { return e.Soak().Done })
	if s := e.Soak(); s.OK != 0 {
		t.Fatalf("soak = %+v", s)
	}
	for _, in := range f.taken() {
		if strings.HasPrefix(in, "tap") {
			t.Fatalf("the soak tapped without reading the screen: %v", f.taken())
		}
	}
}

func TestSoak(t *testing.T) {
	e, f := newTest(t, "com.apple.springboard")
	if err := e.StartSoak(SoakConfig{Tap: true, Every: time.Second, Minutes: 1}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s := e.Soak(); return s != nil && s.Rounds >= 2 })
	// The person switches away: the soak stops acting.
	f.mu.Lock()
	f.app = "com.apple.springboard"
	n := len(f.inputs)
	f.mu.Unlock()
	waitFor(t, func() bool { return e.Soak().Away >= 1 })
	e.StopTask()
	waitFor(t, func() bool { return e.Soak().Done })
	s := e.Soak()
	if s.OK < 2 {
		t.Fatalf("soak = %+v", s)
	}
	if got := f.taken(); len(got) != n {
		t.Fatalf("the soak acted away from Calculator: %v", got[n:])
	}
	if hb := e.Heartbeat(); !strings.Contains(hb, "soak=") || !strings.Contains(hb, "tap=") {
		t.Fatalf("heartbeat = %s", hb)
	}
}

// fakeClaude answers the Messages API from a script, one reply per request,
// and records the requests.
type fakeClaude struct {
	mu      sync.Mutex
	replies []func(req map[string]any) string
	reqs    []map[string]any
	headers []http.Header
}

func (fc *fakeClaude) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var req map[string]any
	json.Unmarshal(b, &req)
	fc.mu.Lock()
	fc.reqs = append(fc.reqs, req)
	fc.headers = append(fc.headers, r.Header.Clone())
	var f func(map[string]any) string
	if len(fc.replies) > 0 {
		f, fc.replies = fc.replies[0], fc.replies[1:]
	}
	fc.mu.Unlock()
	if f == nil {
		http.Error(w, `{"error":{"message":"no more replies"}}`, http.StatusBadRequest)
		return
	}
	io.WriteString(w, f(req))
}

func toolUse(id, name string, input map[string]any) func(map[string]any) string {
	return func(map[string]any) string {
		in, _ := json.Marshal(input)
		return fmt.Sprintf(`{"content":[{"type":"text","text":"ok","citations":null},{"type":"tool_use","id":%q,"name":%q,"input":%s}],"stop_reason":"tool_use","usage":{"input_tokens":1000,"output_tokens":100}}`, id, name, in)
	}
}

// lastResult is the text of the last tool result in a request.
func lastResult(req map[string]any) string {
	msgs, _ := req["messages"].([]any)
	if len(msgs) == 0 {
		return ""
	}
	m, _ := msgs[len(msgs)-1].(map[string]any)
	blocks, _ := m["content"].([]any)
	var out []string
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		if bm["type"] != "tool_result" {
			continue
		}
		cs, _ := bm["content"].([]any)
		for _, c := range cs {
			cm, _ := c.(map[string]any)
			if s, ok := cm["text"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return strings.Join(out, "\n")
}

func TestTask(t *testing.T) {
	const key = "sk-ant-test-0123456789"
	fc := &fakeClaude{}
	var sevenRef string
	fc.replies = []func(map[string]any) string{
		toolUse("t1", "launch", map[string]any{"bundle_id": "com.apple.calculator"}),
		func(req map[string]any) string {
			// Find the ref of the button "7" in the screen the launch returned.
			for _, l := range strings.Split(lastResult(req), "\n") {
				if strings.Contains(l, `button "7"`) {
					sevenRef = strings.Trim(strings.Fields(l)[0], "[]")
				}
			}
			var ref int
			fmt.Sscan(sevenRef, &ref)
			return toolUse("t2", "tap", map[string]any{"ref": ref})(req)
		},
		toolUse("t3", "request_approval", map[string]any{"action": "Clear the calculator"}),
		func(req map[string]any) string {
			if !strings.HasPrefix(lastResult(req), "approved") {
				t.Errorf("approval result = %q", lastResult(req))
			}
			return toolUse("t4", "tap", map[string]any{"x": 135, "y": 1135})(req)
		},
		toolUse("t5", "finish", map[string]any{"outcome": "done", "summary": "Typed 7, then cleared it."}),
	}
	srv := httptest.NewServer(fc)
	defer srv.Close()

	var logs []string
	var lmu sync.Mutex
	e := New(Config{Self: []string{self}, Settle: time.Millisecond, Agent: agent.Config{BaseURL: srv.URL},
		Log: func(s string) { lmu.Lock(); logs = append(logs, s); lmu.Unlock() }})
	f := &fakePhone{app: self}
	f.onHome = func() { e.SetUIActive(false) }
	e.SetPhone(f)
	e.SetUIActive(true)

	if b := e.Handle(context.Background(), []byte(`{"op":"task_start","task":"Type 7 in Calculator, then clear it","api_key":"`+key+`"}`)); !strings.Contains(string(b), `"ok":true`) {
		t.Fatalf("task_start: %s", b)
	}
	// Answer the approval from "the app", as the person would.
	var approvalID string
	waitFor(t, func() bool {
		for _, ev := range e.Events(0) {
			if ev.Kind == "approval" {
				approvalID = ev.ID
				return true
			}
		}
		return false
	})
	// While it waits, the agent cannot act.
	if err := e.Tap(context.Background(), screen.Point{X: 1, Y: 1}, false); !errors.Is(err, ErrApproval) {
		t.Fatalf("tap during approval: %v", err)
	}
	if b := e.Handle(context.Background(), []byte(`{"op":"approve","id":"`+approvalID+`","yes":true}`)); !strings.Contains(string(b), `"ok":true`) {
		t.Fatalf("approve: %s", b)
	}
	waitFor(t, func() bool { st := e.Task(); return st != nil && !st.Running() })
	st := e.Task()
	if st.End != agent.Done || st.Summary != "Typed 7, then cleared it." || st.Steps != 4 {
		t.Fatalf("task = %+v", st)
	}
	if f.display != "" {
		t.Fatalf("display = %q, want cleared", f.display)
	}
	want := []string{"key home", "launch com.apple.calculator", "tap 945,1405", "tap 135,1135"}
	if got := f.taken(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("inputs = %v, want %v", got, want)
	}

	// What went to the API.
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.reqs) != 5 {
		t.Fatalf("%d requests", len(fc.reqs))
	}
	if h := fc.headers[0]; h.Get("Authorization") != "Bearer "+key || h.Get("Anthropic-Version") == "" ||
		!strings.Contains(h.Get("Anthropic-Beta"), "context-management") || !strings.Contains(h.Get("Anthropic-Beta"), "server-side-fallback") {
		t.Fatalf("headers = %v", h)
	}
	r0 := fc.reqs[0]
	if r0["model"] != agent.DefaultModel || r0["fallbacks"] != "default" || r0["cache_control"] == nil {
		t.Fatalf("request = %v", r0)
	}
	tools, _ := r0["tools"].([]any)
	if last, _ := tools[len(tools)-1].(map[string]any); last["name"] != "finish" || last["cache_control"] == nil {
		t.Fatalf("last tool = %v", last)
	}
	first, _ := json.Marshal(r0["messages"])
	if !strings.Contains(string(first), "Type 7 in Calculator") || !strings.Contains(string(first), "Home Screen") {
		t.Fatalf("first message = %s", first)
	}
	// The echo drops null fields.
	if b, _ := json.Marshal(fc.reqs[1]["messages"]); strings.Contains(string(b), "citations") {
		t.Fatalf("null field echoed: %s", b)
	}

	// The key stays out of what the engine shows and logs.
	status, _ := json.Marshal(e.Status(0))
	lmu.Lock()
	all := string(status) + strings.Join(logs, "\n")
	lmu.Unlock()
	if strings.Contains(all, key) {
		t.Fatal("the API key leaked into the status or the log")
	}
}

func TestStopDuringApproval(t *testing.T) {
	fc := &fakeClaude{replies: []func(map[string]any) string{
		toolUse("t1", "request_approval", map[string]any{"action": "Do something"}),
	}}
	srv := httptest.NewServer(fc)
	defer srv.Close()
	e := New(Config{Self: []string{self}, Settle: time.Millisecond, Agent: agent.Config{BaseURL: srv.URL}})
	e.SetPhone(&fakePhone{app: "com.apple.springboard"})
	if err := e.StartTask(context.Background(), "x", "k", "", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return e.Pending() != nil })
	e.Handle(context.Background(), []byte(`{"op":"stop"}`))
	waitFor(t, func() bool { st := e.Task(); return !st.Running() })
	if st := e.Task(); st.End != agent.Stopped {
		t.Fatalf("task = %+v", st)
	}
	if e.Pending() != nil {
		t.Fatal("the approval still waits")
	}
	var ended bool
	for _, ev := range e.Events(0) {
		ended = ended || ev.Kind == "task_ended" && ev.End == "stopped"
	}
	if !ended {
		t.Fatalf("events = %+v", e.Events(0))
	}
}

func TestBlockedToolResult(t *testing.T) {
	// The agent's tap on Pinky's own screen comes back as blocked, and
	// nothing reaches the phone.
	fc := &fakeClaude{}
	fc.replies = []func(map[string]any) string{
		toolUse("t1", "tap", map[string]any{"x": 300, "y": 1860}),
		func(req map[string]any) string {
			if !strings.HasPrefix(lastResult(req), "error: blocked") {
				t.Errorf("result = %q", lastResult(req))
			}
			return toolUse("t2", "finish", map[string]any{"outcome": "failed", "summary": "blocked"})(req)
		},
	}
	srv := httptest.NewServer(fc)
	defer srv.Close()
	e := New(Config{Self: []string{self}, Settle: time.Millisecond, Agent: agent.Config{BaseURL: srv.URL}})
	f := &fakePhone{app: "com.apple.springboard"}
	e.SetPhone(f)
	if err := e.StartTask(context.Background(), "x", "k", "", ""); err != nil {
		t.Fatal(err)
	}
	// Pinky's app comes to the front after the task started.
	f.mu.Lock()
	f.app = self
	f.mu.Unlock()
	e.SetUIActive(true)
	waitFor(t, func() bool { st := e.Task(); return !st.Running() })
	if got := f.taken(); len(got) != 0 {
		t.Fatalf("inputs = %v", got)
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatal("timed out")
}
