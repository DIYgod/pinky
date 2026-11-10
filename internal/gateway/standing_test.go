package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/policy"
)

// changeAppearance changes the phone's appearance and answers the
// approval it asks with d, when it asks; it reports whether it asked.
func changeAppearance(t *testing.T, g *Gateway, s *Session, to string, d Decision, check func(NeedView)) bool {
	t.Helper()
	done := make(chan *Result, 1)
	go func() { done <- s.Device(context.Background(), DeviceRequest{Appearance: to}) }()
	stop := make(chan struct{})
	defer close(stop)
	asked := false
	select {
	case res := <-done:
		mustStatus(t, res, StatusSettled)
		return false
	case n := <-needCh(g, stop):
		asked = true
		if check != nil {
			check(n)
		}
		if err := g.Decide(n.ID, d, ""); err != nil {
			t.Fatal(err)
		}
	}
	res := <-done
	if d == Deny {
		mustStatus(t, res, StatusDenied)
	} else {
		mustStatus(t, res, StatusSettled)
	}
	return asked
}

// needCh delivers the first approval card that shows, until stop closes.
func needCh(g *Gateway, stop <-chan struct{}) <-chan NeedView {
	ch := make(chan NeedView, 1)
	go func() {
		for range 1500 {
			for _, n := range g.Needs() {
				if n.Kind == NeedApproval {
					ch <- n
					return
				}
			}
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	return ch
}

// Always allow on changing the phone's settings: the agent no longer asks
// on that phone, in later tasks and after a restart, while another agent
// still asks; the settings still go back when each task ends; asking
// every action, and taking it back in Settings, make it ask again.
func TestAlwaysAllowPhoneSettings(t *testing.T) {
	dir := t.TempDir()
	x := newExtraDev("pixel", "Pixel 9")
	open := func() *Gateway {
		pol := loadPolicy(t, dir)
		if err := pol.SetPreset(policy.PresetPayments); err != nil {
			t.Fatal(err)
		}
		return newExtraGateway(t, x, func(o *Options) { o.Policy = pol })
	}
	g := open()
	s, _ := connect(t, g, "Test dark mode")
	if !changeAppearance(t, g, s, "dark", AllowAlways, func(n NeedView) {
		if n.Rule != settingsRule || !n.AlwaysAllow || !n.AllowTask || n.Agent != agent.Name || n.DeviceName != "Pixel 9" {
			t.Errorf("approval %+v", n)
		}
	}) {
		t.Fatal("the first change did not ask")
	}
	got := g.Exemptions()
	if len(got) != 1 || got[0].Rule != settingsRule || got[0].Agent != agent.Name || got[0].DeviceID != "pixel" ||
		got[0].DeviceName != "Pixel 9" || got[0].AppID != "" || len(got[0].With) != 0 {
		t.Fatalf("exemptions %+v", got)
	}
	release(t, g, s)
	if v := x.SwitchValue(driver.SwitchAppearance); v != driver.AppearanceLight {
		t.Fatalf("appearance %q after the task", v)
	}

	// The next task, after a restart: no question, and still put back.
	g.Close()
	g = open()
	s, _ = connect(t, g, "Test dark mode again")
	if changeAppearance(t, g, s, "dark", AllowOnce, nil) {
		t.Fatal("asked again after Always allow")
	}
	if v := x.SwitchValue(driver.SwitchAppearance); v != "dark" {
		t.Fatalf("appearance %q", v)
	}
	release(t, g, s)
	if v := x.SwitchValue(driver.SwitchAppearance); v != driver.AppearanceLight {
		t.Fatalf("appearance %q after the second task", v)
	}

	// Another agent on the same phone asks.
	other := g.OpenSession(AgentInfo{Name: "Codex", Client: "codex 1.0"})
	t.Cleanup(other.Close)
	mustStatus(t, other.Connect(context.Background(), ConnectRequest{Task: "Dark mode"}), StatusSettled)
	if !changeAppearance(t, g, other, "dark", Deny, nil) {
		t.Fatal("another agent did not ask")
	}
	release(t, g, other)

	// While every action asks, it asks, and offers no Always allow.
	if err := g.Policy().SetPreset(policy.PresetEvery); err != nil {
		t.Fatal(err)
	}
	s, _ = connect(t, g, "Every action")
	if !changeAppearance(t, g, s, "dark", Deny, func(n NeedView) {
		if n.AlwaysAllow {
			t.Errorf("Always allow offered while every action asks: %+v", n)
		}
	}) {
		t.Fatal("did not ask while every action asks")
	}
	release(t, g, s)
	if err := g.Policy().SetPreset(policy.PresetPayments); err != nil {
		t.Fatal(err)
	}

	// Taken back in Settings: it asks again.
	if err := g.RemoveExemption(g.Exemptions()[0]); err != nil {
		t.Fatal(err)
	}
	if len(g.Exemptions()) != 0 {
		t.Fatalf("exemptions %+v", g.Exemptions())
	}
	s, _ = connect(t, g, "Taken back")
	if !changeAppearance(t, g, s, "dark", Deny, nil) {
		t.Fatal("did not ask after taking it back")
	}
}

// Always allow on reading an app's logs holds for that agent, phone and
// app: another app's logs still ask, and removing the phone drops it.
func TestAlwaysAllowAppLogs(t *testing.T) {
	ctx := context.Background()
	g, _ := newLogsGateway(t)
	s, _ := connect(t, g, "Test Crash Test")
	answer(t, g, AllowAlways, func(n NeedView) {
		if n.Rule != policy.RuleAppLogs || !n.AlwaysAllow || n.App != "Crash Test" {
			t.Errorf("approval %+v", n)
		}
	})
	mustStatus(t, s.Logs(ctx, LogsRequest{App: "Crash Test"}), StatusSettled)
	got := g.Exemptions()
	if len(got) != 1 || got[0].Rule != policy.RuleAppLogs || got[0].AppID != crashTest || got[0].AppName != "Crash Test" ||
		got[0].Agent != agent.Name || got[0].DeviceID != "pixel" {
		t.Fatalf("exemptions %+v", got)
	}
	mustStatus(t, s.Release(ctx, ReleaseRequest{}), StatusSettled)

	// The next task reads Crash Test's logs without asking.
	s, _ = connect(t, g, "Test again")
	mustStatus(t, s.Logs(ctx, LogsRequest{App: crashTest, Kind: "crashes"}), StatusSettled)
	if len(g.Needs()) != 0 {
		t.Fatalf("asked: %+v", g.Needs())
	}
	// Another app asks.
	answer(t, g, Deny, func(n NeedView) {
		if n.App != "Notes Two" {
			t.Errorf("approval %+v", n)
		}
	})
	mustStatus(t, s.Logs(ctx, LogsRequest{App: "Notes Two"}), StatusDenied)
	mustStatus(t, s.Release(ctx, ReleaseRequest{}), StatusSettled)

	// Removing the phone drops its answers.
	if err := g.Policy().ForgetDevice("pixel"); err != nil {
		t.Fatal(err)
	}
	if len(g.Exemptions()) != 0 {
		t.Fatalf("exemptions after forgetting the phone %+v", g.Exemptions())
	}
}
