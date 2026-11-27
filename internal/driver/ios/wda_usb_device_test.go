//go:build device

package ios

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// Put the unlocked phone on the Settings root screen before running this.
// Install a signed WDA runner and mount its developer disk image on iOS 16,
// with its test stopped. On iOS 17.0–17.3 leave the test running in Xcode.
//
//	PINKY_DEVICE=<udid> scripts/devlock.sh <udid> go test -tags device -run '^TestDeviceUSBWDA$' -v ./internal/driver/ios
func TestDeviceUSBWDA(t *testing.T) {
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("PINKY_DEVICE is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	e, err := findEntry(id, false)
	if err != nil || e.Properties.ConnectionType != "USB" {
		t.Fatalf("USB connection required: %v", err)
	}
	values, err := getValues(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	kind, _ := pathFor(parseVersion(values.Value.ProductVersion))
	if !kind.usbWDAOnly() {
		t.Skip("this test needs iOS 16 to 17.3")
	}
	if values.Value.PasswordProtected {
		t.Skip("unlock the phone first")
	}
	p := deviceProvider(t, id)
	defer p.Close()
	dev, err := p.Open(ctx, id)
	if errors.Is(err, errAutomation) {
		t.Log("waiting for the person to allow UI automation on the iPhone")
		for ctx.Err() == nil {
			if p.info(ctx, e).State == driver.StateOnline {
				dev, err = p.Open(ctx, id)
				if !errors.Is(err, errAutomation) {
					break
				}
			}
			time.Sleep(time.Second)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	wda, ok := dev.(*WDADevice)
	if !ok || wda.l != nil || (kind != pathLockdownWDA && wda.run != nil) {
		t.Fatalf("expected %v WebDriverAgent over USB (runner started here: %t)", kind, ok && wda.run != nil)
	}
	t.Logf("opened iOS %s through WebDriverAgent over USB (started runner: %t)", wda.info.OSVersion, wda.run != nil)
	app, err := wda.c.activeApp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if app.BundleID != "com.apple.Preferences" {
		if app.BundleID != "com.apple.springboard" && (wda.run == nil || app.BundleID != wda.run.app.BundleID) {
			t.Skip("leave the phone on the Settings root screen before testing navigation")
		}
		if err := wda.Launch(ctx, "com.apple.Preferences"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	observe := func() *screen.Screen {
		t.Helper()
		s, err := wda.Observe(ctx, driver.ObserveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if s.Locked || s.App.ID != "com.apple.Preferences" {
			t.Fatal("the phone locked or left Settings")
		}
		return s
	}
	find := func(s *screen.Screen, names ...string) screen.Element {
		for _, e := range s.Elements {
			for _, name := range names {
				if e.Label == name && !e.Bounds.Empty() {
					return e
				}
			}
		}
		return screen.Element{}
	}
	s := observe()
	if shot, err := wda.Screenshot(ctx); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("Settings: %d elements, screenshot %dx%d", len(s.Elements), shot.Bounds().Dx(), shot.Bounds().Dy())
	}
	general := find(s, "General", "通用", "一般")
	if general.Bounds.Empty() {
		t.Skip("the General row must be visible on the Settings root screen")
	}
	time.Sleep(400 * time.Millisecond)
	if err := wda.Tap(ctx, general.Bounds.Center(), driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if find(observe(), "About", "关于本机", "情報").Bounds.Empty() {
		t.Fatal("tapping General did not show its About row")
	}
	time.Sleep(400 * time.Millisecond)
	if err := wda.Key(ctx, driver.KeyBack); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if find(observe(), "General", "通用", "一般").Bounds.Empty() {
		t.Fatal("Back did not return to the Settings root screen")
	}
	t.Log("tapped General and returned to Settings")
}
