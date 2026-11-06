package android

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"strconv"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/companion"
	"github.com/DIYgod/pinky/internal/driver/android/scrcpy"
	"github.com/DIYgod/pinky/internal/screen"
)

// BackgroundOptions configure a virtual display for background work.
type BackgroundOptions struct {
	// App is started on the display once it exists: a package or a
	// display name, as for Launch. Empty starts nothing.
	App string
	// Width, Height and DPI of the display; zero means the main display's.
	Width, Height, DPI int
	// KeepContent moves the display's apps to the main display when the
	// background session closes, instead of closing them.
	KeepContent bool
	// SystemDecorations shows the status bar, the taskbar and a launcher
	// on the display. Off by default: the app fills the display, and its
	// layout does not shift when a taskbar appears and hides.
	SystemDecorations bool
	// MaxSize and MaxFPS tune the display's video stream.
	MaxSize, MaxFPS int
}

// Background is a scrcpy virtual display on the phone, so an agent can
// work in an app while the main screen stays free for the person. It has
// its own scrcpy session, video and input, and implements driver.Device.
//
// Limits: Observe needs the companion app, since uiautomator dumps only
// the main display. Screenshot returns driver.ErrUnsupported, since
// screencap rejects virtual display ids; decode a frame from Stream
// instead. Some apps refuse secondary displays and open on the main
// display instead.
type Background struct {
	d       *Device
	l       *live
	display int
	w, h    int // display size when the stream is scaled; 0 = video size
}

// OpenBackground creates a virtual display and, if opt.App is set, starts
// the app on it.
func (d *Device) OpenBackground(ctx context.Context, opt BackgroundOptions) (*Background, error) {
	if d.props.SDK > 0 && d.props.SDK < 29 {
		return nil, fmt.Errorf("android: virtual displays need Android 10: %w", driver.ErrUnsupported)
	}
	var pkg string
	if opt.App != "" {
		var err error
		if pkg, err = d.resolveApp(ctx, opt.App); err != nil {
			return nil, err
		}
	}
	b := &Background{d: d}
	if opt.MaxSize > 0 {
		b.w, b.h = opt.Width, opt.Height
		if b.w == 0 || b.h == 0 {
			natW, natH, err := d.naturalSize(ctx)
			if err != nil {
				return nil, err
			}
			b.w, b.h = natW, natH
		}
	}
	sopt := scrcpy.Options{
		MaxSize:      opt.MaxSize,
		MaxFPS:       float64(opt.MaxFPS),
		VideoBitRate: d.cfg.VideoBitRate,
		Virtual: &scrcpy.VirtualDisplay{
			Width: opt.Width, Height: opt.Height, DPI: opt.DPI,
			KeepContent:         opt.KeepContent,
			NoSystemDecorations: !opt.SystemDecorations,
		},
		// Keep the soft keyboard on the virtual display, off the person's
		// main screen.
		DisplayIMEPolicy: "local",
	}
	l, err := startLive(ctx, &d.cfg, d.client, d.info.ID, sopt, func(vw, vh int) (int, int) {
		if b.w > 0 && b.h > 0 {
			return orient(b.w, b.h, vw, vh)
		}
		return vw, vh
	})
	if err != nil {
		return nil, wrapErr(err)
	}
	b.l = l
	idctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	b.display, err = l.s.DisplayID(idctx)
	cancel()
	if err != nil {
		l.close(err)
		return nil, fmt.Errorf("android: virtual display did not start: %w", err)
	}
	if pkg != "" {
		if err := l.startApp(ctx, pkg); err != nil {
			l.close(err)
			return nil, err
		}
	}
	return b, nil
}

// DisplayID returns the Android display id of the virtual display.
func (b *Background) DisplayID() int { return b.display }

// Info implements driver.Device.
func (b *Background) Info() driver.Info {
	info := b.d.info
	info.Backend = "scrcpy-virtual-display"
	return info
}

// Observe implements driver.Device through the companion app, which reads
// any display; uiautomator reads only the main one, so without the
// companion this returns driver.ErrUnsupported.
func (b *Background) Observe(ctx context.Context, _ driver.ObserveOptions) (*screen.Screen, error) {
	b.d.used()
	c := b.d.companionClient(ctx)
	if c == nil {
		return nil, fmt.Errorf("android: observing a virtual display needs the companion app: %w", driver.ErrUnsupported)
	}
	t, err := c.Dump(ctx, companion.DumpOptions{Display: b.display})
	if err != nil {
		if ctx.Err() == nil {
			b.d.companionFailed(err)
		}
		return nil, err
	}
	s := t.Screen(companion.ScreenOptions{})
	s.CapturedAt = c.HostTime(t.Time)
	s.App.Name = b.d.companionAppName(s.App)
	// scrcpy creates the display always unlocked; the phone's own lock
	// screen is on the main display.
	s.Locked = false
	return s, nil
}

// Screenshot implements driver.Device with `screencap -d`.
func (b *Background) Screenshot(ctx context.Context) (image.Image, error) {
	out, err := b.d.client.Exec(ctx, b.d.info.ID, "screencap -p -d "+strconv.Itoa(b.display))
	if err != nil {
		return nil, wrapErr(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("android: screencap of display %d: %q: %w", b.display, firstLine(out), driver.ErrUnsupported)
	}
	return img, nil
}

// Tap implements driver.Device.
func (b *Background) Tap(ctx context.Context, p screen.Point, opt driver.TapOptions) error {
	hold := tapHold
	if opt.LongPress {
		hold = b.d.longPressHold(ctx)
	}
	return b.l.tap(ctx, p, hold)
}

// Swipe implements driver.Device.
func (b *Background) Swipe(ctx context.Context, from, to screen.Point, dur time.Duration) error {
	return b.l.swipe(ctx, from, to, dur)
}

// Touch implements driver.Toucher.
func (b *Background) Touch(ctx context.Context, a driver.TouchAction, p screen.Point) error {
	return b.l.touch(ctx, a, p)
}

// Type implements driver.Device.
func (b *Background) Type(ctx context.Context, text string, opt driver.TypeOptions) error {
	return b.l.typeText(ctx, text, opt, plainASCII(text) && b.d.keysReachField(ctx))
}

// Key implements driver.Device.
func (b *Background) Key(ctx context.Context, k driver.Key) error {
	code, ok := keycodes[k]
	if !ok {
		return fmt.Errorf("android: key %q: %w", k, driver.ErrUnsupported)
	}
	return b.l.key(code, 0)
}

// Launch implements driver.Device: the app starts on the virtual display.
func (b *Background) Launch(ctx context.Context, app string) error {
	pkg, err := b.d.resolveApp(ctx, app)
	if err != nil {
		return err
	}
	return b.l.startApp(ctx, pkg)
}

// Apps implements driver.Device.
func (b *Background) Apps(ctx context.Context) ([]driver.App, error) { return b.d.Apps(ctx) }

// Stream implements driver.Device. The display's video size is fixed when
// it is created, so opt is ignored.
func (b *Background) Stream(ctx context.Context, _ driver.StreamOptions) (driver.Stream, error) {
	return b.l.attach(ctx)
}

// WaitSettled implements driver.Settler for the virtual display.
func (b *Background) WaitSettled(ctx context.Context, max time.Duration) (bool, error) {
	return b.l.waitSettled(ctx, max)
}

// Locked implements driver.LockReporter for the whole phone.
func (b *Background) Locked(ctx context.Context) (bool, error) { return b.d.Locked(ctx) }

// SetClipboard implements driver.Clipboard; the clipboard is shared.
func (b *Background) SetClipboard(ctx context.Context, text string) error {
	if err := b.l.s.SetClipboard(ctx, text, false); err != nil {
		return b.l.mapErr(err)
	}
	return nil
}

// Clipboard implements driver.Clipboard.
func (b *Background) Clipboard(ctx context.Context) (string, error) {
	text, err := b.l.s.Clipboard(ctx)
	if err != nil {
		return "", b.l.mapErr(err)
	}
	return text, nil
}

// VideoStats returns the display's video packets since t.
func (b *Background) VideoStats(ctx context.Context, since time.Time) (VideoStats, error) {
	w, h, err := b.l.videoSize(ctx)
	if err != nil {
		return VideoStats{}, err
	}
	return VideoStats{Width: w, Height: h, Codec: string(b.l.codec), SettleBytes: b.l.settle.bytes(), Packets: b.l.stats.since(since)}, nil
}

// Close implements driver.Device. Unless KeepContent was set, the apps on
// the display close with it.
func (b *Background) Close() error {
	b.l.close(fmt.Errorf("android: background display closed"))
	return nil
}

var (
	_ driver.Device       = (*Background)(nil)
	_ driver.Toucher      = (*Background)(nil)
	_ driver.LockReporter = (*Background)(nil)
	_ driver.Clipboard    = (*Background)(nil)
	_ driver.Settler      = (*Background)(nil)
)
