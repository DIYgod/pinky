package android

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/companion"
	"github.com/DIYgod/pinky/internal/screen"
)

// Waking and unlocking. Pinky never keeps a phone awake and never presses
// the power key: the screen is turned on with KEYCODE_WAKEUP, which can
// only ever turn it on, and the phone's own screen timeout and lock apply
// between steps.

// screenCmd reads each display's power state from the display power
// controllers, and the overall wakefulness as a fallback. With a
// background display in its own display group the phone stays awake while
// its own screen is off, so neither the wakefulness nor the device idle
// controller's mScreenOn tells about display 0 (both say "on").
const screenCmd = `dumpsys display | grep -E 'mDisplayId=|mScreenState='; dumpsys power | grep -E 'mWakefulness='`

// panelCmd reads the built-in panel's power mode from SurfaceFlinger
// (Android 14 and later): a panel turned off with scrcpy's display power
// control is off while the display power controller says on.
const panelCmd = `dumpsys SurfaceFlinger --displays | grep -m 4 -E 'connectionType=|powerMode='`

// ScreenOn implements driver.Waker: whether the phone's own display is on.
// Dozing (an always-on display) counts as off, and so does a panel that
// is off while the phone is awake (dark.go).
func (d *Device) ScreenOn(ctx context.Context) (bool, error) {
	if d.darkPanel() {
		return false, nil
	}
	cmd := screenCmd
	if d.props.SDK >= 34 {
		cmd += "; " + panelCmd
	}
	out, err := d.client.Shell(ctx, d.info.ID, cmd)
	if err != nil {
		return false, wrapErr(err)
	}
	on, ok := parseScreenOn(string(out))
	if !ok {
		return false, fmt.Errorf("android: cannot tell whether the screen is on from %q", firstLine(out))
	}
	if on && parsePhonePower(string(out)).panel == "Off" {
		on = false
	}
	if on {
		d.markAwake()
	}
	return on, nil
}

// Wake implements driver.Waker. It presses KEYCODE_WAKEUP when the screen
// is off and waits for it to come on; it never unlocks.
func (d *Device) Wake(ctx context.Context) error {
	l, err := d.session(ctx)
	if err != nil {
		return err
	}
	return d.wakeNow(ctx, l)
}

// wakeNow checks the screen without trusting an earlier check and wakes it
// when it is off. A panel that a background display keeps off is turned
// back on instead: the phone is awake already.
func (d *Device) wakeNow(ctx context.Context, l *live) error {
	if d.undark() {
		d.markAwake()
		return nil
	}
	on, err := d.ScreenOn(ctx)
	if err != nil {
		return err
	}
	if on {
		return nil
	}
	return d.pressWakeup(ctx, l)
}

// pressWakeup presses KEYCODE_WAKEUP and waits up to 2 s for the screen.
func (d *Device) pressWakeup(ctx context.Context, l *live) error {
	d.cfg.logf("android: %s: screen is off, waking it", d.info.ID)
	if err := l.input(func() error { return l.key(keycodeWakeup, 0) }); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := sleepCtx(ctx, 150*time.Millisecond); err != nil {
			return err
		}
		on, err := d.ScreenOn(ctx)
		if err == nil && on {
			break
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return errors.New("android: the screen did not turn on")
		}
	}
	// Give the display a moment before input lands on it.
	return sleepCtx(ctx, 250*time.Millisecond)
}

// Unlock timing.
const (
	bouncerWait  = 2 * time.Second        // for the PIN pad to appear
	pinKeyGap    = 80 * time.Millisecond  // between the PIN's digits
	unlockVerify = 4 * time.Second        // for the lock screen to go after Enter
	lockPoll     = 200 * time.Millisecond // between lock checks
)

// Unlock implements driver.Unlocker. It wakes the screen, asks the system
// to dismiss the lock screen (`wm dismiss-keyguard`, which shows the PIN
// pad on a secure lock screen), and swipes up if no PIN pad or password
// field shows. Only once it sees one does it enter pin, as key events
// through scrcpy (never in a shell command line, where other processes
// could read it), then press Enter unless the phone already unlocked
// itself. It returns nil once the lock screen is gone and wraps
// driver.ErrLocked when it is still there. It enters the PIN at most once
// and never logs it.
func (d *Device) Unlock(ctx context.Context, pin string) error {
	d.used()
	digits, err := checkPIN(pin)
	if err != nil {
		return err
	}
	l, err := d.session(ctx)
	if err != nil {
		return err
	}
	if err := d.wakeNow(ctx, l); err != nil {
		return err
	}
	locked, err := d.Locked(ctx)
	if err != nil || !locked {
		return err
	}
	b, err := d.showBouncer(ctx, l)
	if err != nil {
		return err
	}
	if b.kind == bouncerGone {
		return nil
	}
	switch {
	case b.kind == bouncerPattern:
		return fmt.Errorf("android: the phone is locked with a pattern; Pinky enters only a PIN or a password: %w", driver.ErrUnsupported)
	case b.kind == bouncerPIN && !digits:
		return fmt.Errorf("android: the phone asks for a PIN, and the saved one is not all digits; nothing was entered: %w", driver.ErrPINNotEntered)
	case !b.enabled:
		return fmt.Errorf("android: the phone does not take a PIN right now (too many attempts); nothing was entered: %w", driver.ErrPINNotEntered)
	}
	// One gesture: a session restart waits for the whole PIN, and a PIN
	// that was not sent at all goes to the new session.
	if err := d.withInput(ctx, false, func(l *live) error { return enterPIN(ctx, l, pin, digits) }); err != nil {
		return err
	}
	// Some phones unlock as soon as the last digit is right; Enter would
	// then go to the app behind the lock screen.
	if err := sleepCtx(ctx, 300*time.Millisecond); err != nil {
		return err
	}
	if locked, err := d.Locked(ctx); err == nil && !locked {
		d.markAwake()
		return nil
	}
	if err := d.withInput(ctx, false, func(l *live) error { return l.key(keycodeEnter, 0) }); err != nil {
		return err
	}
	deadline := time.Now().Add(unlockVerify)
	for {
		if err := sleepCtx(ctx, lockPoll); err != nil {
			return err
		}
		locked, err := d.Locked(ctx)
		if err == nil && !locked {
			d.markAwake()
			d.cfg.logf("android: %s: unlocked", d.info.ID)
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			d.cfg.logf("android: %s: still locked after entering the PIN", d.info.ID)
			return fmt.Errorf("android: the phone is still locked after the PIN was entered (wrong PIN?); not retried: %w", driver.ErrLocked)
		}
	}
}

// checkPIN accepts a PIN of 4 to 16 digits or a password of printable
// ASCII (up to 64 characters), which is what Android's lock screen takes
// from key events. It reports whether pin is all digits.
func checkPIN(pin string) (digits bool, err error) {
	if pin == "" {
		return false, errors.New("android: no PIN to enter")
	}
	if len(pin) > 64 || !plainASCII(pin) {
		return false, fmt.Errorf("android: the PIN or password has characters Pinky cannot type on the lock screen: %w", driver.ErrUnsupported)
	}
	digits = true
	for i := 0; i < len(pin); i++ {
		if pin[i] < '0' || pin[i] > '9' {
			digits = false
		}
	}
	if digits && (len(pin) < 4 || len(pin) > 16) {
		return false, errors.New("android: a PIN has 4 to 16 digits")
	}
	return digits, nil
}

// enterPIN types the PIN: digits as their key codes, a password as text.
func enterPIN(ctx context.Context, l *live, pin string, digits bool) error {
	if !digits {
		return l.injectText(pin)
	}
	for i := 0; i < len(pin); i++ {
		if i > 0 {
			if err := sleepCtx(ctx, pinKeyGap); err != nil {
				return err
			}
		}
		if err := l.key(keycode0+int32(pin[i]-'0'), 0); err != nil {
			return err
		}
	}
	return nil
}

// showBouncer brings up the PIN pad: first with `wm dismiss-keyguard`,
// then, if nothing shows, with a swipe up from the lower middle of the
// screen. It returns bouncerGone when the lock screen went away without a
// PIN (no secure lock, or a trusted device nearby).
func (d *Device) showBouncer(ctx context.Context, l *live) (bouncer, error) {
	if _, err := d.client.Shell(ctx, d.info.ID, "wm dismiss-keyguard"); err != nil {
		return bouncer{}, wrapErr(err)
	}
	b, err := d.waitBouncer(ctx, bouncerWait)
	if err != nil || b.kind != bouncerNone {
		return b, err
	}
	w, h, err := d.naturalSize(ctx)
	if err != nil {
		return bouncer{}, err
	}
	vw, vh, err := l.videoSize(ctx)
	if err == nil && vw > vh {
		w, h = max(w, h), min(w, h)
	} else {
		w, h = min(w, h), max(w, h)
	}
	if b.fingerprint {
		// The alternate bouncer waits for a finger on the sensor at the
		// bottom; a tap away from it brings up the PIN pad.
		d.cfg.logf("android: %s: the lock screen asks for a fingerprint; tapping above the sensor for the PIN pad", d.info.ID)
		tap := func(l *live) error { return l.tap(ctx, screen.Point{X: w / 2, Y: h * 30 / 100}, 0) }
		if err := d.withInput(ctx, false, tap); err != nil {
			return bouncer{}, err
		}
		if b, err = d.waitBouncer(ctx, bouncerWait); err != nil || b.kind != bouncerNone {
			return b, err
		}
	}
	// A swipe up, starting above where under-display fingerprint sensors
	// sit: one that starts on the sensor goes to the sensor instead.
	d.cfg.logf("android: %s: no PIN pad after dismiss-keyguard; swiping up", d.info.ID)
	swipe := func(l *live) error {
		return l.swipe(ctx, screen.Point{X: w / 2, Y: h * 70 / 100}, screen.Point{X: w / 2, Y: h * 25 / 100}, 250*time.Millisecond)
	}
	if err := d.withInput(ctx, false, swipe); err != nil {
		return bouncer{}, err
	}
	b, err = d.waitBouncer(ctx, bouncerWait)
	if err != nil {
		return b, err
	}
	if b.kind == bouncerNone {
		return b, fmt.Errorf("android: the lock screen showed no PIN pad; nothing was entered: %w", driver.ErrPINNotEntered)
	}
	return b, nil
}

// waitBouncer reads the screen until it shows a PIN pad, a password field
// or a pattern, or the lock screen is gone, for up to max.
func (d *Device) waitBouncer(ctx context.Context, max time.Duration) (bouncer, error) {
	deadline := time.Now().Add(max)
	for {
		if err := sleepCtx(ctx, 400*time.Millisecond); err != nil {
			return bouncer{}, err
		}
		locked, err := d.Locked(ctx)
		if err != nil {
			return bouncer{}, err
		}
		if !locked {
			return bouncer{kind: bouncerGone}, nil
		}
		b, err := d.readBouncer(ctx)
		if err != nil {
			return bouncer{}, err
		}
		// The fingerprint screen will not turn into the PIN pad by itself.
		if b.kind != bouncerNone || b.fingerprint || time.Now().After(deadline) {
			return b, nil
		}
	}
}

// readBouncer reads the main display's tree, through the companion when
// it is on (uiautomator would reset it) and with uiautomator otherwise.
func (d *Device) readBouncer(ctx context.Context) (bouncer, error) {
	var nodes []lockNode
	if c := d.companionClient(ctx); c != nil {
		t, err := c.Dump(ctx, companion.DumpOptions{})
		if err == nil {
			for _, w := range t.Windows {
				nodes = appendCompanionNodes(nodes, w.Package, w.Root)
			}
			return classifyBouncer(nodes), nil
		}
		if ctx.Err() != nil {
			return bouncer{}, ctx.Err()
		}
		d.companionFailed(err)
	}
	t, err := d.dumpUI(ctx)
	if err != nil {
		return bouncer{}, err
	}
	for _, r := range t.Roots {
		nodes = appendUINodes(nodes, r)
	}
	return classifyBouncer(nodes), nil
}

// bouncerKind is what the lock screen shows.
type bouncerKind int

const (
	bouncerNone     bouncerKind = iota // the clock and notifications, no way to enter anything yet
	bouncerGone                        // no lock screen any more
	bouncerPIN                         // a PIN pad
	bouncerPassword                    // a password field
	bouncerPattern                     // a pattern grid
)

func (k bouncerKind) String() string {
	return [...]string{"none", "gone", "PIN", "password", "pattern"}[k]
}

// bouncer is the state of the lock screen's credential view.
type bouncer struct {
	kind    bouncerKind
	enabled bool // false while the phone refuses attempts
	// fingerprint: the alternate bouncer shows, which asks for a finger on
	// an under-display sensor instead of the PIN (phones with one, such as
	// the Mi 10 Pro). A tap away from the sensor brings up the PIN pad.
	fingerprint bool
}

// lockNode is the part of a tree node that classifyBouncer looks at, from
// either a uiautomator dump or a companion tree.
type lockNode struct {
	pkg, id, text, desc        string
	password, focused, enabled bool
}

func appendUINodes(out []lockNode, n *uiNode) []lockNode {
	if n == nil {
		return out
	}
	out = append(out, lockNode{pkg: n.Package, id: n.ResID, text: n.Text, desc: n.Desc,
		password: n.Password, focused: n.Focused, enabled: n.Enabled})
	for _, c := range n.Children {
		out = appendUINodes(out, c)
	}
	return out
}

func appendCompanionNodes(out []lockNode, pkg string, n *companion.Node) []lockNode {
	if n == nil {
		return out
	}
	out = append(out, lockNode{pkg: pkg, id: n.ID, text: n.Text, desc: n.Desc,
		password: n.Password, focused: n.Focused, enabled: !n.Disabled})
	for _, c := range n.Children {
		out = appendCompanionNodes(out, pkg, c)
	}
	return out
}

// Resource ids of AOSP's keyguard views (Android 10 to 17); vendors that
// keep SystemUI's layouts keep them too.
var (
	pinEntryIDs      = []string{"pinEntry", "pin_entry"}
	passwordEntryIDs = []string{"passwordEntry", "password_entry"}
	patternIDs       = []string{"lockPatternView", "lock_pattern_view"}
	// The alternate bouncer of phones with an under-display fingerprint
	// sensor (Android 13+).
	fingerprintIDs = []string{"alternate_bouncer", "alternate_bouncer_udfps_icon_view", "udfps_touch_overlay"}
	pinKeyIDs      = []string{"key0", "key1", "key2", "key3", "key4", "key5", "key6", "key7", "key8", "key9", "digit_text"}
)

// classifyBouncer finds the lock screen's credential view among the
// nodes: SystemUI's PIN, password or pattern view by resource id, or a
// focused password field in SystemUI next to digit keys (a PIN pad) or
// without them (a password).
func classifyBouncer(nodes []lockNode) bouncer {
	var pinEntry, passEntry, pattern, field *lockNode
	keys := 0
	keysEnabled := 0
	fingerprint := false
	for i := range nodes {
		n := &nodes[i]
		name := idName(n.id)
		if hasName(name, fingerprintIDs) {
			fingerprint = true
		}
		switch {
		case hasName(name, pinEntryIDs):
			pinEntry = n
		case hasName(name, passwordEntryIDs):
			passEntry = n
		case hasName(name, patternIDs):
			pattern = n
		case hasName(name, pinKeyIDs) || isSystemUI(n.pkg) && isDigitKey(n):
			keys++
			if n.enabled {
				keysEnabled++
			}
		}
		if n.password && n.focused && isSystemUI(n.pkg) && field == nil {
			field = n
		}
	}
	switch {
	case pinEntry != nil:
		return bouncer{kind: bouncerPIN, enabled: pinEntry.enabled && (keys == 0 || keysEnabled > 0)}
	case passEntry != nil:
		return bouncer{kind: bouncerPassword, enabled: passEntry.enabled}
	case pattern != nil:
		return bouncer{kind: bouncerPattern, enabled: pattern.enabled}
	case field != nil && keys >= 10:
		return bouncer{kind: bouncerPIN, enabled: field.enabled && keysEnabled > 0}
	case field != nil:
		return bouncer{kind: bouncerPassword, enabled: field.enabled}
	}
	return bouncer{kind: bouncerNone, fingerprint: fingerprint}
}

func hasName(name string, names []string) bool {
	for _, n := range names {
		if name == n {
			return true
		}
	}
	return false
}

func isSystemUI(pkg string) bool {
	return pkg == "com.android.systemui" || strings.HasSuffix(pkg, ".systemui") || strings.Contains(pkg, "keyguard")
}

// isDigitKey reports whether a node is a key of a PIN pad: a single digit
// as its text or description.
func isDigitKey(n *lockNode) bool {
	for _, s := range []string{n.text, n.desc} {
		if len(s) == 1 && s[0] >= '0' && s[0] <= '9' {
			return true
		}
	}
	return false
}

var (
	_ driver.Waker    = (*Device)(nil)
	_ driver.Unlocker = (*Device)(nil)
)
