package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/internal/secret"
)

// Unlock without asking (#231, unlockwindow.go).

// fakePIN is the PIN of the phones here: plainly not a real one, and text
// no log line has by chance.
const fakePIN = "fake-4711"

// windowGateway runs a gateway on an extraDev that takes fakePIN, whose
// unlock windows hand out pin.
func windowGateway(t *testing.T, pin string, opts ...func(*Options)) (*Gateway, *extraDev) {
	t.Helper()
	x := newExtraDev("pixel", "Pixel 9")
	x.pin = fakePIN
	opts = append([]func(*Options){func(o *Options) {
		o.UnlockWindowPIN = func(context.Context, string, time.Duration) (string, error) { return pin, nil }
	}}, opts...)
	g := newExtraGateway(t, x, opts...)
	waitUntil(t, "the phone", func() bool { d := g.Devices(); return len(d) == 1 && d[0].Status != DeviceOffline })
	return g, x
}

// enteredPINs returns the PINs entered on x so far.
func (x *extraDev) enteredPINs() []string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return append([]string(nil), x.entered...)
}

// lockPhone locks the phone and waits for the gateway to see it.
func lockPhone(t *testing.T, g *Gateway, x *extraDev) {
	t.Helper()
	x.SetLocked(true)
	waitUntil(t, "the lock poll", func() bool { return gatewaySeesLocked(g, "pixel") })
}

func gatewaySeesLocked(g *Gateway, id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.devices[id]
	return d != nil && d.locked
}

// windowPIN returns the PIN bytes of the phone's open window, nil without
// one: the same bytes the gateway zeroes when the window ends.
func windowPIN(g *Gateway, id string) []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	if w := g.windows[id]; w != nil {
		return w.pin
	}
	return nil
}

func zeroed(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return len(b) > 0
}

func unlockUntil(g *Gateway) time.Time { return g.Devices()[0].UnlockUntil }

// windowSteps returns the steps of the session's task about unlocking
// without asking.
func windowSteps(g *Gateway, s *Session) []StepView {
	var out []StepView
	for _, st := range taskSteps(g, s) {
		if strings.Contains(st.Text, "without asking") {
			out = append(out, st)
		}
	}
	return out
}

func hasNeed(g *Gateway, kind NeedKind) bool {
	for _, n := range g.Needs() {
		if n.Kind == kind {
			return true
		}
	}
	return false
}

// mustUnlockedOnce checks that the window entered its PIN once, and that
// the task has the step for it.
func mustUnlockedOnce(t *testing.T, g *Gateway, x *extraDev, s *Session) {
	t.Helper()
	if got := x.enteredPINs(); len(got) != 1 || got[0] != fakePIN {
		t.Fatalf("PINs entered: %d, want the window's once", len(got))
	}
	steps := windowSteps(g, s)
	if len(steps) != 1 || !strings.HasPrefix(steps[0].Text, "Unlocked without asking (until ") || steps[0].Status != string(StatusSettled) {
		t.Fatalf("steps %+v", steps)
	}
	if want := unlockUntil(g).Local().Format("15:04"); !strings.Contains(steps[0].Text, want) {
		t.Fatalf("step %q does not say until %s", steps[0].Text, want)
	}
	if hasNeed(g, NeedUnlock) {
		t.Fatalf("an unlock card: %+v", g.Needs())
	}
	if gatewaySeesLocked(g, "pixel") {
		t.Fatal("the gateway still sees the phone locked")
	}
}

func TestUnlockWindowOpenAndEnd(t *testing.T) {
	var mu sync.Mutex
	var asked []time.Duration
	confirm := func() error { return nil }
	g, _ := windowGateway(t, fakePIN, func(o *Options) {
		o.UnlockWindowPIN = func(_ context.Context, id string, length time.Duration) (string, error) {
			mu.Lock()
			asked = append(asked, length)
			mu.Unlock()
			if err := confirm(); err != nil {
				return "", err
			}
			return fakePIN, nil
		}
	})
	if err := g.OpenUnlockWindow("pixel", 2*time.Hour); err == nil || len(asked) != 0 {
		t.Fatalf("2 hours: %v, asked %v", err, asked)
	}
	if err := g.OpenUnlockWindow("nope", time.Hour); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("unknown phone: %v", err)
	}
	if !unlockUntil(g).IsZero() || g.Devices()[0].UnlockRefused {
		t.Fatalf("off by default: %+v", g.Devices()[0])
	}

	if err := g.OpenUnlockWindow("pixel", time.Hour); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(unlockUntil(g)); left < 59*time.Minute || left > time.Hour {
		t.Fatalf("1 hour: %s left", left)
	}
	first := windowPIN(g, "pixel")
	if string(first) != fakePIN {
		t.Fatal("the window does not hold the PIN")
	}
	// Opening again replaces the window, and zeroes the PIN it held.
	if err := g.OpenUnlockWindow("pixel", 8*time.Hour); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(unlockUntil(g)); left < 8*time.Hour-time.Minute || left > 8*time.Hour {
		t.Fatalf("8 hours: %s left", left)
	}
	if !zeroed(first) {
		t.Fatal("the replaced window's PIN was not zeroed")
	}
	second := windowPIN(g, "pixel")
	if err := g.EndUnlockWindow("pixel"); err != nil {
		t.Fatal(err)
	}
	if !zeroed(second) || windowPIN(g, "pixel") != nil || !unlockUntil(g).IsZero() {
		t.Fatal("End left the window or its PIN")
	}
	if err := g.EndUnlockWindow("pixel"); err != nil {
		t.Fatalf("ending no window: %v", err)
	}
	if len(asked) != 2 || asked[0] != time.Hour || asked[1] != 8*time.Hour {
		t.Fatalf("asked for %v", asked)
	}

	// Not confirmed: no window.
	confirm = func() error { return secret.ErrCancelled }
	if err := g.OpenUnlockWindow("pixel", 4*time.Hour); !errors.Is(err, secret.ErrCancelled) || !unlockUntil(g).IsZero() {
		t.Fatalf("cancelled: %v", err)
	}
	// Ended, or Settings › Unlock from this computer turned off, while the
	// person confirmed: no window either.
	for name, end := range map[string]func(){
		"End":  func() { g.EndUnlockWindow("pixel") },
		"Ends": g.EndUnlockWindows,
	} {
		confirm = func() error { end(); return nil }
		if err := g.OpenUnlockWindow("pixel", 4*time.Hour); !errors.Is(err, ErrUnlockWindowEnded) || !unlockUntil(g).IsZero() {
			t.Fatalf("%s while confirming: %v", name, err)
		}
	}
	confirm = func() error { return nil }
	if err := g.OpenUnlockWindow("pixel", 4*time.Hour); err != nil {
		t.Fatal(err)
	}
	pin := windowPIN(g, "pixel")
	g.EndUnlockWindows()
	if !zeroed(pin) || !unlockUntil(g).IsZero() {
		t.Fatal("EndUnlockWindows left the window")
	}
}

// noUnlockProvider serves a phone Pinky cannot enter a PIN on.
type noUnlockProvider struct{ *demo.Provider }

type noUnlockDev struct{ driver.Device }

func (p noUnlockProvider) Open(ctx context.Context, id string) (driver.Device, error) {
	dev, err := p.Provider.Open(ctx, id)
	return noUnlockDev{dev}, err
}

func TestUnlockWindowNeedsAPINSource(t *testing.T) {
	asked := false
	hook := func(context.Context, string, time.Duration) (string, error) { asked = true; return fakePIN, nil }
	x := newExtraDev("pixel", "Pixel 9")
	g := newExtraGateway(t, x)
	waitUntil(t, "the phone", func() bool { return len(g.Devices()) == 1 && g.Devices()[0].Status != DeviceOffline })
	if err := g.OpenUnlockWindow("pixel", time.Hour); !errors.Is(err, ErrNoUnlock) {
		t.Fatalf("no PIN source: %v", err)
	}
	// A phone Pinky cannot enter a PIN on: nobody is asked.
	dev := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	g = newExtraGateway(t, x, func(o *Options) {
		o.Providers = []driver.Provider{noUnlockProvider{demo.NewProvider(driver.Android, dev)}}
		o.UnlockWindowPIN = hook
	})
	waitUntil(t, "the phone", func() bool { return len(g.Devices()) == 1 && g.Devices()[0].Status != DeviceOffline })
	if err := g.OpenUnlockWindow("pixel", time.Hour); !errors.Is(err, ErrNoUnlock) || asked {
		t.Fatalf("a phone without unlocking: %v, asked %v", err, asked)
	}
}

// The window unlocks the phone wherever an agent's step finds it locked,
// and goes on with the step: the agent never gets device_locked.
func TestUnlockWindowUnlocksForSteps(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T, g *Gateway) {
		t.Helper()
		if err := g.OpenUnlockWindow("pixel", 4*time.Hour); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("action", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		s, _ := connect(t, g, "Lunch")
		open(t, g)
		lockPhone(t, g, x)
		mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
		if x.Current() != "dinely" {
			t.Fatalf("screen %q", x.Current())
		}
		mustUnlockedOnce(t, g, x, s)
		// The phone locks as it would; the next step unlocks it again.
		lockPhone(t, g, x)
		mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "key", Key: "back"}), StatusSettled)
		if n := len(x.enteredPINs()); n != 2 || len(windowSteps(g, s)) != 2 {
			t.Fatalf("second lock: %d PINs, %d steps", n, len(windowSteps(g, s)))
		}
	})

	t.Run("action reads the lock", func(t *testing.T) {
		// No lock poll has seen the lock: the action's own read does.
		g, x := windowGateway(t, fakePIN, func(o *Options) { o.DiscoverEvery = time.Hour })
		s, first := connect(t, g, "Lunch")
		open(t, g)
		x.SetLocked(true)
		mustStatus(t, tap(s, ref(t, first, "Dinely")), StatusSettled)
		if x.Current() != "dinely" {
			t.Fatalf("screen %q", x.Current())
		}
		mustUnlockedOnce(t, g, x, s)
	})

	t.Run("observe", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		s, _ := connect(t, g, "Lunch")
		open(t, g)
		lockPhone(t, g, x)
		res := s.Observe(ctx, ObserveRequest{})
		mustStatus(t, res, StatusSettled)
		if res.Screen == nil || res.Screen.Locked {
			t.Fatalf("observe: %+v", res)
		}
		mustUnlockedOnce(t, g, x, s)
	})

	t.Run("connect", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		open(t, g)
		lockPhone(t, g, x)
		s, res := connect(t, g, "Lunch")
		if strings.Contains(res.Message, "locked") || res.Screen == nil || res.Screen.Locked {
			t.Fatalf("connect: %+v", res)
		}
		mustUnlockedOnce(t, g, x, s)
	})

	t.Run("handoff to unlock", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		s, _ := connect(t, g, "Lunch")
		lockPhone(t, g, x)
		mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusLocked)
		open(t, g)
		// Asked for nothing but the unlock: answered at once, nobody asked.
		res := s.Handoff(ctx, HandoffRequest{Reason: "Please unlock the phone"})
		mustStatus(t, res, StatusSettled)
		if !strings.Contains(res.Message, "Nobody was asked") || res.Screen == nil || res.Screen.Locked {
			t.Fatalf("handoff: %+v", res)
		}
		if hasNeed(g, NeedHandoff) {
			t.Fatal("a handoff card")
		}
		mustUnlockedOnce(t, g, x, s)
	})

	t.Run("handoff for more", func(t *testing.T) {
		// A handoff that asks for more than the unlock still goes to the
		// person; handed back on a locked phone, the window unlocks it.
		g, x := windowGateway(t, fakePIN)
		s, _ := connect(t, g, "Lunch")
		open(t, g)
		res := s.Handoff(ctx, HandoffRequest{Reason: "Please sign in to Dinely"})
		mustStatus(t, res, StatusPending)
		waitNeedKind(t, g, NeedHandoff)
		x.SetLocked(true)
		if err := g.HandBack("pixel", "signed in"); err != nil {
			t.Fatal(err)
		}
		res = s.Handoff(ctx, HandoffRequest{Call: Call{ApprovalID: res.ApprovalID}, Reason: "Please sign in to Dinely"})
		mustStatus(t, res, StatusSettled)
		if res.Screen == nil || res.Screen.Locked {
			t.Fatalf("handed back: %+v", res)
		}
		mustUnlockedOnce(t, g, x, s)
	})

	t.Run("read list", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		s, _ := connect(t, g, "Lunch")
		mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
		open(t, g)
		lockPhone(t, g, x)
		res := s.ReadList(ctx, ReadListRequest{Area: &screen.Rect{X: 0, Y: 300, W: 1080, H: 1500}, MaxScreens: 2})
		if res.IsError() || res.Status == StatusLocked {
			t.Fatalf("read list: %+v", res)
		}
		mustUnlockedOnce(t, g, x, s)
	})

	t.Run("approved", func(t *testing.T) {
		// The phone locks while the person decides: the window waits for
		// them, then unlocks it for the step they allowed.
		g, x := windowGateway(t, fakePIN, func(o *Options) { o.MaxBlock = 300 * time.Millisecond })
		s, _ := connect(t, g, "Lunch")
		cart := toCart(t, s)
		place := Action{Tool: "tap", Ref: ref(t, cart, "Place order · $12.80")}
		open(t, g)
		res := s.Act(ctx, Call{}, place)
		mustStatus(t, res, StatusPending)
		lockPhone(t, g, x)
		// While the approval waits, nothing unlocks the phone.
		mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusLocked)
		if len(x.enteredPINs()) != 0 {
			t.Fatal("unlocked while an approval waited")
		}
		var approvalNeed NeedView
		for _, n := range g.Needs() {
			if n.Kind == NeedApproval {
				approvalNeed = n
			}
		}
		if err := g.Decide(approvalNeed.ID, AllowOnce, ""); err != nil {
			t.Fatal(err)
		}
		mustStatus(t, s.Act(ctx, Call{ApprovalID: res.ApprovalID}, place), StatusSettled)
		if x.Current() != "done" {
			t.Fatalf("order not placed: %q", x.Current())
		}
		mustUnlockedOnce(t, g, x, s)
	})

	t.Run("leaves the background display", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		x.refuse[demo.Chatter] = true
		g.SetBackground("pixel", true)
		s, _ := connect(t, g, "Chat")
		open(t, g)
		x.SetLocked(true)
		// Chatter does not run on the background display: the task moves
		// to the phone's own screen, which the window unlocks.
		mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Chatter"}), StatusSettled)
		if x.Current() != "chats" {
			t.Fatalf("screen %q", x.Current())
		}
		mustUnlockedOnce(t, g, x, s)
	})

	// Any agent the person lets use the phone: Ask Pinky's, a remote one.
	for _, a := range []AgentInfo{
		{Name: "Pinky", Client: "pinky test", Trusted: true, Pilot: true},
		{Name: "Claude Code (remote)", Client: "claude-code 2.1", Resume: true},
	} {
		t.Run(a.Name, func(t *testing.T) {
			g, x := windowGateway(t, fakePIN)
			s := g.OpenSession(a)
			t.Cleanup(s.Close)
			mustStatus(t, s.Connect(ctx, ConnectRequest{Task: "Lunch"}), StatusSettled)
			open(t, g)
			lockPhone(t, g, x)
			mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
			mustUnlockedOnce(t, g, x, s)
		})
	}
}

// A phone that does not take the PIN gets it once: the window ends at
// once, its PIN is zeroed and the person gets the unlock card.
func TestUnlockWindowWrongPIN(t *testing.T) {
	ctx := context.Background()
	const wrong = "fake-0000"
	g, x := windowGateway(t, wrong)
	s, _ := connect(t, g, "Lunch")
	if err := g.OpenUnlockWindow("pixel", time.Hour); err != nil {
		t.Fatal(err)
	}
	pin := windowPIN(g, "pixel")
	lockPhone(t, g, x)
	res := s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"})
	mustStatus(t, res, StatusLocked)
	if !strings.HasPrefix(res.Message, windowFailedNote) {
		t.Fatalf("message %q", res.Message)
	}
	if got := x.enteredPINs(); len(got) != 1 {
		t.Fatalf("entered %d times", len(got))
	}
	if !zeroed(pin) || windowPIN(g, "pixel") != nil || !unlockUntil(g).IsZero() {
		t.Fatal("the window stays")
	}
	if !g.Devices()[0].UnlockRefused {
		t.Fatal("the card does not say the phone refused the PIN")
	}
	waitNeedKind(t, g, NeedUnlock)
	steps := windowSteps(g, s)
	if len(steps) != 1 || steps[0].Status != "wrong_pin" ||
		steps[0].Text != "Unlocking without asking failed: the phone did not accept the saved PIN. Unlock without asking has ended" {
		t.Fatalf("steps %+v", steps)
	}
	// Never again: every way to the lock leaves it to the person.
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusLocked)
	mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusLocked)
	go s.Handoff(ctx, HandoffRequest{Reason: "Please unlock the phone"})
	waitNeedKind(t, g, NeedHandoff)
	if n := len(x.enteredPINs()); n != 1 {
		t.Fatalf("tried again: %d times", n)
	}
	// Unlocked on the phone, the card no longer says it refused the PIN.
	x.SetLocked(false)
	waitUntil(t, "the unlock", func() bool { return !g.Devices()[0].UnlockRefused })
}

// The manual unlock's wrong PIN ends the window too: it holds the same PIN.
func TestUnlockWindowEndsWithAWrongManualUnlock(t *testing.T) {
	g, x := windowGateway(t, fakePIN, func(o *Options) {
		o.PIN = func(context.Context, string) (string, error) { return "fake-0000", nil }
	})
	connect(t, g, "Lunch")
	if err := g.OpenUnlockWindow("pixel", time.Hour); err != nil {
		t.Fatal(err)
	}
	pin := windowPIN(g, "pixel")
	lockPhone(t, g, x)
	if err := g.Unlock("pixel"); !errors.Is(err, ErrWrongPIN) {
		t.Fatalf("unlock: %v", err)
	}
	if !zeroed(pin) || !unlockUntil(g).IsZero() {
		t.Fatal("the window stays")
	}
}

func TestUnlockWindowRunsOut(t *testing.T) {
	ctx := context.Background()
	g, x := windowGateway(t, fakePIN)
	s, _ := connect(t, g, "Lunch")
	if err := g.OpenUnlockWindow("pixel", time.Hour); err != nil {
		t.Fatal(err)
	}
	pin := windowPIN(g, "pixel")
	// By the wall clock, as after the computer slept through the timer:
	// the lock poll ends it.
	g.mu.Lock()
	g.windows["pixel"].until = time.Now().Add(-time.Second).Round(0)
	g.mu.Unlock()
	waitUntil(t, "the window to end", func() bool { return windowPIN(g, "pixel") == nil })
	if !zeroed(pin) || !unlockUntil(g).IsZero() {
		t.Fatal("the PIN was not zeroed")
	}
	lockPhone(t, g, x)
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusLocked)
	if len(x.enteredPINs()) != 0 {
		t.Fatal("a window that ran out unlocked the phone")
	}

	// Its timer ends it, unless another window took its place.
	if err := g.OpenUnlockWindow("pixel", time.Hour); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	old := g.windows["pixel"]
	g.mu.Unlock()
	if err := g.OpenUnlockWindow("pixel", 4*time.Hour); err != nil {
		t.Fatal(err)
	}
	g.expireWindow("pixel", old)
	if unlockUntil(g).IsZero() {
		t.Fatal("the replaced window's timer ended the new one")
	}
	g.mu.Lock()
	cur := g.windows["pixel"]
	g.mu.Unlock()
	pin = cur.pin
	g.expireWindow("pixel", cur)
	if !zeroed(pin) || !unlockUntil(g).IsZero() {
		t.Fatal("the timer did not end the window")
	}
}

// The window is not used while no task runs, for the person's own use of
// the phone, while an approval waits, for an agent that may only watch,
// on a background display, or for a run nobody started.
func TestUnlockWindowNotUsed(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T, g *Gateway) {
		t.Helper()
		if err := g.OpenUnlockWindow("pixel", 4*time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	none := func(t *testing.T, x *extraDev) {
		t.Helper()
		if n := len(x.enteredPINs()); n != 0 {
			t.Fatalf("entered the PIN %d times", n)
		}
	}

	t.Run("no task", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		open(t, g)
		lockPhone(t, g, x)
		time.Sleep(100 * time.Millisecond) // lock polls
		none(t, x)
		// Nor after a task ended.
		x.SetLocked(false)
		waitUntil(t, "the unlock", func() bool { return !gatewaySeesLocked(g, "pixel") })
		s, _ := connect(t, g, "Lunch")
		mustStatus(t, s.Release(ctx, ReleaseRequest{}), StatusSettled)
		lockPhone(t, g, x)
		time.Sleep(100 * time.Millisecond)
		none(t, x)
	})

	t.Run("taken over", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		s, _ := connect(t, g, "Lunch")
		open(t, g)
		if err := g.TakeOver("pixel"); err != nil {
			t.Fatal(err)
		}
		lockPhone(t, g, x)
		mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusTookOver)
		go s.Handoff(ctx, HandoffRequest{Reason: "Please unlock the phone"})
		time.Sleep(100 * time.Millisecond)
		none(t, x)
	})

	t.Run("paused", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		s, _ := connect(t, g, "Lunch")
		open(t, g)
		lockPhone(t, g, x)
		if err := g.PauseDevice("pixel"); err != nil {
			t.Fatal(err)
		}
		if res := s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}); res.Status == StatusSettled {
			t.Fatalf("acted on a paused phone: %+v", res)
		}
		none(t, x)
	})

	t.Run("watch only", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		g.SetAgentScope(agent.Name, Scope{Mode: ScopeWatchOnly})
		s, _ := connect(t, g, "Watch")
		open(t, g)
		lockPhone(t, g, x)
		mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusLocked)
		go s.Handoff(ctx, HandoffRequest{Reason: "Please unlock the phone"})
		time.Sleep(100 * time.Millisecond)
		none(t, x)
	})

	t.Run("background display", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		g.SetBackground("pixel", true)
		s, _ := connect(t, g, "Lunch")
		open(t, g)
		lockPhone(t, g, x)
		mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
		mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusSettled)
		none(t, x)
		if !gatewaySeesLocked(g, "pixel") {
			t.Fatal("the phone's own screen was unlocked")
		}
	})

	t.Run("scheduled", func(t *testing.T) {
		g, x := windowGateway(t, fakePIN)
		s := g.OpenSession(AgentInfo{Name: "Pinky", Client: "pinky test", Trusted: true, Pilot: true, Unattended: true})
		t.Cleanup(s.Close)
		mustStatus(t, s.Connect(ctx, ConnectRequest{Task: "Every morning"}), StatusSettled)
		open(t, g)
		lockPhone(t, g, x)
		mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusLocked)
		mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusLocked)
		waitNeedKind(t, g, NeedUnlock)
		none(t, x)
	})
}

// Removing the phone, and Pinky stopping, end the window.
func TestUnlockWindowEndsWithThePhoneAndPinky(t *testing.T) {
	g, _ := windowGateway(t, fakePIN)
	if err := g.OpenUnlockWindow("pixel", time.Hour); err != nil {
		t.Fatal(err)
	}
	pin := windowPIN(g, "pixel")
	if err := g.RemoveDevice(context.Background(), "pixel", RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if !zeroed(pin) || windowPIN(g, "pixel") != nil {
		t.Fatal("removing the phone left its window")
	}

	g, _ = windowGateway(t, fakePIN)
	if err := g.OpenUnlockWindow("pixel", time.Hour); err != nil {
		t.Fatal(err)
	}
	pin = windowPIN(g, "pixel")
	g.Close()
	if !zeroed(pin) {
		t.Fatal("Close left the PIN")
	}
	if err := g.OpenUnlockWindow("pixel", time.Hour); err == nil {
		t.Fatal("opened a window on a closed gateway")
	}
}

// The PIN never reaches the log or a step; the failures say why.
func TestUnlockWindowKeepsThePINOut(t *testing.T) {
	ctx := context.Background()
	var buf syncBuffer
	g, x := windowGateway(t, fakePIN, func(o *Options) {
		o.Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	s, _ := connect(t, g, "Lunch")
	if err := g.OpenUnlockWindow("pixel", time.Hour); err != nil {
		t.Fatal(err)
	}
	lockPhone(t, g, x)
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
	g.mu.Lock()
	l := s.leases["pixel"]
	g.mu.Unlock()
	until := time.Now().Add(time.Hour)
	g.recordWindowUnlock(l, time.Now(), until, driver.ErrPINNotEntered)
	g.recordWindowUnlock(l, time.Now(), until, errors.New("adb: broken pipe"))
	steps := windowSteps(g, s)
	if len(steps) != 3 || !strings.HasPrefix(steps[0].Text, "Unlocked without asking (until ") {
		t.Fatalf("steps %+v", steps)
	}
	for i, w := range []struct{ text, status string }{
		{"Unlocking without asking failed: Pinky could not get to the passcode screen and entered nothing. Unlock without asking has ended", "pin_not_entered"},
		{"Unlocking without asking failed. Unlock without asking has ended", string(ErrInternal)},
	} {
		if st := steps[i+1]; st.Text != w.text || st.Status != w.status {
			t.Errorf("step %d: %q %q, want %q %q", i+1, st.Text, st.Status, w.text, w.status)
		}
	}
	for _, st := range taskSteps(g, s) {
		if strings.Contains(st.Text+st.Args, fakePIN) {
			t.Fatalf("the PIN in a step: %+v", st)
		}
	}
	if !strings.Contains(buf.String(), "unlock without asking") {
		t.Fatal("the log has no unlock")
	}
	if strings.Contains(buf.String(), fakePIN) {
		t.Fatal("the PIN in the log")
	}
}

// syncBuffer is a bytes.Buffer for a log that several goroutines write.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
