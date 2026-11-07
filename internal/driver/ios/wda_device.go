package ios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
	goios "github.com/danielpaulus/go-ios/ios"
)

// WDADevice drives an iPhone through the WebDriverAgent helper: iOS 17.4 to
// 26, where the phone refuses CoreDevice's screen stream and HID input.
// Coordinates are screenshot pixels, as on the other paths; WDA works in
// points, so every point is divided by the screen's scale on the way in and
// multiplied on the way out.
type WDADevice struct {
	info  driver.Info
	c     *wdaClient
	entry goios.DeviceEntry // the usbmuxd entry; zero for a simulator
	run   *wdaRun           // the test run hosting WDA, when Pinky started it
	l     *link             // the tunnel the run goes through, from iOS 17.4

	scale         float64
	width, height int // pixels

	inputMu sync.Mutex // one gesture or text entry at a time
	appMu   sync.Mutex
	apps    *appCatalog
	rec     touchRecorder

	videoMu        sync.Mutex
	videoW, videoH int // the stream's picture, for the mirror's touches

	closeOnce sync.Once

	imageDir string // Pinky's folder for developer disk images (checkup.go)
}

// openWDA connects to a WDA server that is running and reads the screen's
// size. run, when not nil, is the test run that hosts it and is stopped on
// Close.
func openWDA(ctx context.Context, info driver.Info, c *wdaClient, entry goios.DeviceEntry, run *wdaRun) (*WDADevice, error) {
	st, err := c.waitReady(ctx)
	if err != nil {
		return nil, err
	}
	scr, err := c.screenInfo(ctx)
	if err != nil {
		return nil, err
	}
	d := &WDADevice{
		info: info, c: c, entry: entry, run: run,
		scale:  scr.Scale,
		width:  int(math.Round(scr.ScreenSize.Width * scr.Scale)),
		height: int(math.Round(scr.ScreenSize.Height * scr.Scale)),
	}
	// Screenshots are the coordinate space, and XCTest's can be a pixel
	// narrower than points times scale (1178 for 393 points at 3x).
	if b, err := c.screenshot(ctx); err == nil {
		if cfg, err := png.DecodeConfig(bytes.NewReader(b)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
			d.width, d.height = cfg.Width, cfg.Height
		}
	}
	d.info.Backend = backendWDA
	if d.info.OSVersion == "" {
		d.info.OSVersion = st.OS.Version
	}
	return d, nil
}

// wdaStartTimeout bounds starting the runner: the first start after the
// runner was installed takes the longest (10 to 20 s is common).
const wdaStartTimeout = 60 * time.Second

// OpenWDA opens a phone through its WebDriverAgent helper, starting the
// helper's test when it is not running yet.
func OpenWDA(ctx context.Context, info driver.Info) (*WDADevice, error) {
	s, err := startWDAFor(ctx, info)
	if err != nil {
		return nil, err
	}
	d, err := s.open(ctx, info)
	if err != nil {
		s.stop()
		return nil, err
	}
	return d, nil
}

// errAutomation is the phone waiting for its passcode: now and then iOS
// asks for it on the phone before a UI test runs, so before the helper can
// control the phone (see runnerLog). A passcode entered after the runner
// gave up still counts for the next start.
var errAutomation = errors.New("the iPhone asks on its screen for its passcode to allow automation")

// wdaStart is WebDriverAgent starting on a phone: the client that reaches
// it and, when Pinky started it, the runner's test run and the tunnel the
// run goes through.
type wdaStart struct {
	base goios.DeviceEntry
	c    *wdaClient
	run  *wdaRun
	l    *link
}

// startWDAFor starts the helper's test on a phone, unless WebDriverAgent
// already answers there (started by Xcode, or by an earlier Pinky).
func startWDAFor(ctx context.Context, info driver.Info) (*wdaStart, error) {
	base, err := findEntry(info.ID, info.Connection == "wifi")
	if err != nil {
		return nil, err
	}
	s := &wdaStart{base: base, c: newWDAUSB(base)}
	if s.ready(ctx) {
		return s, nil
	}
	bundleID, err := findWDARunner(base)
	if err != nil {
		return nil, err
	}
	if bundleID == "" {
		return nil, fmt.Errorf("the WebDriverAgent helper is not installed on %s: %w", info.ID, driver.ErrNotFound)
	}
	l, err := dialLinkEntry(ctx, base)
	if err != nil {
		return nil, err
	}
	s.run, s.l = startWDA(l.entry, bundleID), l
	return s, nil
}

// ready reports whether WebDriverAgent answers and is ready.
func (s *wdaStart) ready(ctx context.Context) bool {
	sctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	st, err := s.c.status(sctx)
	return err == nil && st.Ready
}

// running reports whether the start can still succeed: WebDriverAgent was
// running already, or the runner's test is still going.
func (s *wdaStart) running() bool {
	if s.run == nil {
		return true
	}
	alive, _ := s.run.alive()
	return alive
}

// open waits for WebDriverAgent to answer and opens the phone through it;
// the device then owns the run and the tunnel. It stops waiting when the
// run ends, and returns errAutomation as soon as the phone asks for its
// passcode, leaving the start to the caller, to stop or keep.
func (s *wdaStart) open(ctx context.Context, info driver.Info) (*WDADevice, error) {
	if s.run == nil {
		return openWDA(ctx, info, s.c, s.base, nil)
	}
	wctx, cancel := context.WithTimeout(ctx, wdaStartTimeout)
	defer cancel()
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-s.run.done:
				cancel()
				return
			case <-wctx.Done():
				return
			case <-t.C:
				if s.run.asking() {
					cancel()
					return
				}
			}
		}
	}()
	d, err := openWDA(wctx, info, s.c, s.base, s.run)
	if err != nil {
		if alive, rerr := s.run.alive(); !alive && rerr != nil {
			err = fmt.Errorf("start WebDriverAgent: %w", rerr)
		} else if alive && s.run.asking() {
			err = errAutomation
		}
		return nil, err
	}
	d.l = s.l
	return d, nil
}

// stop ends what the start began.
func (s *wdaStart) stop() {
	if s.run != nil {
		s.run.stop()
	}
	if s.l != nil {
		s.l.Close()
	}
}

func OpenWDAURL(ctx context.Context, base, mjpeg string) (*WDADevice, error) {
	return openWDA(ctx, driver.Info{ID: base, Platform: driver.IOS, Name: "WebDriverAgent", Connection: "usb", State: driver.StateOnline}, newWDAURL(base, mjpeg), goios.DeviceEntry{}, nil)
}

// Info implements driver.Device.
func (d *WDADevice) Info() driver.Info { return d.info }

// toPoints converts screenshot pixels to WDA's points.
func (d *WDADevice) toPoints(p screen.Point) wdaPoint {
	x := math.Max(0, math.Min(float64(p.X), float64(d.width-1)))
	y := math.Max(0, math.Min(float64(p.Y), float64(d.height-1)))
	return wdaPoint{X: (x + 0.5) / d.scale, Y: (y + 0.5) / d.scale}
}

// Observe implements driver.Device: WDA's page source, with frames.
func (d *WDADevice) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	var (
		wg      sync.WaitGroup
		root    *wdaNode
		srcErr  error
		app     wdaApp
		locked  bool
		lines   []ocrLine
		ocrDone bool
	)
	wg.Add(3)
	go func() { defer wg.Done(); root, srcErr = d.c.source(ctx) }()
	go func() { defer wg.Done(); app, _ = d.c.activeApp(ctx) }()
	go func() { defer wg.Done(); locked, _ = d.lockState(ctx) }()
	if opt.OCR {
		wg.Add(1)
		go func() {
			defer wg.Done()
			png, err := d.c.screenshot(ctx)
			if err != nil {
				return
			}
			if l, err := recognizeText(png, ocrOptions{Languages: ocrLanguages}); err == nil {
				lines, ocrDone = l, true
			}
		}()
	}
	wg.Wait()
	if srcErr != nil {
		return nil, fmt.Errorf("observe: %w", srcErr)
	}
	s := &screen.Screen{
		Width: d.width, Height: d.height,
		Orientation: "portrait",
		Locked:      locked,
		CapturedAt:  time.Now(),
	}
	s.App = d.appInfo(ctx, app, root)
	scale := d.scale
	if root.Rect.Width > 0 {
		scale = float64(d.width) / root.Rect.Width
		if root.Rect.Width > root.Rect.Height {
			s.Orientation = "landscape"
		}
	}
	s.Elements, s.Keyboard = wdaScreenElements(root, scale, d.width, d.height, s.App.ID)
	if ocrDone && len(lines) > 0 {
		if top, kb := keyboardFromOCR(lines); kb {
			s.Keyboard = true
			kept := lines[:0:0]
			for _, l := range lines {
				if l.Y < top {
					kept = append(kept, l)
				}
			}
			lines = kept
		}
		s.Elements = append(s.Elements, ocrExtras(s.Elements, lines, d.width, d.height)...)
	}
	return s, nil
}

// appInfo names the app in front: WDA's active app, with the name from the
// app list (WDA leaves SpringBoard's empty).
func (d *WDADevice) appInfo(ctx context.Context, a wdaApp, root *wdaNode) screen.App {
	if a.BundleID == springBoard {
		return screen.App{Name: "Home Screen", ID: springBoard}
	}
	out := screen.App{ID: a.BundleID, Name: a.Name}
	if out.Name == "" && root != nil {
		out.Name = strings.TrimSpace(strOr(root.Label))
	}
	if out.Name == "" && a.BundleID != "" {
		if apps, err := d.Apps(ctx); err == nil {
			for _, x := range apps {
				if x.ID == a.BundleID {
					out.Name = x.Name
				}
			}
		}
	}
	return out
}

// Screenshot implements driver.Device.
func (d *WDADevice) Screenshot(ctx context.Context) (image.Image, error) {
	b, err := d.c.screenshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("screenshot: %w", err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("screenshot: decode: %w", err)
	}
	return img, nil
}

// Tap implements driver.Device.
func (d *WDADevice) Tap(ctx context.Context, p screen.Point, opt driver.TapOptions) error {
	hold := 60 * time.Millisecond
	if opt.LongPress {
		hold = 800 * time.Millisecond
	}
	d.inputMu.Lock()
	defer d.inputMu.Unlock()
	return d.c.gesture(ctx, []wdaPoint{d.toPoints(p)}, 0, hold)
}

// Swipe implements driver.Device.
func (d *WDADevice) Swipe(ctx context.Context, from, to screen.Point, dur time.Duration) error {
	if dur <= 0 {
		dur = 300 * time.Millisecond
	}
	d.inputMu.Lock()
	defer d.inputMu.Unlock()
	return d.c.gesture(ctx, []wdaPoint{d.toPoints(from), d.toPoints(to)}, dur, 0)
}

// Type implements driver.Device. XCTest types any text, CJK and emoji
// included, into the field with the keyboard focus.
func (d *WDADevice) Type(ctx context.Context, text string, opt driver.TypeOptions) error {
	d.inputMu.Lock()
	defer d.inputMu.Unlock()
	if !opt.Append {
		if err := d.c.clearFocused(ctx); err != nil {
			if errors.Is(err, errNoFocus) {
				return fmt.Errorf("type: %v: %w", err, driver.ErrNotFound)
			}
			return fmt.Errorf("type: %w", err)
		}
	}
	if text != "" {
		if err := d.c.keys(ctx, text); err != nil {
			return fmt.Errorf("type: %w", err)
		}
	}
	if opt.Submit {
		if err := d.c.keys(ctx, "\n"); err != nil {
			return fmt.Errorf("type: submit: %w", err)
		}
	}
	return nil
}

// wdaKeys are the keys WDA types as characters (XCUIKeyboardKey values).
var wdaKeys = map[driver.Key]string{
	driver.KeyEnter:         "\n",
	driver.KeyTab:           "\t",
	driver.KeyDelete:        "\b",
	driver.KeyForwardDelete: "\u007f",
	driver.KeyEscape:        "\u001b",
}

// Key implements driver.Device. Home is XCTest's Home button; Back is a
// swipe from the left edge, as on the other path.
func (d *WDADevice) Key(ctx context.Context, k driver.Key) error {
	switch k {
	case driver.KeyHome:
		d.inputMu.Lock()
		defer d.inputMu.Unlock()
		return d.c.home(ctx)
	case driver.KeyBack:
		return d.back(ctx)
	case driver.KeyRecents:
		return fmt.Errorf("recents on iPhone: %w", driver.ErrUnsupported)
	}
	s, ok := wdaKeys[k]
	if !ok {
		return fmt.Errorf("key %q: %w", k, driver.ErrUnsupported)
	}
	d.inputMu.Lock()
	defer d.inputMu.Unlock()
	return d.c.keys(ctx, s)
}

// back taps the navigation bar's back button when the screen has one, and
// otherwise swipes from the left edge. XCTest's synthesized touches do not
// always start the system's edge swipe (not on the iOS 18 Simulator), so
// the button comes first.
func (d *WDADevice) back(ctx context.Context) error {
	if root, err := d.c.source(ctx); err == nil {
		if p, ok := backButton(root, d.scale); ok {
			return d.Tap(ctx, p, driver.TapOptions{})
		}
	}
	y := d.height / 2
	d.inputMu.Lock()
	defer d.inputMu.Unlock()
	from, to := d.toPoints(screen.Point{X: 0, Y: y}), d.toPoints(screen.Point{X: d.width * 7 / 10, Y: y})
	from.X = 0
	return d.c.gesturePath(ctx, []wdaPoint{from, from, to}, []time.Duration{0, 50 * time.Millisecond, 250 * time.Millisecond}, 0)
}

// backButton finds a navigation bar's back button: the bar's first button,
// in the left third of the screen.
func backButton(root *wdaNode, scale float64) (screen.Point, bool) {
	var bar *wdaNode
	var find func(n *wdaNode, depth int)
	find = func(n *wdaNode, depth int) {
		if bar != nil || depth > 60 {
			return
		}
		if n.Type == "NavigationBar" && bool(n.Visible) {
			bar = n
			return
		}
		for i := range n.Children {
			find(&n.Children[i], depth+1)
		}
	}
	find(root, 0)
	if bar == nil {
		return screen.Point{}, false
	}
	third := root.Rect.Width / 3
	var btn *wdaNode
	var first func(n *wdaNode, depth int)
	first = func(n *wdaNode, depth int) {
		if btn != nil || depth > 10 {
			return
		}
		if n.Type == "Button" && bool(n.Visible) && n.Rect.Width > 0 && n.Rect.X < third {
			btn = n
			return
		}
		for i := range n.Children {
			first(&n.Children[i], depth+1)
		}
	}
	first(bar, 0)
	if btn == nil {
		return screen.Point{}, false
	}
	return screen.Point{
		X: int(math.Round((btn.Rect.X + btn.Rect.Width/2) * scale)),
		Y: int(math.Round((btn.Rect.Y + btn.Rect.Height/2) * scale)),
	}, true
}

// Launch implements driver.Device. A running app comes to the front without
// restarting.
func (d *WDADevice) Launch(ctx context.Context, app string) error {
	id, err := d.resolveApp(ctx, app)
	if err != nil {
		return err
	}
	if err := d.c.activate(ctx, id); err != nil {
		return fmt.Errorf("launch %s: %w", id, err)
	}
	return nil
}

// resolveApp turns a display name into a bundle id through the app list.
// Without one (a simulator), the name is taken as a bundle id.
func (d *WDADevice) resolveApp(ctx context.Context, app string) (string, error) {
	app = strings.TrimSpace(app)
	if app == "" {
		return "", fmt.Errorf("no app given: %w", driver.ErrNotFound)
	}
	if d.entry.DeviceID == 0 {
		return app, nil
	}
	apps, err := d.Apps(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range apps {
		if a.ID == app {
			return a.ID, nil
		}
	}
	for _, a := range apps {
		if strings.EqualFold(a.Name, app) || strings.EqualFold(a.ID, app) {
			return a.ID, nil
		}
	}
	return "", fmt.Errorf("app %q: %w", app, driver.ErrNotFound)
}

// Apps implements driver.Device, through the installation proxy.
func (d *WDADevice) Apps(ctx context.Context) ([]driver.App, error) {
	if d.entry.DeviceID == 0 {
		return nil, fmt.Errorf("app list: %w", driver.ErrUnsupported)
	}
	d.appMu.Lock()
	if d.apps == nil {
		d.apps = &appCatalog{}
	}
	c := d.apps
	d.appMu.Unlock()
	c.mu.Lock()
	fresh := c.apps != nil && time.Since(c.at) < appCacheTTL
	apps := append([]driver.App(nil), c.apps...)
	c.mu.Unlock()
	if fresh {
		return apps, nil
	}
	type result struct {
		apps   []driver.App
		byPath map[string]driver.App
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		apps, byPath, err := browseApps(&link{base: d.entry})
		ch <- result{apps, byPath, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		c.mu.Lock()
		c.apps, c.byPath, c.at = r.apps, r.byPath, time.Now()
		c.mu.Unlock()
		return append([]driver.App(nil), r.apps...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Locked implements driver.LockReporter: whether the phone needs its
// passcode, from lockdown as on the CoreDevice path.
func (d *WDADevice) Locked(ctx context.Context) (bool, error) {
	return d.lockState(ctx)
}

// Stream implements driver.Device: WDA's MJPEG picture, encoded to H.264.
// The picture is the screen at half size (wdaSettings).
func (d *WDADevice) Stream(ctx context.Context, opt driver.StreamOptions) (driver.Stream, error) {
	if _, err := d.c.session(ctx); err != nil { // applies the MJPEG settings
		return nil, err
	}
	s, err := startFrameStream(ctx, func(ctx context.Context) (frameSource, error) {
		return openMJPEG(ctx, d.c.long, d.c.mjpeg)
	}, d.width, d.height)
	if err != nil {
		return nil, err
	}
	w, h := s.Size()
	d.videoMu.Lock()
	d.videoW, d.videoH = w, h
	d.videoMu.Unlock()
	return s, nil
}

// videoSize is the size of the last stream's picture, or 0, 0.
func (d *WDADevice) videoSize() (int, int) {
	d.videoMu.Lock()
	defer d.videoMu.Unlock()
	return d.videoW, d.videoH
}

// Close implements driver.Device. It ends the WebDriver session and stops
// the test run Pinky started; WDA itself stays installed.
func (d *WDADevice) Close() error {
	d.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		d.c.deleteSession(ctx)
		cancel()
		if d.run != nil {
			d.run.stop()
		}
		if d.l != nil {
			d.l.Close()
		}
	})
	return nil
}

var (
	_ driver.Device       = (*WDADevice)(nil)
	_ driver.LockReporter = (*WDADevice)(nil)
	_ driver.Settler      = (*WDADevice)(nil)
)
