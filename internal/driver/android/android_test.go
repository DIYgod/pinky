package android

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/driver/android/scrcpy"
	"github.com/DIYgod/pinky/internal/screen"
)

// Outputs below are hand-written in the shape each Android version prints.

func TestParseKeyguard(t *testing.T) {
	android17 := "    KeyguardServiceDelegate\n      showing=true\n      occluded=false\n      simSecure=false\n"
	android10 := "    KeyguardServiceDelegate\n      showing=false\n      showingAndNotOccluded=true\n      KeyguardStateMonitor\n        mIsShowing=false\n"
	older := "    mShowingLockscreen=true mShowingDream=false\n"
	for name, c := range map[string]struct {
		out           string
		showing, have bool
	}{
		"android 17": {android17, true, true},
		"android 10": {android10, false, true},
		"older":      {older, true, true},
		"nothing":    {"mDreamingLockscreen=false\n", false, false},
	} {
		showing, ok := parseKeyguard(c.out)
		if showing != c.showing || ok != c.have {
			t.Errorf("%s: showing=%v ok=%v", name, showing, ok)
		}
	}
}

func TestParseIMEShown(t *testing.T) {
	if parseIMEShown("      mImeWindowVis=0\n      mInputShown=false\n") {
		t.Error("hidden keyboard reported as shown")
	}
	if !parseIMEShown("  mShowRequested=true mShowExplicitlyRequested=false mShowForced=false mInputShown=true\n") {
		t.Error("Android 10 keyboard not seen")
	}
	if !parseIMEShown("      mImeWindowVis=3\n") {
		t.Error("IME_VISIBLE bit not seen")
	}
}

func TestParseMisc(t *testing.T) {
	if pkg := parseFocusedApp("  mFocusedApp=ActivityRecord{250279450 u0 app.lawnchair.play/app.lawnchair.LawnchairLauncher t348}"); pkg != "app.lawnchair.play" {
		t.Errorf("focused app = %q", pkg)
	}
	if pkg := parseFocusedApp("  mFocusedApp=AppWindowToken{7d88c2 token=Token{abab10d ActivityRecord{10b6fa4 u0 com.google.android.apps.nexuslauncher/.NexusLauncherActivity t163}}}"); pkg != "com.google.android.apps.nexuslauncher" {
		t.Errorf("focused app (Android 10) = %q", pkg)
	}
	for out, want := range map[string]bool{"  mWakefulness=Awake\n": true, "  mWakefulness=Dozing\n": false, "  mWakefulness=Asleep\n": false} {
		if awake, ok := parseAwake(out); !ok || awake != want {
			t.Errorf("parseAwake(%q) = %v, %v", out, awake, ok)
		}
	}
	w, h, ok := parseWMSize("Physical size: 1080x2340\n")
	if !ok || w != 1080 || h != 2340 {
		t.Errorf("wm size = %dx%d %v", w, h, ok)
	}
	w, h, _ = parseWMSize("Physical size: 1080x2340\nOverride size: 720x1560\n")
	if w != 720 || h != 1560 {
		t.Errorf("override = %dx%d", w, h)
	}
	if w, h := rotate(1080, 2340, 1); w != 2340 || h != 1080 {
		t.Errorf("rotate = %dx%d", w, h)
	}
	if w, h := orient(1080, 2340, 1920, 886); w != 2340 || h != 1080 {
		t.Errorf("orient landscape = %dx%d", w, h)
	}
	if w, h := orient(2340, 1080, 540, 1170); w != 1080 || h != 2340 {
		t.Errorf("orient portrait = %dx%d", w, h)
	}
	sec := splitSections("showing=false\n--ime--\nmInputShown=true\n")
	if sec[""] != "showing=false\n" || sec["ime"] != "mInputShown=true\n\n" {
		t.Errorf("sections = %q", sec)
	}
}

func TestInfoFromEntry(t *testing.T) {
	info := infoFromEntry(adb.DeviceEntry{Serial: "3e8be48e", State: "device", USB: "1-1", Model: "Mi_10_Pro"})
	if info.State != driver.StateOnline || info.Name != "Mi 10 Pro" || info.Connection != "usb" || info.Backend != "scrcpy" {
		t.Errorf("online = %+v", info)
	}
	info = infoFromEntry(adb.DeviceEntry{Serial: "192.168.1.2:5555", State: "unauthorized"})
	if info.State != driver.StateUnauthorized || info.Connection != "wifi" || info.Detail == "" {
		t.Errorf("unauthorized = %+v", info)
	}
	info = infoFromEntry(adb.DeviceEntry{Serial: "x", State: "no permissions", Detail: "missing udev rules"})
	if info.State != driver.StateNeedsSetup {
		t.Errorf("no permissions = %+v", info)
	}
	if err := wrapErr(&adb.Error{Msg: "device 'x' not found"}); !errors.Is(err, driver.ErrOffline) {
		t.Errorf("wrapErr = %v", err)
	}
}

func TestMatchApp(t *testing.T) {
	apps := []driver.App{
		{ID: "com.android.settings", Name: "\u8bbe\u7f6e", System: true},
		{ID: "com.android.calculator2", Name: "Calculator", System: true},
		{ID: "com.android.deskclock", Name: "Clock", System: true},
		{ID: "com.example.cal", Name: "Calendar"},
		{ID: "com.example.clockwork", Name: "Clockwork"},
	}
	for q, want := range map[string]string{
		"com.android.settings": "com.android.settings",
		"\u8bbe\u7f6e":         "com.android.settings",
		"calculator":           "com.android.calculator2",
		"CLOCK":                "com.android.deskclock", // exact beats prefix
		"calcu":                "com.android.calculator2",
		"work":                 "com.example.clockwork",
	} {
		got, err := matchApp(apps, q)
		if err != nil || got != want {
			t.Errorf("matchApp(%q) = %q, %v; want %q", q, got, err, want)
		}
	}
	if _, err := matchApp(apps, "cal"); !errors.Is(err, errAmbiguous) {
		t.Errorf("ambiguous prefix: %v", err)
	}
	if _, err := matchApp(apps, "nothing"); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if !looksLikePackage("com.example.app") || looksLikePackage("Calculator") || looksLikePackage("a b.c") {
		t.Error("looksLikePackage")
	}
}

func TestPlainASCII(t *testing.T) {
	if !plainASCII("hello, World! 123 ~") {
		t.Error("printable ASCII rejected")
	}
	for _, s := range []string{"line\nbreak", "tab\there", "caf\u00e9", "\u4f60\u597d"} {
		if plainASCII(s) {
			t.Errorf("%q should be pasted", s)
		}
	}
}

func TestSettleTracker(t *testing.T) {
	var tr settleTracker
	t0 := time.Unix(1000, 0)
	tr.resize(t0, 1080, 2340)
	if tr.small != defaultSettleBytes(1080, 2340) || !tr.lastChange().IsZero() {
		t.Fatalf("after resize: small=%d last=%v", tr.small, tr.lastChange())
	}
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	at := func(n int) time.Time { return t0.Add(ms(n)) }
	pts := ms(5000)
	step := func(arrive, gap, size int, key bool) bool {
		pts += ms(gap)
		return tr.packet(at(arrive), pts, size, false, key)
	}
	if step(0, 0, 40000, true) {
		t.Error("a key frame counted as a change")
	}
	if step(100, 100, 30000, false) {
		t.Error("a refinement 100 ms after the previous frame counted as a change")
	}
	if !step(111, 11, 20000, false) {
		t.Error("an animation frame did not count")
	}
	if tr.lastChange() != at(111) {
		t.Errorf("last change = %v", tr.lastChange())
	}
	if step(122, 11, 100, false) {
		t.Error("a tiny packet counted as a change")
	}
	if !step(2000, 1878, 5000, false) {
		t.Error("a new frame after silence did not count")
	}
	if tr.packet(at(2001), 0, 30, true, false) {
		t.Error("configuration counted")
	}
}

func TestWaitQuiet(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	last := start.Add(-time.Hour)
	ok, err := waitQuiet(ctx, start, 50*time.Millisecond, time.Second, func() time.Time { return last }, nil)
	if err != nil || !ok {
		t.Fatalf("still screen: %v %v", ok, err)
	}
	if d := time.Since(start); d < 50*time.Millisecond || d > 500*time.Millisecond {
		t.Errorf("still screen took %v", d)
	}
	// A screen that keeps changing hits the limit.
	start = time.Now()
	ok, err = waitQuiet(ctx, start, 50*time.Millisecond, 150*time.Millisecond, time.Now, nil)
	if err != nil || ok {
		t.Fatalf("busy screen: %v %v", ok, err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := waitQuiet(cctx, time.Now(), time.Second, time.Second, time.Now, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	done := make(chan struct{})
	close(done)
	if _, err := waitQuiet(ctx, time.Now(), time.Second, time.Second, time.Now, done); err != errSessionEnded {
		t.Errorf("ended session: %v", err)
	}
}

func testLive(vw, vh int, screenSize func(int, int) (int, int)) *live {
	l := &live{cfg: &Config{}, sized: make(chan struct{}), done: make(chan struct{}), screenSize: screenSize, vw: vw, vh: vh}
	close(l.sized)
	return l
}

func TestPosition(t *testing.T) {
	ctx := context.Background()
	native := testLive(1080, 2340, func(vw, vh int) (int, int) { return orient(1080, 2340, vw, vh) })
	p, err := native.position(ctx, screen.Point{X: 540, Y: 1170})
	if err != nil || p != (scrcpy.Position{X: 540, Y: 1170, Width: 1080, Height: 2340}) {
		t.Errorf("native = %+v, %v", p, err)
	}
	// A scaled stream maps screen pixels onto the video frame.
	scaled := testLive(512, 1104, func(vw, vh int) (int, int) { return orient(1080, 2340, vw, vh) })
	p, _ = scaled.position(ctx, screen.Point{X: 1080, Y: 2340})
	if p != (scrcpy.Position{X: 511, Y: 1103, Width: 512, Height: 1104}) {
		t.Errorf("scaled corner = %+v", p)
	}
	p, _ = scaled.position(ctx, screen.Point{X: 540, Y: 1170})
	if p.X != 256 || p.Y != 552 {
		t.Errorf("scaled center = %+v", p)
	}
	// Landscape: the video is wide, so the screen is too.
	land := testLive(2340, 1080, func(vw, vh int) (int, int) { return orient(1080, 2340, vw, vh) })
	p, _ = land.position(ctx, screen.Point{X: 2000, Y: 100})
	if p.X != 2000 || p.Y != 100 || p.Width != 2340 {
		t.Errorf("landscape = %+v", p)
	}
}

func TestStreamPush(t *testing.T) {
	l := testLive(100, 100, nil)
	st := &stream{l: l, ch: make(chan driver.Packet, 4), waitStart: true}
	frame := scrcpy.Packet{Data: []byte{1}, PTS: time.Second}
	st.push(frame)
	if len(st.ch) != 0 {
		t.Fatal("a frame before the configuration was delivered")
	}
	st.push(scrcpy.Packet{Data: []byte{0}, Config: true})
	st.push(scrcpy.Packet{Data: []byte{2}, KeyFrame: true})
	st.push(frame)
	if len(st.ch) != 3 {
		t.Fatalf("delivered %d packets", len(st.ch))
	}
	if p := <-st.ch; !p.Config {
		t.Error("the stream did not start with the configuration")
	}
	st.end(errors.New("gone"))
	if _, ok := <-st.ch; !ok {
		t.Error("buffered packets were lost on end")
	}
	st.push(frame) // after end: ignored, no panic
	if st.Err() == nil {
		t.Error("no error after end")
	}
}
