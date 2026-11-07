package ui

import (
	"context"
	"slices"
	"strings"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/ui/demogw"
)

// TestGatewayAlwaysAllow answers the lunch payment with More options ›
// Always allow payments in Dinely: Settings › Safety lists it, the next
// lunch is paid without asking, and the list's × makes payments ask again.
func TestGatewayAlwaysAllow(t *testing.T) {
	ga := newGatewayApp(t, true)
	tt := mg.NewTester(ga.view, 1280, 820)
	done := ga.agent(t, "Claude Code", pixel, (*demogw.Agent).ReorderLunch)

	var pay gateway.NeedView
	ga.waitFor(t, "the payment approval", func() (ok bool) { pay, ok = ga.need(isPayment); return })
	if !pay.AlwaysAllow || pay.Rule != "Payments" || pay.App != "Dinely" {
		t.Fatalf("payment approval %+v", pay)
	}
	tt.Frame()
	if err := tt.Click("More options"); err != nil {
		t.Fatal(err)
	}
	if texts := tt.Texts(); !slices.Contains(texts, "Allow for this task") || !slices.Contains(texts, "Always allow payments in Dinely") {
		t.Fatalf("More options: %q", texts)
	}
	if err := tt.Click("Always allow payments in Dinely"); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, done, "the first lunch"); err != nil {
		t.Fatalf("agent: %v", err)
	}
	if got := ga.w.Gateway.Exemptions(); len(got) != 1 || got[0].Rule != "Payments" || got[0].AppName != "Dinely" {
		t.Fatalf("exemptions %+v", got)
	}

	// Settings › Safety lists it.
	ga.page, ga.set.sec = pageSettings, secSafety
	ga.waitFor(t, "Always allowed", func() bool { tt.Frame(); return ga.always.loaded && len(ga.always.list) == 1 })
	tt.Frame()
	for _, want := range []string{"Always allowed", "Dinely", "Payments"} {
		if !hasText(tt, want) {
			t.Errorf("no %q in Settings › Safety: %q", want, tt.Texts())
		}
	}
	if _, ok := tt.Find("Ask again about payments in Dinely"); !ok {
		t.Fatalf("no button to take it back: %q", tt.Texts())
	}

	// The next lunch is paid without asking. (The agent goes on from where
	// the phone is: on the screen of the order placed, it would just stop.)
	ga.w.Pixel.Show("home")
	done = ga.agent(t, "Claude Code", pixel, (*demogw.Agent).ReorderLunch)
	ga.waitFor(t, "the second lunch", func() bool {
		if n, ok := ga.need(isPayment); ok {
			t.Fatalf("the second payment asked: %+v", n)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("agent: %v", err)
			}
			return true
		default:
			return false
		}
	})
	if !paid(ga.w.Gateway.Tasks()) {
		t.Fatalf("the second lunch was not paid: %+v", ga.w.Gateway.Tasks())
	}

	// Taken back, payments in Dinely ask again.
	tt.Frame()
	if err := tt.Click("Ask again about payments in Dinely"); err != nil {
		t.Fatal(err)
	}
	ga.waitFor(t, "the answer taken back", func() bool { return len(ga.w.Gateway.Exemptions()) == 0 })
	ga.waitFor(t, "the list to empty", func() bool { tt.Frame(); return len(ga.always.list) == 0 })
	tt.Frame()
	if !hasText(tt, "None yet.") || hasText(tt, "Ask again about") {
		t.Fatalf("Settings › Safety after taking it back: %q", tt.Texts())
	}
	ga.w.Pixel.Show("home")
	done = ga.agent(t, "Claude Code", pixel, (*demogw.Agent).ReorderLunch)
	ga.waitFor(t, "the third payment to ask", func() (ok bool) { pay, ok = ga.need(isPayment); return })
	ga.w.Gateway.Decide(pay.ID, gateway.AllowOnce, "")
	if err := wait(t, done, "the third lunch"); err != nil {
		t.Fatalf("agent: %v", err)
	}
}

// paid reports whether the last task tapped Place order, and it went
// through without waiting for the person.
func paid(tasks []gateway.TaskView) bool {
	if len(tasks) == 0 {
		return false
	}
	for _, st := range tasks[len(tasks)-1].Steps {
		if st.Text == "Tapped “Place order · $12.80”" {
			return st.Status == "settled"
		}
		if strings.HasPrefix(st.Text, "Waiting for you") {
			return false
		}
	}
	return false
}

// TestGatewayAlwaysAllowNotOffered: while every action asks, More options
// has no Always allow, and Settings says the answers wait.
func TestGatewayAlwaysAllowNotOffered(t *testing.T) {
	ga := newGatewayApp(t, true)
	g := ga.w.Gateway
	// An answer from before, in another app.
	if _, err := g.Policy().Exempt([]string{"Messages"}, screen.App{Name: "Chatter", ID: demo.Chatter}); err != nil {
		t.Fatal(err)
	}
	if err := g.Policy().SetPreset(policy.PresetEvery); err != nil {
		t.Fatal(err)
	}
	ga.settings.Preset = presetEvery
	tt := mg.NewTester(ga.view, 1280, 820)
	done := ga.agent(t, "Claude Code", pixel, func(ag *demogw.Agent, ctx context.Context) error {
		if err := ag.Connect(ctx, "Open Dinely"); err != nil {
			return err
		}
		return ag.Act(ctx, "Open Dinely", gateway.Action{Tool: "launch", App: "Dinely"})
	})
	var n gateway.NeedView
	ga.waitFor(t, "the launch approval", func() (ok bool) { n, ok = ga.need(isPayment); return })
	if n.AlwaysAllow || n.Rule != policy.RuleEvery {
		t.Fatalf("approval %+v", n)
	}
	tt.Frame()
	if err := tt.Click("More options"); err != nil {
		t.Fatal(err)
	}
	texts := tt.Texts()
	if !slices.Contains(texts, "Allow for this task") {
		t.Fatalf("More options: %q", texts)
	}
	for _, s := range texts {
		if strings.HasPrefix(s, "Always allow") {
			t.Fatalf("More options offers %q while every action asks", s)
		}
	}
	if err := tt.Click("Allow for this task"); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, done, "the agent"); err != nil {
		t.Fatalf("agent: %v", err)
	}

	ga.page, ga.set.sec = pageSettings, secSafety
	ga.waitFor(t, "Always allowed", func() bool { tt.Frame(); return ga.always.loaded })
	tt.Frame()
	for _, want := range []string{"Chatter", "Messages", "While agents ask before every action, these ask too."} {
		if !hasText(tt, want) {
			t.Errorf("no %q in Settings › Safety: %q", want, tt.Texts())
		}
	}
}
