package fakeapi

import (
	"context"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/screen"
)

// waitFor polls the fake until cond holds, for up to ten seconds of real
// time; the scripts run at 50 times their pace.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func approval(a *API) (gateway.NeedView, bool) {
	for _, n := range a.Needs() {
		if n.Kind == gateway.NeedApproval {
			return n, true
		}
	}
	return gateway.NeedView{}, false
}

func TestLunchAllowed(t *testing.T) {
	a := New(Options{Scenario: OnePhone, Speed: 50})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := a.Subscribe(ctx)

	var n gateway.NeedView
	waitFor(t, "the payment approval", func() (ok bool) { n, ok = approval(a); return })
	if n.Title != "Pay $12.80?" || n.Rule != "Payments" || n.Screenshot == nil || n.Target.Empty() {
		t.Fatalf("approval = %+v", n)
	}
	if d := a.Devices()[0]; d.Status != gateway.DeviceNeedsYou {
		t.Errorf("device status %q while asking, want needs_you", d.Status)
	}
	if err := a.Decide(n.ID, gateway.AllowOnce, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task to finish", func() bool {
		ts := a.Tasks()
		return len(ts) == 1 && ts[0].State == gateway.TaskDone
	})
	steps := a.Tasks()[0].Steps
	if got := steps[len(steps)-1].Text; got != "Delivery in 25–35 min" {
		t.Errorf("last step %q", got)
	}
	if d := a.Devices()[0]; d.Status != gateway.DeviceIdle {
		t.Errorf("device status %q after the task, want idle", d.Status)
	}
	hist, _ := a.History(ctx, gateway.HistoryQuery{})
	if len(hist) != 6 || hist[0].Title != "Reorder last week's lunch" {
		t.Errorf("history has %d tasks, newest %q", len(hist), hist[0].Title)
	}
	select {
	case <-events:
	default:
		t.Error("no events")
	}
}

func TestTouchTakesOver(t *testing.T) {
	a := New(Options{Scenario: OnePhone, Speed: 50})
	waitFor(t, "the payment approval", func() bool { _, ok := approval(a); return ok })
	if err := a.Touch("pixel-9", driver.TouchDown, screen.Point{X: 500, Y: 1000}); err != nil {
		t.Fatal(err)
	}
	if _, ok := approval(a); ok {
		t.Error("the approval stayed open after the person took over")
	}
	d := a.Devices()[0]
	if d.Status != gateway.DeviceInControl {
		t.Fatalf("device status %q, want in_control", d.Status)
	}
	if st := a.Tasks()[0].State; st != gateway.TaskPaused {
		t.Errorf("task state %q, want paused", st)
	}
	if err := a.HandBack("pixel-9", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent to ask again", func() bool { _, ok := approval(a); return ok })
	if err := a.StopAll(); err != nil {
		t.Fatal(err)
	}
	if st := a.Tasks()[0].State; st != gateway.TaskStopped {
		t.Errorf("task state %q after Stop all, want stopped", st)
	}
	if len(a.Needs()) != 0 {
		t.Error("needs left after Stop all")
	}
}

func TestLockedUnlock(t *testing.T) {
	a := New(Options{Scenario: Locked, Speed: 50})
	waitFor(t, "the unlock card", func() bool {
		ns := a.Needs()
		return len(ns) == 1 && ns[0].Kind == gateway.NeedUnlock
	})
	if d := a.Devices()[0]; d.Status != gateway.DeviceLocked {
		t.Errorf("device status %q, want locked", d.Status)
	}
	if err := a.Unlock("pixel-9"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the send approval", func() bool {
		n, ok := approval(a)
		return ok && n.Title == "Send a message to Sam?"
	})
}

func TestThreePhones(t *testing.T) {
	a := New(Options{Scenario: ThreePhones, Speed: 50})
	ds := a.Devices()
	if len(ds) != 3 || ds[2].Status != gateway.DeviceOffline {
		t.Fatalf("devices %+v", ds)
	}
	waitFor(t, "two approvals", func() bool { return len(a.Needs()) == 2 })
	a.Play(OnePhone)
	if len(a.Devices()) != 1 || len(a.Needs()) != 0 {
		t.Errorf("Play did not reset: %d devices, %d needs", len(a.Devices()), len(a.Needs()))
	}
}
