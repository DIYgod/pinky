//go:build device

package ios

import (
	"context"
	"testing"
	"time"
)

// TestDeviceLockScreenPad shows what unlocking sees on the lock screen,
// without typing anything: lock the phone first. It wakes the phone,
// swipes up and looks for the passcode pad, logging every error.
func TestDeviceLockScreenPad(t *testing.T) {
	d := testDevice(t)
	ctx := context.Background()
	p := devicePower{d}
	locked, err := p.locked(ctx)
	t.Logf("locked: %v (%v)", locked, err)
	if !locked {
		t.Skip("lock the phone first")
	}
	start := time.Now()
	t.Logf("wake: %v (%v)", wake(ctx, p), time.Since(start).Round(time.Millisecond))
	on, err := p.screenOn(ctx)
	t.Logf("screen on: %v (%v)", on, err)
	if err := p.swipeUp(ctx); err != nil {
		t.Fatalf("swipe up: %v", err)
	}
	time.Sleep(afterSwipe)
	for i := 0; i < 10; i++ {
		start := time.Now()
		shown, err := p.keypadShown(ctx)
		t.Logf("keypad shown: %v, err %v (%v)", shown, err, time.Since(start).Round(time.Millisecond))
		if shown {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	img, err := d.Screenshot(ctx)
	if err == nil {
		t.Logf("a screenshot works on the lock screen: %v", img.Bounds())
	} else {
		t.Logf("screenshot: %v", err)
	}
	locked, err = p.locked(ctx)
	t.Logf("still locked: %v (%v)", locked, err)
}
