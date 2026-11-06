package ios

import (
	"context"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// settleQuiet is how long the screen must stay still to count as settled.
const settleQuiet = 250 * time.Millisecond

// WaitSettled implements driver.Settler. The display stream keeps sending
// frames while nothing moves, but they are tiny skip frames (about 1 KB on
// an iPhone 13 mini); a change shows up as frames several times larger. The
// screen counts as settled once no large frame and no accessibility screen
// or layout notification arrived for settleQuiet, counted from the call.
func (d *Device) WaitSettled(ctx context.Context, max time.Duration) (bool, error) {
	start := time.Now()
	deadline := start.Add(max)
	for {
		m, _, err := d.live(ctx)
		if err != nil {
			return false, err
		}
		now := time.Now()
		if now.Sub(start) >= settleQuiet && d.quietSince(m, now.Add(-settleQuiet)) {
			return true, nil
		}
		if now.After(deadline) {
			return false, nil
		}
		if err := sleepCtx(ctx, 30*time.Millisecond); err != nil {
			return false, err
		}
	}
}

// quietSince reports whether nothing moved on screen since t.
func (d *Device) quietSince(m *media, t time.Time) bool {
	d.svcMu.Lock()
	ax := d.ax
	d.svcMu.Unlock()
	if ax != nil && ax.lastChange().After(t) {
		return false
	}
	recent := m.framesSince(t.Add(-2 * time.Second))
	if len(recent) == 0 {
		return true
	}
	base := recent[0].size
	for _, f := range recent {
		if f.size < base {
			base = f.size
		}
	}
	limit := 4 * base
	if limit < 4096 {
		limit = 4096
	}
	for _, f := range recent {
		if f.at.After(t) && f.size > limit {
			return false
		}
	}
	return true
}

var _ driver.Settler = (*Device)(nil)
