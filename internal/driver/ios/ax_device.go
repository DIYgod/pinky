package ios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/danielpaulus/go-ios/ios/nskeyedarchiver"
)

// AXDevice drives an iPhone on iOS 17.4 to 26 without anything installed on
// it. The phone refuses CoreDevice's screen stream and HID input below iOS
// 27, so this path has only the developer services that remain:
//
//   - the picture: screenshots from instruments, a few a second, encoded to
//     H.264 for the mirror;
//   - the elements: the accessibility inspector's walk, placed on the
//     screen by OCR (the inspector gives no frames);
//   - input: the inspector's Activate action on an element, which is what
//     VoiceOver's double tap does, so Tap presses the element it lands on.
//     There is no touch at a free point, no swipe, no Home and no typing;
//     those need the WebDriverAgent helper;
//   - apps: CoreDevice's app service launches them.
//
// It shares Device's services code through an inner Device that never
// starts the stream or HID.
type AXDevice struct {
	d *Device

	mu   sync.Mutex
	last []axTarget // what the last Observe found, for Tap
	at   time.Time
}

// axTarget is an element Tap can press: where it is and its handle.
type axTarget struct {
	bounds screen.Rect
	handle []byte
	label  string
}

// OpenAccessibility opens a phone on iOS 17.4 to 26 through its developer
// services alone.
func OpenAccessibility(ctx context.Context, info driver.Info) (*AXDevice, error) {
	l, err := dialLink(ctx, info.ID, info.Connection == "wifi")
	if err != nil {
		return nil, err
	}
	d := &Device{info: info, l: l}
	d.info.Backend = backendAccessibility
	png, err := d.ScreenshotPNG(ctx)
	if err != nil {
		l.Close()
		return nil, err
	}
	cfg, err := pngSize(png)
	if err != nil {
		l.Close()
		return nil, fmt.Errorf("screenshot: %w", err)
	}
	d.scr = screenInfo{Width: cfg.Width, Height: cfg.Height, Scale: 1, Orientation: "rot0"}
	if cfg.Width > cfg.Height {
		d.scr.Orientation = "rot90"
	}
	// Connect the accessibility daemon now; its first walk is slow.
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		d.axReady(c)
	}()
	return &AXDevice{d: d}, nil
}

func pngSize(b []byte) (image.Config, error) {
	return png.DecodeConfig(bytes.NewReader(b))
}

// Info implements driver.Device.
func (a *AXDevice) Info() driver.Info { return a.d.info }

// Observe implements driver.Device: the inspector's walk, placed by OCR.
func (a *AXDevice) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	s, from, _, err := a.d.observeInspectorNodes(ctx, opt, ObserveStats{})
	if err != nil {
		return nil, err
	}
	var last []axTarget
	for i, e := range s.Elements {
		if i < len(from) && from[i] != nil && !e.Bounds.Empty() {
			last = append(last, axTarget{bounds: e.Bounds, handle: from[i].handle, label: e.Label})
		}
	}
	a.mu.Lock()
	a.last, a.at = last, time.Now()
	a.mu.Unlock()
	return s, nil
}

// Screenshot implements driver.Device.
func (a *AXDevice) Screenshot(ctx context.Context) (image.Image, error) {
	return a.d.Screenshot(ctx)
}

// axTargetMaxAge is how long an Observe's elements stay pressable: the
// gateway observes before every action, and an old handle may point at an
// element that moved.
const axTargetMaxAge = 2 * time.Minute

// targetAt returns the element under p from the last Observe: the smallest
// one that contains it.
func targetAt(targets []axTarget, p screen.Point) (axTarget, bool) {
	var best axTarget
	found := false
	for _, t := range targets {
		if !t.bounds.Contains(p) {
			continue
		}
		if !found || t.bounds.W*t.bounds.H < best.bounds.W*best.bounds.H {
			best, found = t, true
		}
	}
	return best, found
}

// errNeedsHelper explains what this path cannot do.
func errNeedsHelper(what string) error {
	return fmt.Errorf("%s needs the WebDriverAgent helper on iPhones below iOS 27 (docs/notes/ios.md): %w", what, driver.ErrUnsupported)
}

// Tap implements driver.Device by pressing the element at p (the
// accessibility Activate action, as a VoiceOver double tap). A point with
// no element from the last Observe cannot be pressed.
func (a *AXDevice) Tap(ctx context.Context, p screen.Point, opt driver.TapOptions) error {
	if opt.LongPress {
		return errNeedsHelper("a long press")
	}
	a.mu.Lock()
	targets, at := a.last, a.at
	a.mu.Unlock()
	if time.Since(at) > axTargetMaxAge {
		targets = nil
	}
	t, ok := targetAt(targets, p)
	if !ok {
		return fmt.Errorf("nothing Pinky can press at %d,%d: without the WebDriverAgent helper it presses only elements of the last observation: %w", p.X, p.Y, driver.ErrNotFound)
	}
	ax, err := a.d.axReady(ctx)
	if err != nil {
		return err
	}
	a.d.markInput()
	return ax.activate(ctx, t.handle)
}

// Swipe implements driver.Device.
func (a *AXDevice) Swipe(ctx context.Context, from, to screen.Point, dur time.Duration) error {
	return errNeedsHelper("swiping")
}

// Type implements driver.Device.
func (a *AXDevice) Type(ctx context.Context, text string, opt driver.TypeOptions) error {
	return errNeedsHelper("typing")
}

// Key implements driver.Device.
func (a *AXDevice) Key(ctx context.Context, k driver.Key) error {
	if k == driver.KeyRecents {
		return fmt.Errorf("recents on iPhone: %w", driver.ErrUnsupported)
	}
	return errNeedsHelper(fmt.Sprintf("the %s key", k))
}

// Launch implements driver.Device, through CoreDevice's app service.
func (a *AXDevice) Launch(ctx context.Context, app string) error { return a.d.Launch(ctx, app) }

// Apps implements driver.Device.
func (a *AXDevice) Apps(ctx context.Context) ([]driver.App, error) { return a.d.Apps(ctx) }

// Locked implements driver.LockReporter.
func (a *AXDevice) Locked(ctx context.Context) (bool, error) { return a.d.Locked(ctx) }

// Stream implements driver.Device: screenshots, encoded to H.264.
func (a *AXDevice) Stream(ctx context.Context, opt driver.StreamOptions) (driver.Stream, error) {
	return startFrameStream(ctx, func(context.Context) (frameSource, error) {
		return &shotSource{d: a.d}, nil
	}, a.d.scr.Width, a.d.scr.Height)
}

// Close implements driver.Device.
func (a *AXDevice) Close() error { return a.d.Close() }

// shotSource yields screenshots as frames, as often as the phone takes
// them (0.35 to 0.75 s each over USB on an iPhone 13 mini), and less often
// while the screen stays the same.
type shotSource struct {
	d       *Device
	same    int
	lastKey uint64
}

const (
	shotGap     = 100 * time.Millisecond // between screenshots while the screen changes
	shotIdleGap = time.Second            // once it has not changed for a while
)

func (s *shotSource) next(ctx context.Context) (frame, error) {
	gap := shotGap
	if s.same >= 5 {
		gap = shotIdleGap
	}
	if err := sleepCtx(ctx, gap); err != nil {
		return frame{}, err
	}
	b, err := s.d.ScreenshotPNG(ctx)
	if err != nil {
		return frame{}, err
	}
	key := frameKey(b)
	if key == s.lastKey {
		s.same++
	} else {
		s.same, s.lastKey = 0, key
	}
	return frame{key: key, decode: func() (image.Image, error) { return png.Decode(bytes.NewReader(b)) }}, nil
}

func (s *shotSource) Close() error { return nil }

// activate performs an element's default action, as VoiceOver's double tap
// does (the inspector's "Activate", AXAction-2010).
func (a *axSession) activate(ctx context.Context, handle []byte) error {
	if len(handle) == 0 {
		return errors.New("accessibility: no element") // a null handle breaks the daemon's connection
	}
	pass := func(v any) any {
		return nskeyedarchiver.NewNSMutableDictionary(map[string]any{"ObjectType": "passthrough", "Value": v})
	}
	action := nskeyedarchiver.NewNSMutableDictionary(map[string]any{
		"ObjectType": "AXAuditElementAttribute_v1",
		"Value": pass(nskeyedarchiver.NewNSMutableDictionary(map[string]any{
			"AttributeNameValue_v1":     pass("AXAction-2010"),
			"HumanReadableNameValue_v1": pass("Activate"),
			"PerformsActionValue_v1":    pass(true),
			"SettableValue_v1":          pass(false),
			"DisplayAsTree_v1":          pass(false),
			"DisplayInlineValue_v1":     pass(false),
			"IsInternal_v1":             pass(false),
			"ValueTypeValue_v1":         pass(int32(1)),
		})),
	})
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := a.ch.MethodCallWithContext(c, "deviceElement:performAction:withValue:", elementArg(handle), action, nskeyedarchiver.NewNSMutableDictionary(map[string]any{})); err != nil {
		return fmt.Errorf("accessibility: activate: %w", err)
	}
	return nil
}

var (
	_ driver.Device       = (*AXDevice)(nil)
	_ driver.LockReporter = (*AXDevice)(nil)
)
