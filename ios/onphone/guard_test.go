package onphone

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

// Input is refused while Pinky's app is in front, as an action that was
// not done.
func TestGateRefusesWhilePinkyIsInFront(t *testing.T) {
	g := newGate(policy.IPhoneApp)
	if err := g.check(); err != nil {
		t.Fatal(err)
	}
	g.setUI(true)
	err := g.check()
	if !errors.Is(err, ErrOwnUI) || !errors.Is(err, driver.ErrUnsupported) {
		t.Fatalf("check = %v", err)
	}
	g.setUI(false)
	if err := g.check(); err != nil {
		t.Fatal(err)
	}
}

// A read learns the app the task works in, never Pinky's or the home
// screen; it names Pinky's app where the driver could not; and another app
// read well after Pinky's app said it came to the front means it left.
func TestGateLearnsFromReads(t *testing.T) {
	g := newGate("cc.diygod.pinky.ABCDE12345")
	read := func(id string, start time.Time) screen.App {
		s := &screen.Screen{App: screen.App{ID: id}}
		g.saw(s, start)
		return s.App
	}
	read("com.apple.calculator", time.Now())
	read(springboard, time.Now())
	read("cc.diygod.pinky.ABCDE12345", time.Now())
	read("pinky.abcde12345", time.Now())
	read("", time.Now())
	if got := g.lastApp(); got != "com.apple.calculator" {
		t.Fatalf("last app = %q", got)
	}

	g.setUI(true)
	if app := read("", time.Now()); app.ID != "cc.diygod.pinky.ABCDE12345" {
		t.Fatalf("Pinky's screen unnamed: %+v", app)
	}
	// Started before Pinky's app came to the front: the app it found is
	// old news.
	read("com.apple.Preferences", time.Now().Add(-time.Second))
	if !g.uiActive() {
		t.Fatal("an old read cleared Pinky's app")
	}
	read("com.apple.Preferences", time.Now().Add(staleUI+time.Second))
	if g.uiActive() {
		t.Fatal("a later read of another app kept Pinky's app in front")
	}
	if got := g.lastApp(); got != "com.apple.Preferences" {
		t.Fatalf("last app = %q", got)
	}
}

func TestGateWaitUI(t *testing.T) {
	g := newGate(policy.IPhoneApp)
	g.setUI(true)
	if g.waitUI(context.Background(), false, 20*time.Millisecond) {
		t.Fatal("waitUI reported a change that did not happen")
	}
	time.AfterFunc(10*time.Millisecond, func() { g.setUI(false) })
	if !g.waitUI(context.Background(), false, time.Second) {
		t.Fatal("waitUI missed the change")
	}
}
