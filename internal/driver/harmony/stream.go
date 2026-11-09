package harmony

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/video"
)

// Stream mirrors still screenshots through Pinky's existing frame encoder.
// It needs the host ffmpeg command, uses no recording app on the phone,
// and never changes the device's power or screen-lock settings.
func (d *Device) Stream(ctx context.Context, opt driver.StreamOptions) (driver.Stream, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	first, err := d.capture(ctx)
	if err != nil {
		return nil, err
	}
	if d.cancel != nil {
		d.cancel()
	}
	sctx, cancel := context.WithCancel(ctx)
	fps := 2
	if opt.MaxFPS > 0 {
		fps = min(opt.MaxFPS, 2)
	}
	w, h := first.Bounds().Dx(), first.Bounds().Dy()
	blank := image.NewRGBA(image.Rect(0, 0, w, h))
	encoder, err := findFFmpeg()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("harmony: live preview needs ffmpeg: %w", driver.ErrUnsupported)
	}
	st, err := video.LowLatencyFrameStream(sctx, w, h, fps, encoder, func() image.Image {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.previewMu.Lock()
		if d.inputs > 0 || d.closed {
			d.previewMu.Unlock()
			first = nil
			return nil
		}
		captureCtx, captureCancel := context.WithCancel(sctx)
		d.previewCancel = captureCancel
		d.previewMu.Unlock()
		defer func() {
			captureCancel()
			d.previewMu.Lock()
			d.previewCancel = nil
			d.previewMu.Unlock()
		}()
		if first != nil {
			img := first
			first = nil
			return img
		}
		img, err := d.capturePreview(captureCtx, image.Pt(w, h))
		if captureCtx.Err() != nil {
			return nil // Input interrupted this optional frame.
		}
		if err != nil {
			if errors.Is(err, errDisplayChanged) {
				cancel()
				return nil
			}
			return blank
		}
		// Rotation requires a new stream with its new dimensions.
		if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
			cancel()
			return nil
		}
		return img
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("harmony: starting live preview: %v: %w", err, driver.ErrUnsupported)
	}
	d.cancel = cancel
	return st, nil
}

func (d *Device) Checkup(ctx context.Context) (driver.Checkup, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cu := driver.Checkup{Family: "huawei", System: d.info.OSVersion}
	out, err := d.shell(ctx, "uitest", "--version")
	if err != nil {
		return cu, err
	}
	cu.Checks = append(cu.Checks, driver.Check{ID: "harmony_control", Status: driver.CheckPass, Detail: "UITest " + string(out) + "; tap, swipe, navigation and app launch are available. Text injection is not supported yet."})
	status, detail := driver.CheckPass, "Screenshots and the screenshot-based live mirror are available (up to 2 fps)."
	fix := ""
	if _, err := findFFmpeg(); err != nil {
		status = driver.CheckWarn
		fix = "ffmpeg"
		detail = "Screenshots are available; the live mirror needs the host ffmpeg command."
	}
	if _, err := d.capture(ctx); err != nil {
		status, fix, detail = driver.CheckFail, driver.FixScreenshot, err.Error()
	}
	cu.Checks = append(cu.Checks, driver.Check{ID: driver.CheckScreen, Status: status, Fix: fix, Detail: detail})
	return cu, nil
}
