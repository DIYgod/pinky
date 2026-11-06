package android

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/scrcpy"
	"github.com/DIYgod/pinky/internal/screen"
)

// Android key codes and meta states (android.view.KeyEvent).
const (
	keycodeHome       = 3
	keycodeBack       = 4
	keycodeDpadUp     = 19
	keycodeDpadDown   = 20
	keycodeDpadLeft   = 21
	keycodeDpadRight  = 22
	keycodeA          = 29
	keycodeTab        = 61
	keycodeEnter      = 66
	keycodeDel        = 67
	keycodePageUp     = 92
	keycodePageDown   = 93
	keycodeEscape     = 111
	keycodeForwardDel = 112
	keycodeAppSwitch  = 187
	keycodeWakeup     = 224

	metaCtrlOn     = 0x1000
	metaCtrlLeftOn = 0x2000
)

var keycodes = map[driver.Key]int32{
	driver.KeyBack:          keycodeBack,
	driver.KeyHome:          keycodeHome,
	driver.KeyRecents:       keycodeAppSwitch,
	driver.KeyEnter:         keycodeEnter,
	driver.KeyTab:           keycodeTab,
	driver.KeyDelete:        keycodeDel,
	driver.KeyForwardDelete: keycodeForwardDel,
	driver.KeyEscape:        keycodeEscape,
	driver.KeyDpadUp:        keycodeDpadUp,
	driver.KeyDpadDown:      keycodeDpadDown,
	driver.KeyDpadLeft:      keycodeDpadLeft,
	driver.KeyDpadRight:     keycodeDpadRight,
	driver.KeyPageUp:        keycodePageUp,
	driver.KeyPageDown:      keycodePageDown,
}

// Touch timing.
const (
	tapHold      = 50 * time.Millisecond  // finger down time of a tap
	swipeStep    = 8 * time.Millisecond   // interval between move events
	defaultSwipe = 300 * time.Millisecond // swipe duration when none is given
	awakeTrust   = 10 * time.Second       // how long a screen-on check holds
)

// inputSession returns the session for an action, waking the screen first
// if it is off. Waking uses KEYCODE_WAKEUP, which only ever turns the
// screen on, never off.
func (d *Device) inputSession(ctx context.Context) (*live, error) {
	l, err := d.session(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.wake(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

func (d *Device) wake(ctx context.Context, l *live) error {
	d.stateMu.Lock()
	fresh := time.Since(d.awakeAt) < awakeTrust
	d.stateMu.Unlock()
	if fresh {
		return nil
	}
	awake, err := d.screenOn(ctx)
	if err != nil {
		if errors.Is(err, driver.ErrOffline) || ctx.Err() != nil {
			return err
		}
		// A failed check should not block the action itself.
		d.cfg.logf("android: %s: screen state: %v", d.info.ID, err)
		return nil
	}
	if !awake {
		d.cfg.logf("android: %s: screen is off, waking it", d.info.ID)
		if err := l.key(keycodeWakeup, 0); err != nil {
			return err
		}
		// Give the display time to come on before the action lands.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if err := sleepCtx(ctx, 150*time.Millisecond); err != nil {
				return err
			}
			if on, err := d.screenOn(ctx); err == nil && on {
				break
			}
		}
		if err := sleepCtx(ctx, 250*time.Millisecond); err != nil {
			return err
		}
	}
	d.markAwake()
	return nil
}

func (d *Device) markAwake() {
	d.stateMu.Lock()
	d.awakeAt = time.Now()
	d.stateMu.Unlock()
}

// screenOn reports whether the screen is on (dozing counts as off).
func (d *Device) screenOn(ctx context.Context) (bool, error) {
	out, err := d.client.Shell(ctx, d.info.ID, "dumpsys power | grep mWakefulness=")
	if err != nil {
		return false, wrapErr(err)
	}
	awake, ok := parseAwake(string(out))
	if !ok {
		return true, nil // unknown: do not press anything
	}
	return awake, nil
}

// longPressHold returns how long to hold a long press: the phone's
// long-press timeout plus a margin, at least 800 ms.
func (d *Device) longPressHold(ctx context.Context) time.Duration {
	d.stateMu.Lock()
	lp := d.longPress
	d.stateMu.Unlock()
	if lp == 0 {
		lp = 400 * time.Millisecond
		if out, err := d.client.Shell(ctx, d.info.ID, "settings get secure long_press_timeout"); err == nil {
			if ms, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && ms > 0 {
				lp = time.Duration(ms) * time.Millisecond
			}
		}
		d.stateMu.Lock()
		d.longPress = lp
		d.stateMu.Unlock()
	}
	return max(lp+400*time.Millisecond, 800*time.Millisecond)
}

// Tap implements driver.Device.
func (d *Device) Tap(ctx context.Context, p screen.Point, opt driver.TapOptions) error {
	l, err := d.inputSession(ctx)
	if err != nil {
		return err
	}
	hold := tapHold
	if opt.LongPress {
		hold = d.longPressHold(ctx)
	}
	err = l.tap(ctx, p, hold)
	if err == nil {
		d.markAwake()
	}
	return err
}

// Swipe implements driver.Device.
func (d *Device) Swipe(ctx context.Context, from, to screen.Point, dur time.Duration) error {
	l, err := d.inputSession(ctx)
	if err != nil {
		return err
	}
	err = l.swipe(ctx, from, to, dur)
	if err == nil {
		d.markAwake()
	}
	return err
}

// Touch implements driver.Toucher, for the person's drags on the mirror.
func (d *Device) Touch(ctx context.Context, a driver.TouchAction, p screen.Point) error {
	var l *live
	var err error
	if a == driver.TouchDown {
		l, err = d.inputSession(ctx)
	} else {
		l, err = d.session(ctx)
	}
	if err != nil {
		return err
	}
	return l.touch(ctx, a, p)
}

// Key implements driver.Device.
func (d *Device) Key(ctx context.Context, k driver.Key) error {
	code, ok := keycodes[k]
	if !ok {
		return fmt.Errorf("android: key %q: %w", k, driver.ErrUnsupported)
	}
	l, err := d.inputSession(ctx)
	if err != nil {
		return err
	}
	return l.key(code, 0)
}

// Type implements driver.Device. Printable ASCII is typed as key events;
// anything else (CJK, emoji, newlines) is set as the clipboard and pasted,
// which replaces the clipboard's content. Without Append the field's text
// is selected first (Ctrl+A) and replaced.
func (d *Device) Type(ctx context.Context, text string, opt driver.TypeOptions) error {
	l, err := d.inputSession(ctx)
	if err != nil {
		return err
	}
	return l.typeText(ctx, text, opt)
}

// SetClipboard implements driver.Clipboard.
func (d *Device) SetClipboard(ctx context.Context, text string) error {
	l, err := d.session(ctx)
	if err != nil {
		return err
	}
	if err := l.s.SetClipboard(ctx, text, false); err != nil {
		return l.mapErr(err)
	}
	return nil
}

// Clipboard implements driver.Clipboard. An empty or non-text clipboard
// reads as "".
func (d *Device) Clipboard(ctx context.Context) (string, error) {
	l, err := d.session(ctx)
	if err != nil {
		return "", err
	}
	text, err := l.s.Clipboard(ctx)
	if err != nil {
		return "", l.mapErr(err)
	}
	return text, nil
}

// Input on a session, shared by the main display and background displays.

func (l *live) mapErr(err error) error {
	if l.ended() {
		return l.failure()
	}
	return err
}

func (l *live) tap(ctx context.Context, p screen.Point, hold time.Duration) error {
	pos, err := l.position(ctx, p)
	if err != nil {
		return err
	}
	if err := l.send(scrcpy.InjectTouch(scrcpy.ActionDown, scrcpy.PointerGenericFinger, pos, 1, 0, 0)); err != nil {
		return err
	}
	// Always lift the finger, even when ctx ends during the hold.
	werr := sleepCtx(ctx, hold)
	if err := l.send(scrcpy.InjectTouch(scrcpy.ActionUp, scrcpy.PointerGenericFinger, pos, 0, 0, 0)); err != nil {
		return err
	}
	return werr
}

func (l *live) swipe(ctx context.Context, from, to screen.Point, dur time.Duration) error {
	if dur <= 0 {
		dur = defaultSwipe
	}
	a, err := l.position(ctx, from)
	if err != nil {
		return err
	}
	b, err := l.position(ctx, to)
	if err != nil {
		return err
	}
	finger := scrcpy.PointerGenericFinger
	if err := l.send(scrcpy.InjectTouch(scrcpy.ActionDown, finger, a, 1, 0, 0)); err != nil {
		return err
	}
	start := time.Now()
	var werr error
	for {
		if werr = sleepCtx(ctx, swipeStep); werr != nil {
			break
		}
		f := float64(time.Since(start)) / float64(dur)
		if f >= 1 {
			break
		}
		p := a
		p.X = a.X + int32(float64(b.X-a.X)*f)
		p.Y = a.Y + int32(float64(b.Y-a.Y)*f)
		if err := l.send(scrcpy.InjectTouch(scrcpy.ActionMove, finger, p, 1, 0, 0)); err != nil {
			return err
		}
	}
	if err := l.send(scrcpy.InjectTouch(scrcpy.ActionMove, finger, b, 1, 0, 0)); err != nil {
		return err
	}
	if err := l.send(scrcpy.InjectTouch(scrcpy.ActionUp, finger, b, 0, 0, 0)); err != nil {
		return err
	}
	return werr
}

func (l *live) touch(ctx context.Context, a driver.TouchAction, p screen.Point) error {
	pos, err := l.position(ctx, p)
	if err != nil {
		return err
	}
	var action uint8
	pressure := 1.0
	switch a {
	case driver.TouchDown:
		action = scrcpy.ActionDown
	case driver.TouchMove:
		action = scrcpy.ActionMove
	case driver.TouchUp:
		action, pressure = scrcpy.ActionUp, 0
	default:
		return fmt.Errorf("android: touch action %q: %w", a, driver.ErrUnsupported)
	}
	return l.send(scrcpy.InjectTouch(action, scrcpy.PointerGenericFinger, pos, pressure, 0, 0))
}

func (l *live) key(code, meta int32) error {
	if err := l.send(scrcpy.InjectKeycode(scrcpy.ActionDown, code, 0, meta)); err != nil {
		return err
	}
	return l.send(scrcpy.InjectKeycode(scrcpy.ActionUp, code, 0, meta))
}

func (l *live) typeText(ctx context.Context, text string, opt driver.TypeOptions) error {
	if !opt.Append {
		// Select the field's text so the new text replaces it.
		if err := l.key(keycodeA, metaCtrlOn|metaCtrlLeftOn); err != nil {
			return err
		}
		if text == "" {
			if err := l.key(keycodeDel, 0); err != nil {
				return err
			}
		}
	}
	if text != "" {
		if plainASCII(text) {
			for _, part := range scrcpy.SplitText(text, scrcpy.MaxInjectText) {
				msg, err := scrcpy.InjectText(part)
				if err != nil {
					return err
				}
				if err := l.send(msg); err != nil {
					return err
				}
			}
		} else {
			if len(text) > scrcpy.MaxClipboardText {
				return fmt.Errorf("android: text too long to paste (%d bytes)", len(text))
			}
			if err := l.s.SetClipboard(ctx, text, true); err != nil {
				return l.mapErr(err)
			}
		}
	}
	if opt.Submit {
		return l.key(keycodeEnter, 0)
	}
	return nil
}

// plainASCII reports whether text is only printable ASCII, which the
// server can type with the virtual keyboard's key map. Control characters
// are excluded on purpose: a typed newline would press Enter.
func plainASCII(text string) bool {
	for i := 0; i < len(text); i++ {
		if c := text[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
