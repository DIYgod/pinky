package harmonyos

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

type mirrorFrame struct {
	data          []byte
	width, height int
	at            time.Time
	surface       surface
}

func (d *device) jpegFrame(ctx context.Context, longSide int) (mirrorFrame, error) {
	s, err := d.focusedSettings(ctx)
	if err != nil {
		return mirrorFrame{}, err
	}
	w, h := s.width, s.height
	if longest := max(w, h); longSide > 0 && longSide < longest {
		w, h = w*longSide/longest, h*longSide/longest
	}
	data, err := d.capture(ctx, fmt.Sprintf("snapshot_display -w %d -h %d -f ", w, h), ".jpeg", "base64", focusCommand)
	if err != nil {
		return mirrorFrame{}, err
	}
	at := time.Now()
	payload, after, err := checkedCapture(data)
	if err != nil {
		return mirrorFrame{}, err
	}
	if after != s {
		return mirrorFrame{}, fmt.Errorf("the HarmonyOS window changed during capture")
	}
	b, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(payload)))
	if err != nil {
		return mirrorFrame{}, fmt.Errorf("invalid HarmonyOS screenshot transfer: %w", err)
	}
	conf, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil || conf.Width != w || conf.Height != h {
		return mirrorFrame{}, fmt.Errorf("HarmonyOS screenshot dimensions do not match the focused window")
	}
	return mirrorFrame{data: b, width: w, height: h, at: at, surface: s}, nil
}

func (d *device) Screenshot(ctx context.Context) (image.Image, error) {
	f, err := d.jpegFrame(ctx, 0)
	if err != nil {
		return nil, err
	}
	return jpeg.Decode(bytes.NewReader(f.data))
}

// MirrorOnly keeps the live feed out of task video recordings.
func (d *device) MirrorOnly() bool { return true }

// WaitSettled reuses the native feed when the desktop preview is open.
// Explicit agent screenshots are still taken by Screenshot.
func (d *device) WaitSettled(ctx context.Context, limit time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	d.previewMu.Lock()
	preview := d.preview
	d.previewMu.Unlock()
	if preview == nil {
		return false, nil
	}
	return preview.waitQuiet(ctx)
}

func (s *mirrorStream) waitQuiet(ctx context.Context) (bool, error) {
	start := time.Now()
	for {
		s.mu.Lock()
		last, changed := s.lastMotion, s.changed
		s.mu.Unlock()
		after := start
		if last.After(after) {
			after = last
		}
		timer := time.NewTimer(time.Until(after.Add(350 * time.Millisecond)))
		select {
		case <-changed:
			timer.Stop()
		case <-timer.C:
			s.mu.Lock()
			quiet, err := s.lastMotion.Equal(last), s.err
			s.mu.Unlock()
			if err != nil {
				return false, err
			}
			if quiet {
				return true, nil
			}
		case <-s.done:
			timer.Stop()
			return false, s.Err()
		case <-ctx.Done():
			timer.Stop()
			return false, nil
		}
	}
}

func sampleImage(img image.Image) []uint8 {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	out := make([]uint8, 0, 48*48)
	// Exclude the clock and the system navigation bars.
	for y := h / 10; y < h*9/10; y += max(1, h/48) {
		for x := w / 20; x < w*19/20; x += max(1, w/48) {
			r, g, b, _ := img.At(x, y).RGBA()
			out = append(out, uint8((r+2*g+b)>>10))
		}
	}
	return out
}

func samePicture(a, b []uint8) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	changed := 0
	for i := range a {
		if abs(int(a[i])-int(b[i])) > 12 {
			changed++
		}
	}
	return changed*200 < len(a)
}

var _ driver.Settler = (*device)(nil)
