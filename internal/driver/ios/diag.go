package ios

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/DIYgod/pinky/internal/screen"
)

// Diagnostics for iosctl and device tests.

// Services opens a tunnel to the device and lists the RemoteXPC services it
// publishes, with a one-line summary of the tunnel.
func Services(ctx context.Context, udid string) ([]string, string, error) {
	l, err := dialLink(ctx, udid, false)
	if err != nil {
		return nil, "", err
	}
	defer l.Close()
	var names []string
	for n, s := range l.rsd.GetServices() {
		names = append(names, fmt.Sprintf("%s (port %d)", n, s.Port))
	}
	sort.Strings(names)
	info := fmt.Sprintf("host %s, device %s, RSD port %d, MTU %d", l.tunnel.HostAddr(), l.tunnel.DeviceAddr(), l.tunnel.RSDPort(), l.tunnel.MTU())
	return names, info, nil
}

// SwipeLatency swipes back and forth n times and returns, for each swipe,
// how long after the touch went down the first changed video frame
// arrived: input, rendering, encoding and transport together. Horizontal
// swipes page the home screen; vertical ones scroll a list such as
// Settings. Measure on a screen that is otherwise still: animated widgets
// make frames that look like changes.
func (d *Device) SwipeLatency(ctx context.Context, n int, vertical bool) ([]time.Duration, error) {
	s := d.screen()
	left := screen.Point{X: s.Width * 8 / 10, Y: s.Height / 2}
	right := screen.Point{X: s.Width * 2 / 10, Y: s.Height / 2}
	if vertical {
		left = screen.Point{X: s.Width / 2, Y: s.Height * 6 / 10}
		right = screen.Point{X: s.Width / 2, Y: s.Height * 4 / 10}
	}
	var out []time.Duration
	for i := 0; i < n; i++ {
		from, to := left, right
		if i%2 == 1 {
			from, to = right, left
		}
		// Start from a still screen: a list keeps scrolling for seconds
		// after a fling.
		if _, err := d.WaitSettled(ctx, 5*time.Second); err != nil {
			return out, err
		}
		m, _, err := d.live(ctx)
		if err != nil {
			return out, err
		}
		var before []int
		for _, f := range m.framesSince(time.Now().Add(-300 * time.Millisecond)) {
			before = append(before, f.size)
		}
		sort.Ints(before)
		base := 1500
		if len(before) > 0 {
			base = before[len(before)/2]
		}
		limit := 2 * base
		if limit < base+2000 {
			limit = base + 2000
		}
		var down time.Time
		err = d.gestureHook(ctx, swipePath(from, to, 300*time.Millisecond), 10*time.Millisecond, func() { down = time.Now() })
		if err != nil {
			return out, err
		}
		if err := sleepCtx(ctx, 1200*time.Millisecond); err != nil {
			return out, err
		}
		for _, f := range m.framesSince(down) {
			if f.size > limit {
				out = append(out, f.at.Sub(down))
				break
			}
		}
	}
	return out, nil
}
