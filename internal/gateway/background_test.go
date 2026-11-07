package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

func TestBackgroundDisplay(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	g := newExtraGateway(t, x)
	if err := g.SetBackground("pixel", true); err != nil {
		t.Fatal(err)
	}
	s, _ := connect(t, g, "Lunch")
	bgs := x.backgrounds()
	if len(bgs) != 1 {
		t.Fatalf("%d background displays opened", len(bgs))
	}
	bg := bgs[0]
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
	if bg.Current() != "dinely" || x.Current() != "home" {
		t.Fatalf("the launch reached the phone's screen: background %q, phone %q", bg.Current(), x.Current())
	}
	if d := g.Devices()[0]; !strings.Contains(d.StatusLine, "in the background") || !d.Features.Background {
		t.Fatalf("device %+v", d)
	}

	// The phone's own lock does not stop the agent, and asks nobody.
	x.SetLocked(true)
	time.Sleep(60 * time.Millisecond) // the lock poll
	res := s.Act(ctx, Call{}, Action{Tool: "launch", App: "Chatter"})
	mustStatus(t, res, StatusSettled)
	for _, n := range g.Needs() {
		if n.Kind == NeedUnlock {
			t.Fatalf("an unlock card for a task on a background display: %+v", n)
		}
	}
	// The person using the phone (or the mirror of its screen) does not
	// pause it either; Take over does.
	x.PersonTouch()
	if err := g.Touch("pixel", driver.TouchUp, screen.Point{X: 10, Y: 10}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "key", Key: "back"}), StatusSettled)
	if err := g.TakeOver("pixel"); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "key", Key: "back"}), StatusTookOver)
	if err := g.HandBack("pixel", ""); err != nil {
		t.Fatal(err)
	}

	// The display closes with the task.
	mustStatus(t, s.Release(ctx, ReleaseRequest{}), StatusSettled)
	deadline := time.Now().Add(2 * time.Second)
	for !bg.isClosed() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !bg.isClosed() {
		t.Fatal("the background display stayed open after the task")
	}
	if _, err := g.AgentStream(ctx, "pixel", driver.StreamOptions{}); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("agent stream without a task: %v", err)
	}
}

// An app that will not run on a background display moves the task to the
// phone's own screen, and the agent hears why.
func TestBackgroundRefusedFallsBack(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	x.refuse[demo.Chatter] = true
	g := newExtraGateway(t, x)
	g.SetBackground("pixel", true)
	s, _ := connect(t, g, "Chat")
	res := s.Act(ctx, Call{}, Action{Tool: "launch", App: "Chatter"})
	mustStatus(t, res, StatusSettled)
	if !strings.Contains(res.Message, "does not run on a background display") {
		t.Fatalf("message %q", res.Message)
	}
	if x.Current() != "chats" {
		t.Fatalf("Chatter is not on the phone's screen: %q", x.Current())
	}
	if !x.backgrounds()[0].isClosed() {
		t.Fatal("the background display stayed open")
	}
	// The rest of the task stays on the phone's screen.
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "key", Key: "home"}), StatusSettled)
	if x.Current() != "home" || len(x.backgrounds()) != 1 {
		t.Fatalf("phone %q, %d displays", x.Current(), len(x.backgrounds()))
	}

	// When the phone's screen is locked, the launch does not happen there.
	x2 := newExtraDev("pixel", "Pixel 9")
	x2.refuse[demo.Chatter] = true
	g2 := newExtraGateway(t, x2)
	g2.SetBackground("pixel", true)
	s2, _ := connect(t, g2, "Chat")
	x2.SetLocked(true)
	res = s2.Act(ctx, Call{}, Action{Tool: "launch", App: "Chatter"})
	mustStatus(t, res, StatusLocked)
	if x2.Current() == "chats" {
		t.Fatal("launched on a locked phone")
	}
}

func TestBackgroundCannotOpen(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	x.bgErr = errors.New("this phone has no virtual displays")
	g := newExtraGateway(t, x)
	g.SetBackground("pixel", true)
	s := g.OpenSession(agent)
	defer s.Close()
	res := s.Connect(ctx, ConnectRequest{Task: "Lunch"})
	mustStatus(t, res, StatusSettled)
	if !strings.Contains(res.Message, "could not open a background display") {
		t.Fatalf("message %q", res.Message)
	}
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
	if x.Current() != "dinely" {
		t.Fatalf("phone %q", x.Current())
	}
}

func TestBackgroundSetting(t *testing.T) {
	g, _ := newTestGateway(t)
	if err := g.SetBackground("pixel", true); !errors.Is(err, driver.ErrUnsupported) {
		t.Fatalf("a phone without background displays: %v", err)
	}
	if err := g.SetBackground("nope", true); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("unknown phone: %v", err)
	}
	// Kept across restarts.
	x := newExtraDev("pixel", "Pixel 9")
	dir := t.TempDir()
	g1 := newExtraGateway(t, x, func(o *Options) { o.DataDir = dir })
	if err := g1.SetBackground("pixel", true); err != nil {
		t.Fatal(err)
	}
	g1.Close()
	g2 := newExtraGateway(t, x, func(o *Options) { o.DataDir = dir })
	if !g2.Background("pixel") {
		t.Fatal("background off after a restart")
	}
}

func TestWakeBeforeTheAgentActs(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	g := newExtraGateway(t, x)
	x.setScreen(false)
	s, _ := connect(t, g, "Lunch")
	if x.wakes != 1 {
		t.Fatalf("woke %d times for the first screen", x.wakes)
	}
	// Woken moments ago: not again (the screen stays on for its timeout).
	x.setScreen(false)
	mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusSettled)
	if x.wakes != 1 {
		t.Fatalf("woke %d times", x.wakes)
	}
	g.mu.Lock()
	g.devices["pixel"].wokeAt = time.Time{}
	g.mu.Unlock()
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
	if x.wakes != 2 {
		t.Fatalf("woke %d times, want once more before the action", x.wakes)
	}
	// A task on a background display leaves the phone's screen off.
	x2 := newExtraDev("pixel", "Pixel 9")
	g2 := newExtraGateway(t, x2)
	g2.SetBackground("pixel", true)
	x2.setScreen(false)
	s2, _ := connect(t, g2, "Lunch")
	mustStatus(t, s2.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
	if x2.wakes != 0 {
		t.Fatalf("woke the phone %d times for a background task", x2.wakes)
	}
}

// unreadable is a background display that cannot be read, like a virtual
// display Android keeps from accessibility services.
type unreadable struct{ *bgDisplay }

func (u unreadable) Observe(context.Context, driver.ObserveOptions) (*screen.Screen, error) {
	return nil, fmt.Errorf("display 358 is not open to accessibility services: %w", driver.ErrUnsupported)
}

type unreadableDev struct{ *extraDev }

func (x unreadableDev) OpenBackground(ctx context.Context, opt driver.BackgroundOptions) (driver.Device, error) {
	d, err := x.extraDev.OpenBackground(ctx, opt)
	if err != nil {
		return nil, err
	}
	return unreadable{d.(*bgDisplay)}, nil
}

func TestBackgroundUnreadableFallsBack(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	g := newExtraGateway(t, x, func(o *Options) {
		o.Providers = []driver.Provider{extraProvider2{demo.NewProvider(driver.Android, x.Device), unreadableDev{x}}}
	})
	g.SetBackground("pixel", true)
	s := g.OpenSession(agent)
	defer s.Close()
	res := s.Connect(ctx, ConnectRequest{Task: "Lunch"})
	mustStatus(t, res, StatusSettled)
	if !strings.Contains(res.Message, "cannot read the background display") {
		t.Fatalf("message %q", res.Message)
	}
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}), StatusSettled)
	if x.Current() != "dinely" || !x.backgrounds()[0].isClosed() {
		t.Fatalf("phone %q", x.Current())
	}
}

type extraProvider2 struct {
	*demo.Provider
	d driver.Device
}

func (p extraProvider2) Open(context.Context, string) (driver.Device, error) { return p.d, nil }
