//go:build device

// Tests against a real iPhone with iOS 27. Run them while holding the
// device's lock, for example:
//
//	PINKY_DEVICE=<udid> scripts/devlock.sh <udid> go test -tags device -count=1 ./internal/driver/ios/
//
// They use only the home screen, Settings (reading), Calculator and Clock,
// pace their input, and leave the phone on the home screen.
package ios

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

var shared struct {
	once sync.Once
	d    *Device
	err  error
}

// testDevice opens one session for all tests: opening one per test would
// start and stop the device's media stream again and again.
func testDevice(t *testing.T) *Device {
	t.Helper()
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("PINKY_DEVICE not set")
	}
	shared.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		shared.d, shared.err = Open(ctx, driver.Info{ID: id, Platform: driver.IOS})
	})
	if shared.err != nil {
		t.Fatal(shared.err)
	}
	return shared.d
}

func TestMain(m *testing.M) {
	code := m.Run()
	if d := shared.d; d != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		d.Key(ctx, driver.KeyHome)
		cancel()
		time.Sleep(time.Second)
		d.Close()
	}
	os.Exit(code)
}

func ctxFor(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func pause() { time.Sleep(400 * time.Millisecond) }

// home presses Home and waits for SpringBoard to finish with it: a launch
// sent while the press is still being handled is undone by it.
func home(ctx context.Context, d *Device) {
	d.Key(ctx, driver.KeyHome)
	time.Sleep(1200 * time.Millisecond)
}

// TestDeviceInspect checks what the provider reports for the test device,
// without listing (and so probing) other phones.
func TestDeviceInspect(t *testing.T) {
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("PINKY_DEVICE not set")
	}
	entry, err := findEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	info := NewProvider().inspect(ctxFor(t, 30*time.Second), entry)
	t.Logf("%+v", info)
	if info.State != driver.StateOnline || info.Name == "" || info.Model == "" || major(info.OSVersion) < minIOS || info.Connection != "usb" {
		t.Fatalf("not ready: %+v", info)
	}
}

func TestDeviceStream(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, 30*time.Second)
	s, err := d.Stream(ctx, driver.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Codec() != driver.HEVC {
		t.Fatalf("codec %s", s.Codec())
	}
	w, h := s.Size()
	if w < 500 || h < 1000 {
		t.Fatalf("size %dx%d", w, h)
	}
	var frames, keys int
	first := true
	deadline := time.After(4 * time.Second)
loop:
	for {
		select {
		case p, ok := <-s.Packets():
			if !ok {
				t.Fatalf("stream ended: %v", s.Err())
			}
			if first {
				if !p.Config {
					t.Fatal("the first packet is not the codec configuration")
				}
				first = false
				continue
			}
			if frames == 0 && !p.KeyFrame {
				t.Fatal("the first frame is not a keyframe")
			}
			if p.KeyFrame {
				keys++
			}
			frames++
		case <-deadline:
			break loop
		}
	}
	st, _ := d.StreamStats()
	t.Logf("%dx%d, %d frames in about 4 s (%d keyframes); %+v", w, h, frames, keys, st)
	if frames < 30 {
		t.Fatalf("only %d frames", frames)
	}
}

// changed waits up to max for a video frame clearly larger than the idle
// ones, which means the screen moved.
func changed(d *Device, since time.Time, max time.Duration) bool {
	d.mu.Lock()
	m := d.m
	d.mu.Unlock()
	time.Sleep(max)
	base := 0
	for _, f := range m.framesSince(since.Add(-time.Second)) {
		if f.at.After(since) {
			break
		}
		if base == 0 || f.size < base {
			base = f.size
		}
	}
	limit := 4 * base
	if limit < 4096 {
		limit = 4096
	}
	for _, f := range m.framesSince(since) {
		if f.size > limit {
			return true
		}
	}
	return false
}

func TestDeviceHomeAndObserve(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Key(ctx, driver.KeyHome); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	s, st, err := d.ObserveWithStats(ctx, driver.ObserveOptions{OCR: true})
	if err != nil {
		t.Fatal(err)
	}
	screen.NewRefs().Assign(s)
	t.Logf("%+v\n%s", st, s.Render())
	if s.App.ID != springBoard || s.Width < 500 {
		t.Fatalf("header %s", s.Header())
	}
	if st.Elements < 5 || st.Placed == 0 {
		t.Fatalf("elements %d, placed %d", st.Elements, st.Placed)
	}
}

func TestDeviceSwipeAndTouch(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Key(ctx, driver.KeyHome); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	si := d.screen()
	left := screen.Point{X: si.Width * 8 / 10, Y: si.Height / 2}
	right := screen.Point{X: si.Width * 2 / 10, Y: si.Height / 2}

	at := time.Now()
	if err := d.Swipe(ctx, left, right, 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !changed(d, at, time.Second) {
		t.Error("the screen did not move after a swipe")
	}
	pause()

	// The same gesture back, as raw touches. Their points are in video pixels,
	// which on iPhone equal screen pixels inside the screen.
	at = time.Now()
	if err := d.Touch(ctx, driver.TouchDown, right); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		time.Sleep(15 * time.Millisecond)
		p := screen.Point{X: right.X + (left.X-right.X)*i/20, Y: right.Y}
		if err := d.Touch(ctx, driver.TouchMove, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Touch(ctx, driver.TouchUp, left); err != nil {
		t.Fatal(err)
	}
	if !changed(d, at, time.Second) {
		t.Error("the screen did not move after raw touches")
	}
	pause()
	home(ctx, d)
}

func TestDeviceApps(t *testing.T) {
	d := testDevice(t)
	apps, err := d.Apps(ctxFor(t, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, a := range apps {
		ids[a.ID] = true
	}
	for _, want := range []string{"com.apple.Preferences", "com.apple.calculator", "com.apple.mobiletimer"} {
		if !ids[want] {
			t.Errorf("%s missing from %d apps", want, len(apps))
		}
	}
}

func TestDeviceLaunchSettings(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Launch(ctx, "Settings"); err != nil { // by display name
		t.Fatal(err)
	}
	settled, err := d.WaitSettled(ctx, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	s, st, err := d.ObserveWithStats(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("settled=%v %+v\n%s", settled, st, s.Render())
	if s.App.ID != "com.apple.Preferences" {
		t.Fatalf("app %q", s.App.ID)
	}
	if len(s.Elements) == 0 {
		t.Fatal("no elements")
	}
	pause()
	home(ctx, d)
}

func TestDeviceCalculatorTap(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Launch(ctx, "com.apple.calculator"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	s, err := d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("\n%s", s.Render())
	if s.App.ID != "com.apple.calculator" {
		t.Fatalf("app %q", s.App.ID)
	}
	var key screen.Element
	for _, e := range s.Elements {
		if (e.Label == "7" || e.Label == "8" || e.Label == "4") && !e.Bounds.Empty() {
			key = e
			break
		}
	}
	if key.Bounds.Empty() {
		t.Skip("no digit key placed by OCR on this screen")
	}
	pause()
	at := time.Now()
	if err := d.Tap(ctx, key.Bounds.Center(), driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
	if !changed(d, at, time.Second) {
		t.Errorf("tapping %q changed nothing", key.Label)
	}
	pause()
	home(ctx, d)
}

func TestDeviceScreenshot(t *testing.T) {
	d := testDevice(t)
	img, err := d.Screenshot(ctxFor(t, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	si := d.screen()
	if b := img.Bounds(); b.Dx() != si.Width || b.Dy() != si.Height {
		t.Fatalf("screenshot %v, screen %dx%d", b, si.Width, si.Height)
	}
}

func TestDeviceLocked(t *testing.T) {
	d := testDevice(t)
	locked, err := d.Locked(ctxFor(t, 10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		t.Fatal("the test phone reports locked; stop and check it")
	}
}

func TestDeviceKeys(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, 10*time.Second)
	if err := d.Key(ctx, driver.KeyRecents); err == nil || !strings.Contains(err.Error(), driver.ErrUnsupported.Error()) {
		t.Fatalf("recents: %v", err)
	}
	if err := d.Type(ctx, "你好", driver.TypeOptions{}); err == nil {
		t.Fatal("non-ASCII text accepted")
	}
}

// TestDeviceSwipeLatency reports how long a swipe takes to show up in the
// video.
func TestDeviceSwipeLatency(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Key(ctx, driver.KeyHome); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	lat, err := d.SwipeLatency(ctx, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	t.Logf("touch-down to changed frame: %v", lat)
	if len(lat) < 2 {
		t.Fatalf("only %d of 4 swipes showed up in the video", len(lat))
	}
}

// TestDeviceType types into Settings' search field, the one text field the
// safety rules allow, and clears it again.
func TestDeviceType(t *testing.T) {
	d := testDevice(t)
	ctx := ctxFor(t, time.Minute)
	if err := d.Launch(ctx, "com.apple.Preferences"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	s, err := d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var field screen.Element
	for _, e := range s.Elements {
		if e.Role == screen.RoleInput && e.Label == "Search" && !e.Bounds.Empty() {
			field = e
		}
	}
	if s.App.ID != "com.apple.Preferences" || field.Bounds.Empty() {
		t.Skip("Settings' search field is not on screen")
	}
	defer home(ctx, d)
	if err := d.Tap(ctx, field.Bounds.Center(), driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if err := d.Type(ctx, "clock", driver.TypeOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	s, err = d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	typed := false
	for _, e := range s.Elements {
		if e.Role == screen.RoleInput && strings.ReplaceAll(e.Label, "\u2006", "") == "clock" {
			typed = true
		}
	}
	// The letters may still be an input method's composition (Pinyin, on
	// the test phone), which Escape cancels. Settings stays in its search
	// view, empty: the test taps nothing it has not just observed.
	d.Key(ctx, driver.KeyEscape)
	pause()
	if !typed {
		t.Fatalf("the field does not read \"clock\":\n%s", s.Render())
	}
}
