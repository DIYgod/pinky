package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
)

// movingDev is a demo phone whose screen never stops moving, as in a game
// or a video: it notes how long each action waits for the screen to
// settle, and the wait always runs out.
type movingDev struct {
	*demo.Device
	mu     sync.Mutex
	limits []time.Duration
}

func (d *movingDev) WaitSettled(ctx context.Context, limit time.Duration) (bool, error) {
	d.mu.Lock()
	d.limits = append(d.limits, limit)
	d.mu.Unlock()
	t := time.NewTimer(limit)
	defer t.Stop()
	select {
	case <-t.C:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// waits returns the limits asked for since the last call.
func (d *movingDev) waits() []time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := d.limits
	d.limits = nil
	return out
}

func settleMS(n int) *time.Duration {
	d := time.Duration(n) * time.Millisecond
	return &d
}

func sameWaits(t *testing.T, what string, got []time.Duration, want ...time.Duration) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s waited %v for the screen to settle, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s waited %v for the screen to settle, want %v", what, got, want)
		}
	}
}

// settle_ms lowers the wait for a screen that never settles; it never
// raises it above SettleMax, and 0 reads the screen without waiting.
func TestSettleMSLowersTheWait(t *testing.T) {
	ctx := context.Background()
	base := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	dev := &movingDev{Device: base}
	g := newDevGateway(t, base, dev, func(o *Options) { o.SettleMax = 400 * time.Millisecond })
	s, _ := connect(t, g, "Play a game")
	dev.waits()

	res := s.Act(ctx, Call{}, Action{Tool: "swipe", Direction: "up"})
	mustStatus(t, res, StatusSettled)
	if res.Settle != "timeout" {
		t.Fatalf("settle %q, want timeout", res.Settle)
	}
	sameWaits(t, "a swipe without settle_ms", dev.waits(), 400*time.Millisecond)

	res = s.Act(ctx, Call{}, Action{Tool: "swipe", Direction: "down", Settle: settleMS(50)})
	mustStatus(t, res, StatusSettled)
	if res.Settle != "timeout" {
		t.Fatalf("settle %q, want timeout", res.Settle)
	}
	sameWaits(t, "settle_ms 50", dev.waits(), 50*time.Millisecond)

	res = s.Act(ctx, Call{}, Action{Tool: "swipe", Direction: "up", Settle: settleMS(2000)})
	mustStatus(t, res, StatusSettled)
	sameWaits(t, "settle_ms above SettleMax", dev.waits(), 400*time.Millisecond)

	res = s.Act(ctx, Call{}, Action{Tool: "swipe", Direction: "down", Settle: settleMS(0)})
	mustStatus(t, res, StatusSettled)
	if res.Settle != "timeout" {
		t.Fatalf("settle %q, want timeout", res.Settle)
	}
	sameWaits(t, "settle_ms 0", dev.waits())
}

func TestSettleMSOutOfRange(t *testing.T) {
	ctx := context.Background()
	base := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	dev := &movingDev{Device: base}
	g := newDevGateway(t, base, dev)
	s, _ := connect(t, g, "Play a game")
	for _, n := range []int{-1, 3001} {
		res := s.Act(ctx, Call{}, Action{Tool: "swipe", Direction: "up", Settle: settleMS(n)})
		if res.Error != ErrInvalidArgument {
			t.Fatalf("settle_ms %d: %+v, want invalid_argument", n, res)
		}
	}
}

// Each step of a batch waits as long as its own settle_ms says.
func TestSettleMSPerBatchStep(t *testing.T) {
	ctx := context.Background()
	base := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	dev := &movingDev{Device: base}
	g := newDevGateway(t, base, dev, func(o *Options) { o.SettleMax = 300 * time.Millisecond })
	s, _ := connect(t, g, "Play a game")
	dev.waits()

	res := s.Batch(ctx, BatchRequest{Steps: []Action{
		{Tool: "swipe", Direction: "up", Settle: settleMS(20)},
		{Tool: "swipe", Direction: "down"},
		{Tool: "swipe", Direction: "up", Settle: settleMS(0)},
	}})
	if res.IsError() || res.Status != StatusSettled {
		t.Fatalf("batch: %+v", res)
	}
	sameWaits(t, "the batch", dev.waits(), 20*time.Millisecond, 300*time.Millisecond)
}
