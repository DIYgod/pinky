package android

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

const (
	keyguardCmd = `dumpsys window policy | grep -E 'showing=|mIsShowing=|isKeyguardShowing=|mShowingLockscreen=|isStatusBarKeyguard='`
	// imeWindowCmd reads the keyboard window from the window manager,
	// which is much faster than `dumpsys input_method` (that also dumps
	// the keyboard app: 1.7 s on the Pixel).
	imeWindowCmd = `dumpsys window InputMethod | grep -E 'isVisible=|isOnScreen='`
)

// Observe implements driver.Device. With the companion app it reads the
// element tree, toasts and notifications from the app (about 50 ms);
// otherwise from `uiautomator dump` (about 2.2 s, most of it uiautomator
// waiting for the UI to idle). Either way the lock and keyboard state come
// from the window manager, in parallel. Element bounds use the
// screenshot's pixel space. OCR is not done here; opt.OCR is ignored.
func (d *Device) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	d.used()
	if c := d.companionClient(ctx); c != nil {
		s, err := d.observeCompanion(ctx, c)
		if err == nil && emptyWithKeyboard(s) {
			err = errEmptyTree
		}
		if err == nil {
			return s, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Turn it off before uiautomator runs, and fall back.
		d.companionFailed(err)
	}
	return d.observeDump(ctx)
}

// errEmptyTree is a companion tree with no elements while an app shows the
// keyboard, even with the keyboard's own nodes: the read missed something,
// so uiautomator reads the screen instead.
var errEmptyTree = errors.New("the tree has no elements while the keyboard shows")

// emptyWithKeyboard reports whether a screen is empty although an app has
// the keyboard up, which only happens when the tree is incomplete.
func emptyWithKeyboard(s *screen.Screen) bool {
	return len(s.Elements) == 0 && s.Keyboard && !s.Locked && s.App.ID != ""
}

// observeDump reads the screen with `uiautomator dump`.
func (d *Device) observeDump(ctx context.Context) (*screen.Screen, error) {
	captured := time.Now()
	type dumpResult struct {
		tree *uiTree
		err  error
	}
	dumpc := make(chan dumpResult, 1)
	go func() {
		t, err := d.dumpUI(ctx)
		dumpc <- dumpResult{t, err}
	}()
	statec := make(chan string, 1)
	go func() {
		out, err := d.client.Shell(ctx, d.info.ID, keyguardCmd+"; echo --ime--; "+imeWindowCmd)
		if err != nil {
			d.cfg.logf("android: %s: read state: %v", d.info.ID, err)
		}
		statec <- string(out)
	}()
	appsc := make(chan struct{})
	go func() {
		defer close(appsc)
		_, _ = d.cachedApps(ctx, 0)
	}()

	natW, natH, err := d.naturalSize(ctx)
	if err != nil {
		return nil, err
	}
	r := <-dumpc
	stateOut := <-statec
	<-appsc
	if r.err != nil {
		return nil, r.err
	}
	w, h := rotate(natW, natH, r.tree.Rotation)
	sec := splitSections(stateOut)
	locked, _ := parseKeyguard(sec[""])
	scr := &screen.Screen{
		Width:       w,
		Height:      h,
		Orientation: "portrait",
		Keyboard:    parseIMEWindowVisible(sec["ime"]),
		Locked:      locked,
		Elements:    convertTree(r.tree, w, h),
		CapturedAt:  captured,
	}
	if w > h {
		scr.Orientation = "landscape"
	}
	pkg := r.tree.Package()
	scr.App = screen.App{ID: pkg, Name: d.appName(pkg)}
	return scr, nil
}

// dumpUI runs `uiautomator dump`, writing to /dev/tty so the XML comes back
// on stdout, or to a temporary file on phones where that does not work.
func (d *Device) dumpUI(ctx context.Context) (*uiTree, error) {
	d.dumpMu.Lock()
	defer d.dumpMu.Unlock()
	if !d.dumpFile {
		out, err := d.client.Exec(ctx, d.info.ID, "uiautomator dump /dev/tty")
		if err != nil {
			return nil, wrapErr(err)
		}
		t, perr := parseUIDump(out)
		if perr == nil {
			return t, nil
		}
		if !errors.Is(perr, errNoHierarchy) || dumpFailed(out) {
			return nil, dumpError(out, perr)
		}
		d.cfg.logf("android: %s: uiautomator cannot dump to /dev/tty (%q); using a file", d.info.ID, firstLine(out))
		d.dumpFile = true
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	path := "/data/local/tmp/pinky-ui-" + hex.EncodeToString(b[:]) + ".xml"
	out, err := d.client.Exec(ctx, d.info.ID,
		"uiautomator dump "+path+" >/dev/null 2>&1; cat "+path+" 2>/dev/null; rm -f "+path)
	if err != nil {
		return nil, wrapErr(err)
	}
	t, perr := parseUIDump(out)
	if perr != nil {
		return nil, dumpError(out, perr)
	}
	return t, nil
}

func dumpFailed(out []byte) bool {
	return bytes.Contains(out, []byte("ERROR")) || bytes.Contains(out, []byte("Exception"))
}

func dumpError(out []byte, err error) error {
	if bytes.Contains(out, []byte("could not get idle state")) {
		return errors.New("android: uiautomator dump: the screen never stopped changing (could not get idle state)")
	}
	if l := firstLine(out); l != "" {
		return fmt.Errorf("android: uiautomator dump: %v: %s", err, l)
	}
	return fmt.Errorf("android: uiautomator dump: %w", err)
}

func firstLine(out []byte) string {
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Screenshot implements driver.Device with `screencap -p`, in the same
// pixel space as Observe.
func (d *Device) Screenshot(ctx context.Context) (image.Image, error) {
	d.used()
	out, err := d.client.Exec(ctx, d.info.ID, "screencap -p")
	if err != nil {
		return nil, wrapErr(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("android: screencap: %v (%d bytes: %q)", err, len(out), firstLine(out))
	}
	return img, nil
}

// Locked implements driver.LockReporter: true while the lock screen is
// showing, even when an app such as an alarm covers it.
func (d *Device) Locked(ctx context.Context) (bool, error) {
	out, err := d.client.Shell(ctx, d.info.ID, keyguardCmd)
	if err != nil {
		return false, wrapErr(err)
	}
	if showing, ok := parseKeyguard(string(out)); ok {
		return showing, nil
	}
	// Some builds keep the flag only in the full window dump.
	out, err = d.client.Shell(ctx, d.info.ID, `dumpsys window | grep -E 'isKeyguardShowing=|mShowingLockscreen=|isStatusBarKeyguard='`)
	if err != nil {
		return false, wrapErr(err)
	}
	showing, ok := parseKeyguard(string(out))
	if !ok {
		return false, errors.New("android: cannot tell whether the phone is locked")
	}
	return showing, nil
}
