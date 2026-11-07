//go:build device

package android

// Tests on a real phone. They use only the home screen, Settings (search
// only, no setting is changed) and the Calculator, and restore the
// clipboard after the paste test. Run them through the device lock:
//
//	scripts/devlock.sh <id> env PINKY_DEVICE=<id> go test -tags device -v ./internal/driver/android/

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

func openTestDevice(t *testing.T) (*Device, context.Context) {
	t.Helper()
	serial := os.Getenv("PINKY_DEVICE")
	if serial == "" {
		t.Skip("PINKY_DEVICE not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	p := NewProvider(Config{Logf: t.Logf})
	d, err := p.OpenDevice(ctx, serial)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	locked, err := d.Locked(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		t.Skip("the phone is locked; leaving it alone")
	}
	return d, ctx
}

// act runs an action, waits for the screen to settle and paces like a person.
func act(t *testing.T, ctx context.Context, d *Device, what string, f func() error) {
	t.Helper()
	start := time.Now()
	if err := f(); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	ok, err := d.WaitSettled(ctx, 0)
	if err != nil {
		t.Fatalf("%s: settle: %v", what, err)
	}
	t.Logf("%s: settled=%v in %v", what, ok, time.Since(start).Round(time.Millisecond))
	time.Sleep(300 * time.Millisecond)
}

func observe(t *testing.T, ctx context.Context, d *Device) *screen.Screen {
	t.Helper()
	start := time.Now()
	s, err := d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	screen.NewRefs().Assign(s)
	// Toasts and notifications may hold the owner's messages: keep their
	// kinds, never their text, in test logs.
	kinds := make([]string, len(s.Events))
	for i, ev := range s.Events {
		kinds[i] = string(ev.Kind)
	}
	s.Events = nil
	t.Logf("observe: %d elements in %v (events: %v)", len(s.Elements), time.Since(start).Round(time.Millisecond), kinds)
	return s
}

func find(s *screen.Screen, match func(screen.Element) bool) (screen.Element, bool) {
	for _, e := range s.Elements {
		if match(e) {
			return e, true
		}
	}
	return screen.Element{}, false
}

func TestDeviceObserveAndScreenshot(t *testing.T) {
	d, ctx := openTestDevice(t)
	act(t, ctx, d, "home", func() error { return d.Key(ctx, driver.KeyHome) })
	s := observe(t, ctx, d)
	t.Logf("\n%s", s.Render())
	if s.App.ID == "" || len(s.Elements) == 0 {
		t.Fatalf("empty screen: %s", s.Header())
	}
	img, err := d.Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != s.Width || b.Dy() != s.Height {
		t.Errorf("screenshot %dx%d, screen %dx%d", b.Dx(), b.Dy(), s.Width, s.Height)
	}
	for _, e := range s.Elements {
		if e.Bounds.X < 0 || e.Bounds.Y < 0 || e.Bounds.X+e.Bounds.W > s.Width || e.Bounds.Y+e.Bounds.H > s.Height {
			t.Errorf("element outside the screen: %s", e.Line())
		}
	}
}

func calculatorPackage(t *testing.T, ctx context.Context, d *Device) string {
	apps, err := d.Apps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		if strings.Contains(a.ID, "calculator") {
			return a.ID
		}
	}
	t.Skip("no calculator app")
	return ""
}

func TestDeviceCalculator(t *testing.T) {
	d, ctx := openTestDevice(t)
	pkg := calculatorPackage(t, ctx, d)
	act(t, ctx, d, "home", func() error { return d.Key(ctx, driver.KeyHome) })
	act(t, ctx, d, "launch "+pkg, func() error { return d.Launch(ctx, pkg) })
	s := observe(t, ctx, d)
	if s.App.ID != pkg {
		t.Fatalf("foreground app %q, want %q", s.App.ID, pkg)
	}
	button := func(s *screen.Screen, labels ...string) screen.Element {
		t.Helper()
		e, ok := find(s, func(e screen.Element) bool {
			if e.Role != screen.RoleButton {
				return false
			}
			for _, l := range labels {
				if e.Label == l {
					return true
				}
			}
			return false
		})
		if !ok {
			t.Fatalf("no button %q on\n%s", labels, s.Render())
		}
		return e
	}
	// Clear, then 1 + 2 =. Labels are the digits and operator symbols; the
	// clear button is "AC" or, on Google's calculator in Chinese, "\u6e05\u9664".
	clear, ok := find(s, func(e screen.Element) bool {
		return e.Role == screen.RoleButton && (e.Label == "AC" || e.Label == "\u6e05\u9664" || e.Label == "clear")
	})
	if ok {
		act(t, ctx, d, "AC", func() error { return d.Tap(ctx, clear.Bounds.Center(), driver.TapOptions{}) })
	}
	for _, key := range [][]string{{"1"}, {"+", "\u52a0"}, {"2"}, {"=", "\u7b49\u4e8e"}} {
		e := button(s, key...)
		act(t, ctx, d, "tap "+key[0], func() error { return d.Tap(ctx, e.Bounds.Center(), driver.TapOptions{}) })
	}
	s = observe(t, ctx, d)
	if _, ok := find(s, func(e screen.Element) bool { return e.Label == "3" || strings.HasSuffix(e.Label, " 3") }); !ok {
		t.Errorf("no result 3 on\n%s", s.Render())
	}
	act(t, ctx, d, "home", func() error { return d.Key(ctx, driver.KeyHome) })
}

func TestDeviceTypeAndClipboard(t *testing.T) {
	d, ctx := openTestDevice(t)
	// Keep the owner's clipboard: read it now and put it back at the end.
	// Its content is never logged.
	saved, err := d.Clipboard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if saved != "" {
			if err := d.SetClipboard(context.Background(), saved); err != nil {
				t.Errorf("restore clipboard: %v", err)
			}
		}
	}()

	act(t, ctx, d, "home", func() error { return d.Key(ctx, driver.KeyHome) })
	act(t, ctx, d, "launch settings", func() error { return d.Launch(ctx, "com.android.settings") })
	s := observe(t, ctx, d)
	// Settings may resume on its search page; otherwise open it. The entry
	// says "\u641c\u7d22" ("search") in Chinese.
	if _, open := find(s, func(e screen.Element) bool { return e.Role == screen.RoleInput }); !open {
		search, ok := find(s, func(e screen.Element) bool {
			l := strings.ToLower(e.Label)
			return e.Role != screen.RoleList && (strings.Contains(e.Label, "\u641c\u7d22") || strings.Contains(l, "search"))
		})
		if !ok {
			t.Fatalf("no search entry on\n%s", s.Render())
		}
		act(t, ctx, d, "tap search", func() error { return d.Tap(ctx, search.Bounds.Center(), driver.TapOptions{}) })
	}
	focusedInput := func(s *screen.Screen) screen.Element {
		t.Helper()
		e, ok := find(s, func(e screen.Element) bool { return e.Role == screen.RoleInput && e.Has(screen.StateFocused) })
		if !ok {
			t.Fatalf("no focused input on\n%s", s.Render())
		}
		return e
	}
	s = observe(t, ctx, d)
	focusedInput(s)
	if !s.Keyboard {
		t.Logf("keyboard not reported as shown")
	}

	act(t, ctx, d, "type ascii", func() error { return d.Type(ctx, "pinky", driver.TypeOptions{}) })
	if got := focusedInput(observe(t, ctx, d)).Label; got != "pinky" {
		t.Errorf("after typing ASCII the input shows %q", got)
	}
	act(t, ctx, d, "append ascii", func() error { return d.Type(ctx, " ok", driver.TypeOptions{Append: true}) })
	if got := focusedInput(observe(t, ctx, d)).Label; got != "pinky ok" {
		t.Errorf("after appending the input shows %q", got)
	}
	// "\u663e\u793a" is "display"; it goes through the clipboard.
	act(t, ctx, d, "type cjk", func() error { return d.Type(ctx, "\u663e\u793a", driver.TypeOptions{}) })
	if got := focusedInput(observe(t, ctx, d)).Label; got != "\u663e\u793a" {
		t.Errorf("after typing CJK the input shows %q", got)
	}
	clip, err := d.Clipboard(ctx)
	switch {
	case err != nil:
		t.Errorf("clipboard: %v", err)
	case d.companionOn():
		// ACTION_SET_TEXT leaves the clipboard alone.
		if clip != saved {
			t.Errorf("typing through the companion changed the clipboard")
		}
	case clip != "\u663e\u793a":
		t.Errorf("clipboard after paste = %q", clip)
	}
	t.Logf("typed through the companion: %v", d.companionOn())
	act(t, ctx, d, "clear", func() error { return d.Type(ctx, "", driver.TypeOptions{}) })
	act(t, ctx, d, "back", func() error { return d.Key(ctx, driver.KeyBack) })
	act(t, ctx, d, "back", func() error { return d.Key(ctx, driver.KeyBack) })
	act(t, ctx, d, "home", func() error { return d.Key(ctx, driver.KeyHome) })
}

func TestDeviceStreamAndSettle(t *testing.T) {
	d, ctx := openTestDevice(t)
	st, err := d.Stream(ctx, driver.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w, h := st.Size()
	t.Logf("stream %dx%d %s", w, h, st.Codec())
	first := true
	var keys, frames int
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case p, ok := <-st.Packets():
			if !ok {
				t.Fatalf("stream ended: %v", st.Err())
			}
			if first && !p.Config {
				t.Fatal("the stream did not start with a configuration packet")
			}
			first = false
			if p.KeyFrame {
				keys++
			}
			if !p.Config {
				frames++
			}
		case <-timeout:
			break loop
		}
	}
	if keys == 0 || frames == 0 {
		t.Errorf("frames=%d key frames=%d", frames, keys)
	}
	// A still screen settles within the quiet period plus refinements.
	start := time.Now()
	ok, err := d.WaitSettled(ctx, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("still screen: settled=%v in %v", ok, time.Since(start).Round(time.Millisecond))
	if os.Getenv("PINKY_TRACE") != "" {
		vs, _ := d.VideoStats(ctx, start.Add(-time.Second))
		var prev time.Duration
		for _, p := range vs.Packets {
			t.Logf("  +%5dms gap %4dms %7d B config=%v key=%v change=%v", p.At.Sub(start).Milliseconds(), (p.PTS - prev).Milliseconds(), p.Size, p.Config, p.KeyFrame, p.Change)
			prev = p.PTS
		}
	}
	// Input keeps working while the stream is open.
	act(t, ctx, d, "home", func() error { return d.Key(ctx, driver.KeyHome) })
}

func TestDeviceLaunchUnknown(t *testing.T) {
	d, ctx := openTestDevice(t)
	err := d.Launch(ctx, "no such app pinky")
	if err == nil || !strings.Contains(err.Error(), "no app") {
		t.Errorf("launch unknown = %v", err)
	}
	err = d.Launch(ctx, "com.example.pinky.missing")
	if err == nil {
		t.Error("launching a missing package succeeded")
	}
	t.Logf("missing package: %v", err)
}

func TestDeviceBackground(t *testing.T) {
	d, ctx := openTestDevice(t)
	pkg := calculatorPackage(t, ctx, d)
	act(t, ctx, d, "home", func() error { return d.Key(ctx, driver.KeyHome) })
	b, err := d.OpenDisplay(ctx, DisplayOptions{App: pkg})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.DisplayID() <= 0 {
		t.Fatalf("display id %d", b.DisplayID())
	}
	if _, err := b.WaitSettled(ctx, 0); err != nil {
		t.Fatal(err)
	}
	displays := func() string {
		out, err := d.client.Shell(ctx, d.info.ID, "dumpsys window displays | grep -E 'Display: mDisplayId|mFocusedApp='")
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	// The calculator runs on the virtual display; the main display keeps
	// its own app.
	out := displays()
	i := strings.Index(out, fmt.Sprintf("mDisplayId=%d ", b.DisplayID()))
	if i < 0 || parseFocusedApp(out[i:]) != pkg {
		t.Fatalf("calculator not on display %d:\n%s", b.DisplayID(), out)
	}
	if main := parseFocusedApp(out); main == pkg {
		t.Errorf("the calculator took over the main display:\n%s", out)
	}
	// No text field is focused, so typed keys reach the calculator.
	if err := b.Type(ctx, "6*7", driver.TypeOptions{Submit: true}); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.WaitSettled(ctx, 0); err != nil || !ok {
		t.Errorf("settle on the virtual display: %v %v", ok, err)
	}
	if _, err := b.Screenshot(ctx); !errors.Is(err, driver.ErrUnsupported) {
		t.Errorf("screenshot of a virtual display: %v", err)
	}
	if s, err := b.Observe(ctx, driver.ObserveOptions{}); err == nil {
		// Through the companion app: the calculator is on this display.
		if s.App.ID != pkg || len(s.Elements) == 0 {
			t.Errorf("virtual display screen: %s, %d elements", s.Header(), len(s.Elements))
		}
		t.Logf("virtual display: %s, %d elements", s.Header(), len(s.Elements))
	} else if !errors.Is(err, driver.ErrUnsupported) {
		t.Errorf("observe of a virtual display: %v", err)
	}
	b.Close()
	time.Sleep(time.Second)
	if out := displays(); strings.Contains(out, fmt.Sprintf("mDisplayId=%d ", b.DisplayID())) {
		t.Errorf("display %d still exists after Close:\n%s", b.DisplayID(), out)
	}
}

// companionOn reports whether the driver is using the companion app.
func (d *Device) companionOn() bool {
	d.comp.mu.Lock()
	defer d.comp.mu.Unlock()
	return d.comp.on
}

func TestDevicePersonQuietWhileInjecting(t *testing.T) {
	d, ctx := openTestDevice(t)
	pkg := calculatorPackage(t, ctx, d)
	signals := d.PersonInput()
	var n int
	counted := make(chan struct{})
	go func() {
		defer close(counted)
		for range signals {
			n++
		}
	}()
	time.Sleep(2 * time.Second) // let getevent start
	act(t, ctx, d, "home", func() error { return d.Key(ctx, driver.KeyHome) })
	act(t, ctx, d, "launch", func() error { return d.Launch(ctx, pkg) })
	s := observe(t, ctx, d)
	if len(s.Elements) < 10 {
		t.Fatalf("calculator not on screen:\n%s", s.Render())
	}
	// Taps, a long press, a swipe, typed keys and hardware-like keys: all
	// injected, none may look like the person.
	for _, e := range s.Elements[len(s.Elements)-4:] {
		act(t, ctx, d, "tap "+e.Label, func() error { return d.Tap(ctx, e.Bounds.Center(), driver.TapOptions{}) })
	}
	mid := screen.Point{X: s.Width / 2, Y: s.Height / 2}
	act(t, ctx, d, "long press", func() error { return d.Tap(ctx, mid, driver.TapOptions{LongPress: true}) })
	act(t, ctx, d, "swipe", func() error {
		return d.Swipe(ctx, screen.Point{X: mid.X, Y: mid.Y + 200}, screen.Point{X: mid.X, Y: mid.Y - 200}, 300*time.Millisecond)
	})
	act(t, ctx, d, "type", func() error { return d.Type(ctx, "12", driver.TypeOptions{Append: true}) })
	act(t, ctx, d, "back", func() error { return d.Key(ctx, driver.KeyBack) })
	act(t, ctx, d, "home", func() error { return d.Key(ctx, driver.KeyHome) })
	time.Sleep(time.Second)
	d.Close()
	select {
	case <-counted:
	case <-time.After(10 * time.Second):
		t.Fatal("PersonInput was not closed with the device")
	}
	if n != 0 {
		t.Errorf("%d person signals while only Pinky gave input", n)
	}
}
