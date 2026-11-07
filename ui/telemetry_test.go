package ui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/telemetry"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// reports is a telemetry backend that keeps what it is sent.
type reports struct {
	mu       sync.Mutex
	usage    []telemetry.Usage
	crashes  []telemetry.CrashReport
	feedback []telemetry.Feedback
	err      error
}

func (r *reports) Usage(_ context.Context, u telemetry.Usage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage = append(r.usage, u)
	return r.err
}

func (r *reports) Crash(_ context.Context, c telemetry.CrashReport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.crashes = append(r.crashes, c)
	return nil
}

func (r *reports) Feedback(_ context.Context, f telemetry.Feedback) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.feedback = append(r.feedback, f)
	return nil
}

func (r *reports) setErr(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

func (r *reports) sent() ([]telemetry.CrashReport, []telemetry.Feedback) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]telemetry.CrashReport(nil), r.crashes...), append([]telemetry.Feedback(nil), r.feedback...)
}

type telApp struct {
	*testApp
	tel     *telemetry.Client
	dir     string
	mu      sync.Mutex
	changes []Settings
}

// newTelApp is the client on the fake with a telemetry client that sends
// to b (nil: a build with nowhere to send), and crash reports in dir.
func newTelApp(t *testing.T, b telemetry.Backend, crashes ...telemetry.CrashReport) *telApp {
	t.Helper()
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv(telemetry.EndpointEnv, "")
	dir := t.TempDir()
	cr := telemetry.CaptureCrashes(telemetry.CrashOptions{Dir: dir, Process: "app"})
	t.Cleanup(cr.Stop)
	for _, r := range crashes {
		data, _ := json.Marshal(r)
		if err := os.WriteFile(filepath.Join(cr.Dir(), r.ID+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tel := telemetry.Open(telemetry.Options{Dir: dir, Version: "1.2.3", Backend: b, Crashes: cr, Usage: true})
	if b == nil {
		tel = telemetry.Open(telemetry.Options{Dir: dir, Version: telemetry.DevVersion, Crashes: cr, Usage: true})
	}
	ta := &telApp{testApp: newTestApp(t, fakeapi.OnePhone), tel: tel, dir: dir}
	ta.app.opt.Telemetry = tel
	ta.app.settings.UsageStats = true
	ta.app.opt.OnSettings = func(s Settings) {
		ta.mu.Lock()
		ta.changes = append(ta.changes, s)
		ta.mu.Unlock()
	}
	ta.startTelemetry()
	ta.sync()
	return ta
}

func (ta *telApp) lastChange() (Settings, bool) {
	ta.mu.Lock()
	defer ta.mu.Unlock()
	if len(ta.changes) == 0 {
		return Settings{}, false
	}
	return ta.changes[len(ta.changes)-1], true
}

// eventually syncs until cond holds.
func (ta *telApp) eventually(t *testing.T, tt *mg.Tester, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 250; i++ {
		ta.sync()
		if tt != nil {
			tt.Frame()
		}
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestUsageNoteOK(t *testing.T) {
	ta := newTelApp(t, &reports{})
	tt := mg.NewTester(ta.view, 1280, 820)
	if !hasText(tt, "Pinky shares anonymous usage statistics") {
		t.Fatalf("no note about usage statistics: %q", tt.Texts())
	}
	if err := tt.Click("OK"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "the note to be answered", ta.tel.NoticeShown)
	if hasText(tt, "Pinky shares anonymous usage statistics") || !ta.settings.UsageStats || !ta.tel.Usage() {
		t.Fatalf("after OK: note %v, setting %v, usage %v", hasText(tt, "shares anonymous"), ta.settings.UsageStats, ta.tel.Usage())
	}
	// It does not come back.
	ta2 := &telApp{testApp: newTestApp(t, fakeapi.OnePhone), tel: ta.tel}
	ta2.app.opt.Telemetry = ta.tel
	ta2.startTelemetry()
	if ta2.t.notice {
		t.Fatal("the note shows again after it was answered")
	}
}

func TestUsageNoteTurnOff(t *testing.T) {
	ta := newTelApp(t, &reports{})
	ta.tel.Count("runs.started")
	tt := mg.NewTester(ta.view, 1280, 820)
	if err := tt.Click("Turn off"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "usage statistics off", func() bool { return !ta.tel.Usage() && ta.tel.NoticeShown() })
	s, ok := ta.lastChange()
	if !ok || s.UsageStats || ta.settings.UsageStats {
		t.Fatalf("settings after Turn off: %+v (changed %v)", s, ok)
	}
	if n := len(ta.tel.Report().Counts); n != 0 {
		t.Fatalf("counts kept: %v", ta.tel.Report().Counts)
	}
}

func TestUsageNoteAsksWhenOff(t *testing.T) {
	ta := newTelApp(t, &reports{})
	ta.settings.UsageStats = false
	ta.tel.SetUsage(false)
	tt := mg.NewTester(ta.view, 1280, 820)
	if !hasText(tt, "Help make Pinky better by sharing anonymous usage statistics") {
		t.Fatalf("no invitation: %q", tt.Texts())
	}
	if err := tt.Click("Share"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "usage statistics on", ta.tel.Usage)
	if !ta.settings.UsageStats {
		t.Fatal("Share left the setting off")
	}
}

func TestWhatsShared(t *testing.T) {
	ta := newTelApp(t, &reports{})
	ta.tel.Count("runs.started")
	tt := mg.NewTester(ta.view, 1280, 820)
	if err := tt.Click("What's shared"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "the usage report", func() bool { return hasText(tt, `"runs.started": 1`) })
	for _, want := range []string{"What Pinky shares", `"install_id"`, "never include what is on your phones' screens"} {
		if !hasText(tt, want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	if hasText(tt, "What Pinky shares") {
		t.Fatal("the dialog stayed open")
	}
}

func TestPrivacySettings(t *testing.T) {
	ta := newTelApp(t, &reports{})
	ta.page, ta.set.sec = pageSettings, secPrivacy
	tt := mg.NewTester(ta.view, 1280, 820)
	for _, want := range []string{"Share anonymous usage statistics", "Send crash reports automatically", "What's shared", "Send feedback…"} {
		if !hasText(tt, want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Share anonymous usage statistics"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "usage statistics off", func() bool { return !ta.tel.Usage() })
	if s, _ := ta.lastChange(); s.UsageStats {
		t.Fatal("the switch did not save")
	}
	if err := tt.Click("Send crash reports automatically"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "crash reports automatic", ta.tel.AutoCrash)
	if s, _ := ta.lastChange(); !s.CrashReports {
		t.Fatal("the crash switch did not save")
	}

	t.Setenv("DO_NOT_TRACK", "1")
	tt.Frame()
	if !hasText(tt, "Off: DO_NOT_TRACK is set on this computer.") {
		t.Fatalf("DO_NOT_TRACK not shown: %q", tt.Texts())
	}
}

func crashReport(id, process string) telemetry.CrashReport {
	return telemetry.CrashReport{ID: id, Process: process, Version: "1.2.2", OS: "macos", OSVersion: "27.2", Arch: "arm64",
		Started: time.Now().Add(-time.Hour), Crashed: time.Now().Add(-time.Minute),
		Reason: "panic: runtime error: index out of range [3] with length 3", Stack: "goroutine 1 [running]:\nmain.main()"}
}

func TestCrashDialogSends(t *testing.T) {
	b := &reports{}
	ta := newTelApp(t, b, crashReport("app-1-2", "app"))
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.eventually(t, tt, "the crash dialog", func() bool { return hasText(tt, "Pinky quit unexpectedly") })
	if err := tt.Click("Show the report"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !hasText(tt, "index out of range [3] with length 3") {
		t.Fatalf("the report does not show: %q", tt.Texts())
	}
	if err := tt.Click("Send Report"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "the report sent", func() bool { c, _ := b.sent(); return len(c) == 1 })
	ta.eventually(t, tt, "the dialog to close", func() bool { return !hasText(tt, "Pinky quit unexpectedly") })
	if len(ta.tel.Crashes()) != 0 {
		t.Fatal("the sent report is still waiting")
	}
	if s, _ := ta.lastChange(); s.CrashReports {
		t.Fatal("Send turned on automatic reports")
	}
}

func TestCrashDialogDontSendAndAlways(t *testing.T) {
	b := &reports{}
	ta := newTelApp(t, b, crashReport("daemon-1-2", "daemon"), crashReport("app-3-4", "app"))
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.eventually(t, tt, "the crash dialog", func() bool { return hasText(tt, "Pinky quit unexpectedly 2 times") })
	if err := tt.Click("Send crash reports automatically from now on"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Don't Send"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "the reports discarded", func() bool { return len(ta.tel.Crashes()) == 0 })
	if c, _ := b.sent(); len(c) != 0 {
		t.Fatalf("sent %d reports after Don't Send", len(c))
	}
	ta.eventually(t, tt, "automatic reports", ta.tel.AutoCrash)
	if s, _ := ta.lastChange(); !s.CrashReports || !ta.settings.CrashReports {
		t.Fatal("the choice to send automatically was not saved")
	}
}

func TestCrashDialogWaitsForAutomaticReports(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	b := &reports{}
	ta := newTelApp(t, b)
	ta.tel.SetAutoCrash(true)
	ta.t.crashes, ta.t.crashOpen = nil, false
	// With automatic reports the dialog does not open; Run sends them.
	dir := filepath.Join(ta.dir, "crashes")
	data, _ := json.Marshal(crashReport("app-5-6", "app"))
	os.WriteFile(filepath.Join(dir, "app-5-6.json"), data, 0o600)
	ta.startTelemetry()
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.sync()
	tt.Frame()
	if ta.t.crashOpen || hasText(tt, "quit unexpectedly") {
		t.Fatal("asked although reports go out automatically")
	}
}

func TestFeedbackSends(t *testing.T) {
	b := &reports{}
	ta := newTelApp(t, b)
	ta.tel.MarkNoticeShown()
	ta.t.notice = false
	ta.page, ta.set.sec = pageSettings, secAdvanced
	tt := mg.NewTester(ta.view, 1280, 820)
	if err := tt.Click("Send feedback…"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "the diagnostics", func() bool { return !ta.t.fb.reading })
	if !hasText(tt, "What happened, or what would make Pinky better?") {
		t.Fatalf("no feedback dialog: %q", tt.Texts())
	}
	send, _ := tt.Find("Send")
	_ = send
	tt.Type("The mirror froze after I unlocked the phone.")
	if err := tt.Click("E-mail (optional)"); err != nil {
		t.Fatal(err)
	}
	tt.Type("ada@example.com")
	if err := tt.Click("Review diagnostics"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !hasText(tt, "Phones (1):") || !hasText(tt, "Pinky 1.2.3") {
		t.Fatalf("diagnostics: %q", tt.Texts())
	}
	for _, d := range ta.devices {
		if strings.Contains(ta.t.fb.diag, d.Info.ID) {
			t.Fatalf("the diagnostics name the phone %q:\n%s", d.Info.ID, ta.t.fb.diag)
		}
	}
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "the feedback sent", func() bool { _, f := b.sent(); return len(f) == 1 })
	_, f := b.sent()
	if f[0].Message != "The mirror froze after I unlocked the phone." || f[0].Email != "ada@example.com" ||
		!strings.Contains(f[0].Diagnostics, "Phones (1):") || f[0].Version != "1.2.3" {
		t.Fatalf("feedback: %+v", f[0])
	}
	ta.eventually(t, tt, "the dialog to close", func() bool { return !ta.t.fb.open })
	if ta.t.fb.message != "" {
		t.Fatal("the sent message stayed as a draft")
	}
}

func TestFeedbackFailureKeepsTheDraft(t *testing.T) {
	b := &reports{}
	b.setErr(errors.New("network down"))
	ta := newTelApp(t, b)
	ta.t.notice = false
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.openFeedback()
	ta.eventually(t, tt, "the diagnostics", func() bool { return !ta.t.fb.reading })
	tt.Type("Please add a dark icon.")
	if err := tt.Click("Attach diagnostics: versions, phone models and Pinky's recent logs"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "the error", func() bool { return hasText(tt, "Couldn't send it: network down") })
	if !ta.t.fb.open || ta.t.fb.message != "Please add a dark icon." {
		t.Fatalf("the draft was lost: %+v", ta.t.fb)
	}
	b.setErr(nil)
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	ta.eventually(t, tt, "the feedback sent", func() bool { _, f := b.sent(); return len(f) == 1 })
	if _, f := b.sent(); f[0].Diagnostics != "" {
		t.Fatal("diagnostics went out although unticked")
	}
}

func TestFeedbackChecksTheAddress(t *testing.T) {
	b := &reports{}
	ta := newTelApp(t, b)
	ta.t.notice = false
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.openFeedback()
	ta.eventually(t, tt, "the diagnostics", func() bool { return !ta.t.fb.reading })
	tt.Type("Hi")
	tt.Click("E-mail (optional)")
	tt.Type("not an address")
	tt.Click("Send")
	tt.Frame()
	if !hasText(tt, "That e-mail address doesn't look right.") {
		t.Fatalf("no address check: %q", tt.Texts())
	}
	if _, f := b.sent(); len(f) != 0 {
		t.Fatal("sent with a bad address")
	}
}

func TestFeedbackWithoutABackend(t *testing.T) {
	ta := newTelApp(t, nil)
	ta.t.notice = false
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.openFeedback()
	ta.eventually(t, tt, "the diagnostics", func() bool { return !ta.t.fb.reading })
	tt.Type("It works!")
	if !hasText(tt, "This build of Pinky has no address to send feedback to.") {
		t.Fatalf("no explanation: %q", tt.Texts())
	}
	if err := tt.Click("Copy feedback"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); !strings.HasPrefix(got, "It works!") || !strings.Contains(got, "Phones (1):") {
		t.Fatalf("clipboard %q", got)
	}
}

func TestMenusOfferFeedback(t *testing.T) {
	ta := newTelApp(t, &reports{})
	help := ta.helpMenu()
	if help == nil || len(help.Submenu) != 1 || help.Submenu[0].Label != "Send Feedback…" {
		t.Fatalf("help menu %+v", help)
	}
	help.Submenu[0].Click(help.Submenu[0], (*mygo.Window)(nil))
	if !ta.t.fb.open {
		t.Fatal("Send Feedback… did not open the dialog")
	}
	plain := newTestApp(t, fakeapi.OnePhone)
	if plain.helpMenu() != nil {
		t.Fatal("a Help menu without telemetry")
	}
	tt := mg.NewTester(plain.view, 1280, 820)
	plain.page = pageSettings
	tt.Frame()
	if hasText(tt, "Send feedback…") {
		t.Fatal("Send feedback without telemetry")
	}
}
