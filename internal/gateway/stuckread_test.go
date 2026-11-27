package gateway

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// stuckAX is what the iOS driver's screen reads fail with while the
// iPhone's accessibility daemon is stuck (axStuckError in
// internal/driver/ios, GitHub issue #248): words for the agent, with the
// read's own error last. The error wraps nothing, though the read's own
// wrapped a deadline.
const stuckAX = "the iPhone's accessibility service (axauditd) has stopped answering: screen reads have failed for 3m12s, " +
	"while screenshots and taps still work. Restarting Pinky or reconnecting the iPhone does not bring it back; " +
	"only restarting the iPhone does. Do not try to restart the iPhone yourself: call phone_handoff and ask the person " +
	"to restart it, then read the screen again (last error: accessibility daemon: context deadline exceeded)"

// stuckDev is a demo phone whose every read fails with stuckAX once it is
// stuck, which it gets after its next tap when stickOnTap is set.
type stuckDev struct {
	*demo.Device
	mu         sync.Mutex
	stuck      bool
	stickOnTap bool
}

func (d *stuckDev) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	d.mu.Lock()
	stuck := d.stuck
	d.mu.Unlock()
	if stuck {
		return nil, errors.New(stuckAX)
	}
	return d.Device.Observe(ctx, opt)
}

func (d *stuckDev) Tap(ctx context.Context, p screen.Point, opt driver.TapOptions) error {
	err := d.Device.Tap(ctx, p, opt)
	d.mu.Lock()
	d.stuck = d.stuck || d.stickOnTap
	d.mu.Unlock()
	return err
}

func stuckGateway(t *testing.T) (*Session, *stuckDev, *Result) {
	t.Helper()
	base := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	dev := &stuckDev{Device: base}
	g := newDevGateway(t, base, dev)
	s, first := connect(t, g, "Stuck accessibility")
	return s, dev, first
}

// TestStuckAccessibilityReachesTheAgent (#248): the driver's words about a
// stuck accessibility daemon reach the agent as they are, from
// phone_observe and from the read after an action, so that it hands the
// phone over for a restart.
func TestStuckAccessibilityReachesTheAgent(t *testing.T) {
	t.Run("observe", func(t *testing.T) {
		s, dev, _ := stuckGateway(t)
		dev.mu.Lock()
		dev.stuck = true
		dev.mu.Unlock()
		res := s.Observe(context.Background(), ObserveRequest{})
		if res.Error != ErrInternal || !strings.Contains(res.Message, stuckAX) || strings.Contains(res.Message, "timed out") {
			t.Fatalf("phone_observe: %q %q", res.Error, res.Message)
		}
	})
	t.Run("after a tap", func(t *testing.T) {
		s, dev, first := stuckGateway(t)
		dev.mu.Lock()
		dev.stickOnTap = true
		dev.mu.Unlock()
		res := tap(s, ref(t, first, "Dinely"))
		mustStatus(t, res, StatusUncertain)
		if !strings.Contains(res.Message, "could not be read afterwards") || !strings.Contains(res.Message, stuckAX) {
			t.Fatalf("phone_tap: %q", res.Message)
		}
	})
}
