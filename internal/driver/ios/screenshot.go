package ios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"time"

	"github.com/danielpaulus/go-ios/ios/instruments"
)

const (
	screenCaptureService     = "com.apple.coredevice.screencaptureservice"
	featureCaptureScreenshot = "com.apple.coredevice.feature.capturescreenshot"
	actionCaptureScreenshot  = "com.apple.coredevice.action.capturescreenshot"
)

// Two services take screenshots, both as 1125x2436 PNGs on an iPhone 13 mini:
// the instruments one (DTServiceHub, what Xcode's Instruments uses) and
// CoreDevice's screen capture service, which encodes 16 bits per channel.
// Over USB a busy home screen takes about 0.6 s (5.4 MB) and 0.75 s
// (6.9 MB); a plain Settings page 0.35 s. Either service can get stuck on the
// device and stop answering until it restarts, so each call falls back to
// the other.

// ScreenshotPNG returns a PNG of the main display at the size UIKit renders
// (1125x2436 on an iPhone 13 mini), which is the driver's coordinate space.
func (d *Device) ScreenshotPNG(ctx context.Context) ([]byte, error) {
	d.svcMu.Lock()
	defer d.svcMu.Unlock()
	first, second := d.instrumentsShot, d.captureShot
	if d.preferCapture {
		first, second = second, first
	}
	b, err1 := first(ctx)
	if err1 == nil {
		return b, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("screenshot: %w", err1)
	}
	b, err2 := second(ctx)
	if err2 == nil {
		d.preferCapture = !d.preferCapture
		return b, nil
	}
	return nil, fmt.Errorf("screenshot: %w", errors.Join(err1, err2))
}

// instrumentsShot uses the instruments screenshot channel. Callers hold
// svcMu.
func (d *Device) instrumentsShot(ctx context.Context) ([]byte, error) {
	if d.inst == nil {
		type result struct {
			s   *instruments.ScreenshotService
			err error
		}
		ch := make(chan result, 1)
		go func() {
			s, err := instruments.NewScreenshotService(d.l.entry)
			ch <- result{s, err}
		}()
		select {
		case r := <-ch:
			if r.err != nil {
				return nil, fmt.Errorf("instruments: %w", r.err)
			}
			d.inst = r.s
		case <-time.After(dialTimeout):
			go func() {
				if r := <-ch; r.s != nil {
					r.s.Close()
				}
			}()
			return nil, fmt.Errorf("instruments: no answer in %v", dialTimeout)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	s := d.inst
	go func() {
		b, err := s.TakeScreenshot()
		ch <- result{b, err}
	}()
	var r result
	select {
	case r = <-ch:
	case <-ctx.Done():
		r.err = ctx.Err()
	}
	if r.err != nil || len(r.b) == 0 {
		// The connection's state is unknown now; start over next time.
		s.Close()
		d.inst = nil
		if r.err == nil {
			r.err = errors.New("empty image")
		}
		return nil, fmt.Errorf("instruments: %w", r.err)
	}
	return r.b, nil
}

// captureShot uses CoreDevice's screen capture service. Callers hold svcMu.
func (d *Device) captureShot(ctx context.Context) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		if d.shot == nil {
			svc, err := dialCoreDevice(d.l.entry, screenCaptureService)
			if err != nil {
				return nil, err
			}
			d.shot = svc
		}
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := d.shot.invoke(c, featureCaptureScreenshot, actionCaptureScreenshot, map[string]any{"requestedFormat": "png"})
		cancel()
		if err != nil {
			d.shot.Close()
			d.shot = nil
			if isClosedErr(err) && attempt == 0 && ctx.Err() == nil {
				continue // the service closes idle connections; reconnect once
			}
			return nil, err
		}
		img, _ := out["image"].([]byte)
		if len(img) == 0 {
			return nil, fmt.Errorf("%s: empty image", screenCaptureService)
		}
		return img, nil
	}
}

// Screenshot implements driver.Device.
func (d *Device) Screenshot(ctx context.Context) (image.Image, error) {
	b, err := d.ScreenshotPNG(ctx)
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("screenshot: decode: %w", err)
	}
	return img, nil
}
