package android

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// ignoringRunner takes settings changes without an error and keeps nothing
// of them, as vivo's OriginOS 6 does with the accessibility setting
// (GitHub issue #94).
type ignoringRunner struct {
	*quietPhone
	tries atomic.Int32 // settings put and delete it ignored
}

func (r *ignoringRunner) Shell(ctx context.Context, cmd string) (string, error) {
	if strings.HasPrefix(cmd, "settings put") || strings.HasPrefix(cmd, "settings delete") {
		r.tries.Add(1)
		return "", nil
	}
	return r.quietPhone.Shell(ctx, cmd)
}

// fast fails the test when what took longer than a moment: the service's
// own wait is 10 s.
func fast(t *testing.T, what string, start time.Time) {
	t.Helper()
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("%s took %v: it waited for a service that cannot come on", what, took)
	}
}

// TestCompanionManual: on a phone that ignores Pinky's change to its
// accessibility settings, the driver says so at once and remembers it for
// the phone: its state, its checkup item, Turn on and the agent's
// background display all say that only the person can turn the service
// on, until a change of Pinky's is kept.
func TestCompanionManual(t *testing.T) {
	p := newQuietPhone(t)
	d, logs := quietDevice(t, p, "")
	r := &ignoringRunner{quietPhone: p}
	d.compRun = r
	ctx := context.Background()
	if st, _ := d.CompanionState(ctx); st != driver.CompanionReady {
		t.Fatalf("state before any try = %q, want ready", st)
	}

	// A task starts: the service does not come on, without the 10 s wait.
	start := time.Now()
	d.AgentWorking(true)
	if c := d.companionClient(ctx); c != nil {
		t.Fatal("used a companion whose service could not come on")
	}
	fast(t, "the task's first read", start)
	waitFor(t, "the warm-up's try", func() bool { return d.companionManual() })
	if r.tries.Load() == 0 {
		t.Fatal("the driver did not try to turn the service on")
	}
	if st, err := d.CompanionState(ctx); err != nil || st != driver.CompanionManual {
		t.Errorf("state = %q, %v; want manual", st, err)
	}
	ck := d.checkCompanion(ctx)
	if ck.Status != driver.CheckFail || ck.Fix != driver.FixTurnOnCompanionByHand || !strings.Contains(ck.Detail, "did not keep Pinky's entry") {
		t.Errorf("checkup item = %+v", ck)
	}

	// The agent's background display says why it cannot be read, in words
	// the agent can pass on.
	b := &Background{d: d, display: 2, closed: make(chan struct{})}
	start = time.Now()
	_, err := b.Observe(ctx, driver.ObserveOptions{})
	fast(t, "reading the background display", start)
	if !errors.Is(err, driver.ErrUnsupported) || !strings.Contains(err.Error(), "does not let Pinky turn on") ||
		!strings.Contains(err.Error(), "accessibility settings by hand") {
		t.Errorf("background display: %v", err)
	}

	// Turn on (SetUpCompanion) says the same.
	start = time.Now()
	_, err = d.SetUpCompanion(ctx, false)
	fast(t, "Turn on", start)
	var se *driver.CompanionStartError
	if !errors.As(err, &se) || se.Reason != driver.CompanionStartSettingIgnored {
		t.Errorf("SetUpCompanion = %v, want the change the phone did not keep", err)
	}
	if n := countLogs(logs(), "did not keep Pinky's change"); n != 1 {
		t.Errorf("logged the ignored change %d times, want once: %q", n, logs())
	}

	// The person turns the service on in the phone's settings: Pinky uses
	// it, and the phone stays one where only the person can.
	p.mu.Lock()
	p.setting, p.unset = p.app.Component(), false
	p.mu.Unlock()
	if st, _ := d.CompanionState(ctx); st != driver.CompanionOn {
		t.Errorf("state with the service on by hand = %q, want on", st)
	}
	if c := d.companionClientFor(ctx, forBackground); c == nil {
		t.Fatal("did not use the service the person turned on")
	}
	d.companionOff("the task ended")
	// It goes off again (the app's own backstop turns it off).
	p.mu.Lock()
	p.setting, p.unset = "", true
	p.mu.Unlock()
	p.dropAll()
	if st, _ := d.CompanionState(ctx); st != driver.CompanionManual {
		t.Errorf("state once it is off again = %q, want manual", st)
	}

	// Another reason keeps it off: the state says that one.
	d.comp.mu.Lock()
	d.comp.failed = &driver.CompanionStartError{Reason: driver.CompanionStartUIAutomation, Detail: "x"}
	d.comp.mu.Unlock()
	if st, _ := d.CompanionState(ctx); st != driver.CompanionOff {
		t.Errorf("state after another failure = %q, want off", st)
	}

	// The phone keeps Pinky's change again: Turn on works, and the phone is
	// one like any other.
	d.compRun = p
	d.comp.client = nil
	d.AgentWorking(false)
	if _, err := d.SetUpCompanion(ctx, false); err != nil {
		t.Fatalf("SetUpCompanion once the phone keeps the change: %v", err)
	}
	if d.companionManual() {
		t.Error("still manual after a change the phone kept")
	}
	waitFor(t, "the service is off after Turn on", func() bool { return !d.companionInUse() && !p.listed() })
	if st, _ := d.CompanionState(ctx); st != driver.CompanionReady {
		t.Errorf("state = %q, want ready", st)
	}
}

func countLogs(logs []string, s string) int {
	n := 0
	for _, l := range logs {
		if strings.Contains(l, s) {
			n++
		}
	}
	return n
}
