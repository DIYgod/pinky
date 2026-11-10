package policy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

// An Always allow on changing a phone's settings or reading an app's logs
// holds for one agent on one phone (and one app for logs), is saved with
// the others, never lifts a rule's asks, and goes with the phone.
func TestStandingAnswers(t *testing.T) {
	dir := t.TempDir()
	rules, apps := filepath.Join(dir, "rules.yaml"), filepath.Join(dir, "apps.yaml")
	e, err := Load(rules, apps)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetPreset(PresetPayments); err != nil {
		t.Fatal(err)
	}
	app := screen.App{ID: "dev.pinky.crashtest", Name: "Crash Test"}
	if !e.CanKeepStanding(RulePhoneSettings, "Claude Code", "pixel", screen.App{}) ||
		e.CanKeepStanding("Payments", "Claude Code", "pixel", app) || e.CanKeepStanding(RulePhoneSettings, "", "pixel", screen.App{}) ||
		e.CanKeepStanding(RuleAppLogs, "Claude Code", "pixel", screen.App{}) {
		t.Fatal("CanKeepStanding")
	}
	if _, err := e.KeepStanding("Payments", "Claude Code", "pixel", "Pixel 9", app); err != ErrNotExemptable {
		t.Fatalf("a rule's answer kept per phone: %v", err)
	}
	if _, err := e.KeepStanding(RulePhoneSettings, "Claude Code", "pixel", "Pixel 9", screen.App{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.KeepStanding(RuleAppLogs, "Claude Code", "pixel", "Pixel 9", app); err != nil {
		t.Fatal(err)
	}
	// Keeping it again changes nothing.
	if _, err := e.KeepStanding(RuleAppLogs, "Claude Code", "pixel", "Pixel 9", app); err != nil || len(e.Exemptions()) != 2 {
		t.Fatalf("again: %v %+v", err, e.Exemptions())
	}

	check := func(e *Engine, when string) {
		t.Helper()
		for _, c := range []struct {
			rule, agent, device string
			app                 screen.App
			want                bool
		}{
			{RulePhoneSettings, "Claude Code", "pixel", screen.App{}, true},
			{RulePhoneSettings, "Codex", "pixel", screen.App{}, false},
			{RulePhoneSettings, "Claude Code", "iphone", screen.App{}, false},
			{RuleAppLogs, "Claude Code", "pixel", app, true},
			{RuleAppLogs, "Claude Code", "pixel", screen.App{Name: "crash test"}, true},
			{RuleAppLogs, "Claude Code", "pixel", screen.App{ID: "com.example.notes", Name: "Notes"}, false},
			{RuleAppLogs, "Codex", "pixel", app, false},
		} {
			if got := e.Standing(c.rule, c.agent, c.device, c.app); got != c.want {
				t.Errorf("%s: Standing(%s, %s, %s, %+v) = %v", when, c.rule, c.agent, c.device, c.app, got)
			}
		}
	}
	check(e, "kept")

	// Saved, and read back.
	data, err := os.ReadFile(rules)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rule: Phone settings", "agent: Claude Code", "device: pixel", "device_name: Pixel 9", "app: dev.pinky.crashtest"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("rules.yaml lacks %q:\n%s", want, data)
		}
	}
	e, err = Load(rules, apps)
	if err != nil {
		t.Fatal(err)
	}
	check(e, "loaded")

	// Not while every action asks.
	if err := e.SetPreset(PresetEvery); err != nil {
		t.Fatal(err)
	}
	if e.Standing(RulePhoneSettings, "Claude Code", "pixel", screen.App{}) || e.CanKeepStanding(RulePhoneSettings, "Claude Code", "pixel", screen.App{}) {
		t.Fatal("applies while every action asks")
	}
	if err := e.SetPreset(PresetPayments); err != nil {
		t.Fatal(err)
	}

	// A rule of the person's own, even one with the same name, is not
	// lifted by them, and changing the rules keeps them.
	if err := e.SetFileRules(e.FileRules(), []Rule{{Name: RuleAppLogs, Decision: Ask, Match: Match{App: StringList{"Crash Test"}, Action: StringList{"tap"}}}}); err != nil {
		t.Fatal(err)
	}
	if v := e.Evaluate(Input{Op: OpTap, App: app, Target: &screen.Element{Label: "Crash"}}); v.Decision != Ask || len(v.Exempted) != 0 {
		t.Fatalf("verdict %+v", v)
	}
	if err := e.SetFileRules(e.FileRules(), nil); err != nil {
		t.Fatal(err)
	}
	if len(e.Exemptions()) != 2 {
		t.Fatalf("changing the rules dropped them: %+v", e.Exemptions())
	}

	// Taken back one by one: only its agent, phone and app.
	xs := e.Exemptions()
	if err := e.Unexempt(Exemption{Rule: RuleAppLogs, App: xs[1].App, AppName: xs[1].AppName, Agent: "Codex", Device: "pixel"}); err != nil {
		t.Fatal(err)
	}
	if len(e.Exemptions()) != 2 {
		t.Fatal("took back another agent's answer")
	}
	if err := e.Unexempt(xs[1]); err != nil {
		t.Fatal(err)
	}
	if e.Standing(RuleAppLogs, "Claude Code", "pixel", app) || !e.Standing(RulePhoneSettings, "Claude Code", "pixel", screen.App{}) {
		t.Fatal("after taking back the logs answer")
	}

	// Removing the phone drops the rest.
	if err := e.ForgetDevice("iphone"); err != nil || len(e.Exemptions()) != 1 {
		t.Fatalf("another phone: %v %+v", err, e.Exemptions())
	}
	if err := e.ForgetDevice("pixel"); err != nil || len(e.Exemptions()) != 0 {
		t.Fatalf("forget: %v %+v", err, e.Exemptions())
	}
	if e, err = Load(rules, apps); err != nil || len(e.Exemptions()) != 0 {
		t.Fatalf("after forgetting, loaded %v %+v", err, e.Exemptions())
	}
}

func TestStandingInTheRulesFile(t *testing.T) {
	bad := map[string]string{
		"no agent":      "always_allow:\n  - rule: Phone settings\n    device: pixel\n",
		"agent only":    "always_allow:\n  - rule: Phone settings\n    agent: Claude Code\n    app: x\n",
		"rule's answer": "always_allow:\n  - rule: Payments\n    agent: Claude Code\n    device: pixel\n    app: com.dinely.app\n",
		"logs, no app":  "always_allow:\n  - rule: App logs\n    agent: Claude Code\n    device: pixel\n",
		"with":          "always_allow:\n  - rule: Phone settings\n    with: [Commits]\n    agent: Claude Code\n    device: pixel\n",
	}
	for name, src := range bad {
		if _, err := ParseFile([]byte(src)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	f, err := ParseFile([]byte("always_allow:\n  - rule: Phone settings\n    agent: ' Claude Code '\n    device: pixel\n    device_name: Pixel 9\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Exemption{{Rule: RulePhoneSettings, Agent: "Claude Code", Device: "pixel", DeviceName: "Pixel 9"}}
	if !reflect.DeepEqual(f.AlwaysAllow, want) {
		t.Fatalf("parsed %+v, want %+v", f.AlwaysAllow, want)
	}
}
