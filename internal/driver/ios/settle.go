package ios

import (
	"context"
	"image"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/video"
)

// settleQuiet is how long the screen must stay still to count as settled,
// and settleMin how long WaitSettled waits at least: a tap on a list row
// starts moving the picture up to about 330 ms after touch-down, so a short
// quiet stretch right after the call proves nothing.
const (
	settleQuiet = 250 * time.Millisecond
	settleMin   = 500 * time.Millisecond
)

// WaitSettled implements driver.Settler. The display stream keeps sending
// frames while nothing moves, all of one size; motion makes the sizes vary.
// The screen counts as settled once settleMin passed since the call and the
// frames of the last settleQuiet kept one size, with no accessibility screen
// or layout notification in that time. When frame sizes still vary, it
// can instead use the decoded pictures to check for visible motion.
func (d *Device) WaitSettled(ctx context.Context, limit time.Duration) (bool, error) {
	start := time.Now()
	deadline := start.Add(limit)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var visual chan time.Time
	var visualMedia *media
	for {
		m, _, err := d.live(ctx)
		if err != nil {
			return false, err
		}
		now := time.Now()
		if now.Sub(start) >= settleMin && d.quietSince(m, now.Add(-settleQuiet)) {
			return true, nil
		}
		if now.After(deadline) {
			return false, nil
		}
		// If frame sizes keep varying, look at the decoded picture. This
		// also works for encoders that vary the size of a still frame.
		if visualMedia != m && now.Sub(start) >= settleMin {
			visualMedia = m
			visual = make(chan time.Time)
			go watchVisualQuiet(ctx, m, visual)
		}
		select {
		case since := <-visual:
			if !d.axChangedSince(since) && m.alive() {
				return true, nil
			}
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(30 * time.Millisecond):
		}
	}
}

// quietSince reports whether nothing moved on screen since t: either no
// frame came while the stream is alive (the phone stops sending frames
// about 7 s after the last change), or every frame since t has the same
// size, give or take 2% or 64 bytes. The size of a still frame depends on
// the encoder's bitrate at the time, about 1–2 KB when idle and a padded
// 10 KB for seconds after motion, so a constant size is the sign, not a
// small one.
func (d *Device) quietSince(m *media, t time.Time) bool {
	if d.axChangedSince(t) {
		return false
	}
	fs := m.framesSince(t)
	if len(fs) == 0 {
		return m.alive()
	}
	if len(fs) < 3 {
		return false
	}
	lo, hi := fs[0].size, fs[0].size
	for _, f := range fs[1:] {
		lo, hi = min(lo, f.size), max(hi, f.size)
	}
	return hi-lo <= max(64, lo/50)
}

func (d *Device) axChangedSince(t time.Time) bool {
	d.svcMu.Lock()
	ax := d.ax
	d.svcMu.Unlock()
	return ax != nil && ax.lastChange().After(t)
}

// watchVisualQuiet samples small pictures only while the size-based check
// is inconclusive. The anchor is updated on visible changes, so even slow
// motion must stay within the tolerance for the whole quiet window.
func watchVisualQuiet(ctx context.Context, m *media, quiet chan<- time.Time) {
	st := &mediaStream{m: m, s: m.subscribe()}
	defer st.Close()
	dec, err := video.NewDecoder(st.Codec(), video.DecoderOptions{Software: true, Threads: 2, Smooth: true})
	if err != nil {
		return
	}
	defer dec.Close()
	const width, height = 72, 144
	anchor, current := image.NewRGBA(image.Rect(0, 0, width, height)), image.NewRGBA(image.Rect(0, 0, width, height))
	var changed, sampled time.Time
	var turn int
	for {
		select {
		case <-ctx.Done():
			return
		case packet, ok := <-st.Packets():
			if !ok {
				return
			}
			if dec.Send(packet) != nil {
				changed = time.Time{}
				continue
			}
			for {
				_, ok, err := dec.Receive()
				if err != nil {
					changed = time.Time{}
				}
				if !ok || err != nil {
					break
				}
				now := time.Now()
				if now.Sub(sampled) < 40*time.Millisecond {
					continue
				}
				sampled = now
				if dec.Convert(current) != nil {
					changed = time.Time{}
					continue
				}
				if changed.IsZero() || turn != packet.Turn || hasVisualMotion(anchor, current) {
					changed, turn = now, packet.Turn
					anchor, current = current, anchor
				} else if now.Sub(changed) >= settleQuiet && len(st.Packets()) <= 2 {
					select {
					case quiet <- changed:
					default:
					}
				}
			}
		}
	}
}

// hasVisualMotion ignores modest encoder noise in a thumbnail.
func hasVisualMotion(a, b *image.RGBA) bool {
	changed := 0
	for i := 0; i < len(a.Pix); i += 4 {
		delta := 0
		for c := 0; c < 3; c++ {
			d := int(a.Pix[i+c]) - int(b.Pix[i+c])
			delta += max(d, -d)
		}
		if delta > 30 {
			changed++
		}
	}
	return changed*200 > a.Bounds().Dx()*a.Bounds().Dy()
}

var _ driver.Settler = (*Device)(nil)
