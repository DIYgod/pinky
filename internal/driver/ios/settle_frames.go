package ios

import (
	"context"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// Settling below iOS 27 watches the picture: a frame that differs from the
// one before it (other bytes; the phone encodes a still screen the same way
// every time) means the screen is still moving. As on iOS 27, the screen
// counts as settled once settleMin passed since the call and nothing
// changed for settleQuiet.
//
// Two things a still picture does not prove (field report, an iPad on
// iPadOS 26 over Wi-Fi, 2026-11-26: Settings opened over a game that runs
// in landscape, while the iPad stood upright, was reported settled with its
// elements where they were halfway through the turn, and OCR read the
// game's notice from the picture):
//
//   - A gap in the frames. WebDriverAgent's MJPEG server sends frames of a
//     still screen too, 15 a second, and leaves them out only when it could
//     not take a picture, as while the screen turns. Frames on both sides of
//     a gap may be the same while the screen moved in between, so the quiet
//     stretch starts again after one.
//   - Still frames turned the other way than the app in front: the last
//     app's picture, before the new one has turned the screen. The frames'
//     EXIF tag says which way they are turned and GET /rotation which way
//     the app in front is (frameFits).

// frameSettle waits until frames from next stop changing, or until limit.
// next returns each frame's key as it arrives. No frame for settleQuiet or
// more starts the quiet stretch again. ready, when not nil, is asked once
// the frames are still: false means the still picture is not the screen
// to wait for yet, and the wait goes on.
func frameSettle(ctx context.Context, limit time.Duration, next func(context.Context) (uint64, error), ready func(context.Context) bool) (bool, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	var last uint64
	changed, prev := start, start
	seen := false
	for {
		key, err := next(ctx)
		now := time.Now()
		if err != nil {
			if ctx.Err() != nil && now.Sub(start) >= limit {
				return false, nil // limit reached while waiting for a frame
			}
			return false, err
		}
		if !seen || key != last || now.Sub(prev) >= settleQuiet {
			last, seen, changed = key, true, now
		}
		prev = now
		if now.Sub(start) >= settleMin && now.Sub(changed) >= settleQuiet {
			if ready == nil || ready(ctx) {
				return true, nil
			}
			// Frames waited meanwhile: no gap.
			changed, prev = time.Now(), time.Now()
		}
	}
}

// WaitSettled implements driver.Settler with WebDriverAgent's MJPEG
// picture, 15 frames a second. Still frames count once they are turned the
// way the app in front is (frameFits).
func (d *WDADevice) WaitSettled(ctx context.Context, limit time.Duration) (bool, error) {
	if _, err := d.c.session(ctx); err != nil {
		return false, err
	}
	sctx, cancel := context.WithTimeout(ctx, limit+time.Second)
	defer cancel()
	src, err := openMJPEG(sctx, d.c.long, d.c.mjpeg)
	if err != nil {
		return false, err
	}
	defer src.Close()
	var last frame
	return frameSettle(ctx, limit, func(ctx context.Context) (uint64, error) {
		f, err := src.next(ctx)
		last = f
		return f.key, err
	}, func(ctx context.Context) bool { return d.frameFits(ctx, last) })
}

// frameFits reports whether a frame, turned upright, is wide when the app
// in front is in landscape and tall when it is not, by GET /rotation, read
// now (the app that just came to the front may be turned another way than
// the last read said). It answers true when WDA does not say, or the
// frame's size or the way it is turned is unknown (frame.wide): a
// WebDriverAgent that sent the panel without an EXIF orientation tag would
// otherwise leave every settle in a landscape app waiting for the limit.
func (d *WDADevice) frameFits(ctx context.Context, f frame) bool {
	wide, ok := f.wide()
	if !ok {
		return true
	}
	q, ok := d.turnNow(ctx)
	if !ok {
		return true
	}
	return wide == q.swaps()
}

// WaitSettled implements driver.Settler with screenshots: the screen is
// settled when two in a row are the same and settleMin has passed.
func (a *AXDevice) WaitSettled(ctx context.Context, limit time.Duration) (bool, error) {
	start := time.Now()
	deadline := start.Add(limit)
	var last uint64
	seen := false
	for time.Now().Before(deadline) {
		c, cancel := context.WithDeadline(ctx, deadline)
		b, err := a.d.ScreenshotPNG(c)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if time.Now().After(deadline) {
				return false, nil
			}
			return false, err
		}
		key := frameKey(b)
		if seen && key == last && time.Since(start) >= settleMin {
			return true, nil
		}
		last, seen = key, true
	}
	return false, nil
}

var (
	_ driver.Settler = (*WDADevice)(nil)
	_ driver.Settler = (*AXDevice)(nil)
)
