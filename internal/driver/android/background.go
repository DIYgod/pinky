package android

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/companion"
	"github.com/DIYgod/pinky/internal/driver/android/scrcpy"
	"github.com/DIYgod/pinky/internal/screen"
)

// DisplayOptions configure a virtual display for background work.
type DisplayOptions struct {
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

	mu       sync.Mutex
	tapPoint screen.Point // the last tap, for typing into the field under it
	tapAt    time.Time
	// theirs holds the tasks that were elsewhere before an app started on
	// the display: an app the person has open moves its task here when
	// the agent starts it. Close moves those back to the main display and
	// lets the display destroy only what the agent opened. Nil when the
	// display keeps its content anyway.
	theirs    map[int]bool
	closeOnce sync.Once
	// started is set once an app was started on the display. Before that
	// the display has no window, which accessibility reports as it does a
	// display it cannot see, so Observe shows it as empty.
	started atomic.Bool
}

// alwaysUnlocked reports whether scrcpy's virtual displays ignore the
// phone's lock: from Android 13 it creates them always unlocked and in
// their own display group, so they keep running, drawing and taking input
// while the phone's own screen is off and locked (checked on the Android 16
// emulator). Older versions have neither flag.
func (d *Device) alwaysUnlocked() bool { return d.props.SDK >= 33 }

// BackgroundByDefault implements driver.BackgroundDefaulter: from Android
// 13 a background display keeps working while the phone is off and locked,
// so agents use one unless the person turns it off.
func (d *Device) BackgroundByDefault() bool { return d.alwaysUnlocked() }

// OpenBackground implements driver.Backgrounder with a scrcpy virtual
// display at the main display's size, without system decorations.
func (d *Device) OpenBackground(ctx context.Context, opt driver.BackgroundOptions) (driver.Device, error) {
	b, err := d.OpenDisplay(ctx, DisplayOptions{App: opt.App})
	if err != nil {
		return nil, err
	}
	return b, nil
}

// OpenDisplay creates a virtual display and, if opt.App is set, starts the
// app on it.
func (d *Device) OpenDisplay(ctx context.Context, opt DisplayOptions) (*Background, error) {
	if d.props.SDK > 0 && d.props.SDK < 29 {
		return nil, fmt.Errorf("android: virtual displays need Android 10: %w", driver.ErrUnsupported)
	}
	d.mu.Lock()
	broken := d.noVideo
	d.mu.Unlock()
	if broken != "" {
		return nil, fmt.Errorf("android: the phone's video encoder fails (%s), and a virtual display needs it: %w", broken, driver.ErrUnsupported)
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
	}, nil)
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
	if !opt.KeepContent {
		b.theirs = map[int]bool{}
	}
	if pkg != "" {
		if err := b.startApp(ctx, pkg); err != nil {
			l.close(err)
			return nil, err
		}
	}
	return b, nil
}

// rootTasks lists the phone's root tasks and their displays.
func (d *Device) rootTasks(ctx context.Context) ([]rootTask, error) {
	out, err := d.client.Shell(ctx, d.info.ID, "cmd activity stack list")
	if err != nil {
		return nil, wrapErr(err)
	}
	return parseRootTasks(string(out)), nil
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
		if !b.started.Load() && errors.Is(err, driver.ErrUnsupported) && ctx.Err() == nil {
			return b.emptyScreen(ctx)
		}
		if ctx.Err() == nil {
			b.d.companionFailed(err)
		}
		return nil, err
	}
	s := t.Screen(companion.ScreenOptions{})
	s.CapturedAt = c.HostTime(t.Time)
	s.App.Name = b.d.companionAppName(s.App)
	// The phone's lock screen is on the main display; from Android 13 the
	// virtual display is always unlocked.
	s.Locked = t.Locked && !b.d.alwaysUnlocked()
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
	b.d.used()
	hold := tapHold
	if opt.LongPress {
		hold = b.d.longPressHold(ctx)
	}
	err := b.l.input(func() error { return b.l.tap(ctx, p, hold) })
	if err == nil {
		b.mu.Lock()
		b.tapPoint, b.tapAt = p, time.Now()
		b.mu.Unlock()
	}
	return err
}

// recentTap returns the last tap's point if it was in the last minute.
func (b *Background) recentTap() (screen.Point, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tapAt.IsZero() || time.Since(b.tapAt) > time.Minute {
		return screen.Point{}, false
	}
	return b.tapPoint, true
}

// Swipe implements driver.Device.
func (b *Background) Swipe(ctx context.Context, from, to screen.Point, dur time.Duration) error {
	b.d.used()
	return b.l.input(func() error { return b.l.swipe(ctx, from, to, dur) })
}

// Touch implements driver.Toucher.
func (b *Background) Touch(ctx context.Context, a driver.TouchAction, p screen.Point) error {
	return b.l.input(func() error { return b.l.touch(ctx, a, p) })
}

// Type implements driver.Device. With a companion that reads other
// displays (Android 11 and later) the text goes in with ACTION_SET_TEXT
// on this display, as on the main one; otherwise as key events or a
// paste.
func (b *Background) Type(ctx context.Context, text string, opt driver.TypeOptions) error {
	b.d.used()
	if c := b.d.companionClient(ctx); c != nil && b.d.comp.displays() {
		enter := func() error { return b.l.input(func() error { return b.l.key(keycodeEnter, 0) }) }
		if done, err := b.d.typeCompanion(ctx, c, text, opt.Append, opt.Submit, b.display, b.recentTap, enter); done {
			return err
		}
	}
	keys := plainASCII(text) && b.d.keysReachField(ctx)
	return b.l.input(func() error { return b.l.typeText(ctx, text, opt, keys) })
}

// Key implements driver.Device.
func (b *Background) Key(ctx context.Context, k driver.Key) error {
	code, ok := keycodes[k]
	if !ok {
		return fmt.Errorf("android: key %q: %w", k, driver.ErrUnsupported)
	}
	return b.l.input(func() error { return b.l.key(code, 0) })
}

// Launch implements driver.Device: the app starts on the virtual display.
func (b *Background) Launch(ctx context.Context, app string) error {
	pkg, err := b.d.resolveApp(ctx, app)
	if err != nil {
		return err
	}
	return b.startApp(ctx, pkg)
}

// startApp starts an app on the display, noting first which tasks are
// elsewhere, so that one the start moves here can be given back.
func (b *Background) startApp(ctx context.Context, pkg string) error {
	if b.theirs != nil {
		roots, err := b.d.rootTasks(ctx)
		if err != nil {
			return err
		}
		b.mu.Lock()
		for _, r := range roots {
			if r.display != b.display {
				for _, t := range r.tasks {
					b.theirs[t] = true
				}
			}
		}
		b.mu.Unlock()
	}
	if err := b.l.startApp(ctx, pkg); err != nil {
		return err
	}
	b.started.Store(true)
	return nil
}

// emptyScreen is the display before an app was started on it: nothing to
// read, in the display's size.
func (b *Background) emptyScreen(ctx context.Context) (*screen.Screen, error) {
	w, h := b.w, b.h
	if w <= 0 || h <= 0 {
		var err error
		if w, h, err = b.l.videoSize(ctx); err != nil {
			return nil, err
		}
	}
	o := "portrait"
	if w > h {
		o = "landscape"
	}
	return &screen.Screen{Width: w, Height: h, Orientation: o, CapturedAt: time.Now()}, nil
}

// returnTheirs moves the root tasks on the display that hold one of the
// person's tasks back to the main display, before the display closes and
// destroys the rest.
func (b *Background) returnTheirs() {
	b.mu.Lock()
	theirs := b.theirs
	b.mu.Unlock()
	if len(theirs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	roots, err := b.d.rootTasks(ctx)
	if err != nil {
		b.d.cfg.logf("android: %s: listing tasks before closing display %d: %v", b.d.info.ID, b.display, err)
		return
	}
	for _, r := range roots {
		if r.display != b.display || !slices.ContainsFunc(r.tasks, func(t int) bool { return theirs[t] }) {
			continue
		}
		if _, err := b.d.client.Shell(ctx, b.d.info.ID, fmt.Sprintf("cmd activity display move-stack %d 0", r.id)); err != nil {
			b.d.cfg.logf("android: %s: moving task %d back to the main display: %v", b.d.info.ID, r.id, err)
			continue
		}
		b.d.cfg.logf("android: %s: moved the person's task %d back to the main display", b.d.info.ID, r.id)
	}
}

// Apps implements driver.Device.
func (b *Background) Apps(ctx context.Context) ([]driver.App, error) { return b.d.Apps(ctx) }

// Stream implements driver.Device. The display's video size is fixed when
// it is created, so opt is ignored.
func (b *Background) Stream(ctx context.Context, _ driver.StreamOptions) (driver.Stream, error) {
	return b.l.attach(ctx)
}

// WaitSettled implements driver.Settler for the virtual display: its
// video, and the companion's events on it while the companion is on.
func (b *Background) WaitSettled(ctx context.Context, max time.Duration) (bool, error) {
	b.d.used()
	var extra []quietCond
	if c, ok := b.d.settleCond(b.display); ok {
		extra = append(extra, c)
	}
	return b.l.waitSettled(ctx, max, extra...)
}

// Locked implements driver.LockReporter. From Android 13 the virtual
// display is always unlocked, so an agent keeps working while the phone is
// locked; before, the phone's lock applies.
func (b *Background) Locked(ctx context.Context) (bool, error) {
	if b.d.alwaysUnlocked() {
		return false, nil
	}
	return b.d.Locked(ctx)
}

// ListFiles implements driver.Files: the phone's Pinky folder.
func (b *Background) ListFiles(ctx context.Context) ([]driver.FileInfo, error) {
	return b.d.ListFiles(ctx)
}

// Pull implements driver.Files.
func (b *Background) Pull(ctx context.Context, path string, w io.Writer) error {
	return b.d.Pull(ctx, path, w)
}

// Push implements driver.Files.
func (b *Background) Push(ctx context.Context, path string, r io.Reader, size int64, mtime time.Time) error {
	return b.d.Push(ctx, path, r, size, mtime)
}

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
	b.closeOnce.Do(func() {
		if !b.l.ended() {
			b.returnTheirs()
		}
		b.l.close(fmt.Errorf("android: background display closed"))
	})
	return nil
}

var (
	_ driver.Device       = (*Background)(nil)
	_ driver.Toucher      = (*Background)(nil)
	_ driver.LockReporter = (*Background)(nil)
	_ driver.Clipboard    = (*Background)(nil)
	_ driver.Settler      = (*Background)(nil)
	_ driver.Files        = (*Background)(nil)
	_ driver.Backgrounder = (*Device)(nil)
)
