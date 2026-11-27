package ios

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	goios "github.com/danielpaulus/go-ios/ios"
)

func TestInspectUSBWDA(t *testing.T) {
	for _, test := range []struct {
		name, version, wantSetup  string
		running, installed, ready bool
		wantState                 driver.State
	}{
		{"16 installed", "16.5", "", false, true, true, driver.StateOnline},
		{"16 missing developer services", "16.5", driver.SetupPreparing, false, true, false, driver.StateNeedsSetup},
		{"16 missing runner", "16.5", driver.SetupInstallHelper, false, false, true, driver.StateNeedsSetup},
		{"16 missing profile", "16.5", driver.SetupInstallHelper, false, true, true, driver.StateNeedsSetup},
		{"17 already running", "17.3.1", "", true, true, false, driver.StateOnline},
		{"17 installed but stopped", "17.0", driver.SetupWDAExternal, false, true, true, driver.StateNeedsSetup},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := NewProvider()
			p.usbWDAStatus = func(context.Context, goios.DeviceEntry) bool { return test.running }
			p.legacyImageFind = func(string) string { return "/tmp/test/DeveloperDiskImage.dmg" }
			p.legacyImagePrepare = func(context.Context, goios.DeviceEntry, string, string) error {
				return errors.New("the disk image could not be mounted")
			}
			p.findRunner = func(goios.DeviceEntry) (string, error) {
				if test.installed {
					return "com.example.WebDriverAgentRunner.xctrunner", nil
				}
				return "", nil
			}
			p.lockdownXCTestReady = func(context.Context, goios.DeviceEntry) error {
				if !test.ready {
					return errors.New("testmanagerd is unavailable")
				}
				return nil
			}
			p.helperFn = func(context.Context, goios.DeviceEntry, wdaRunner) (time.Time, error) {
				if test.name == "16 missing profile" {
					return time.Time{}, nil
				}
				return time.Now().Add(time.Hour), nil
			}
			kind, _ := pathFor(parseVersion(test.version))
			info := driver.Info{ID: "usb-test", OSVersion: test.version, Connection: "usb", State: driver.StateOnline}
			got := p.inspectUSBWDA(t.Context(), goios.DeviceEntry{}, info, kind)
			if got.State != test.wantState || got.Setup != test.wantSetup || got.Backend != backendWDA {
				t.Fatalf("inspectUSBWDA = %+v", got)
			}
			if test.version == "16.5" && got.State == driver.StateOnline && got.HelperExpires.IsZero() {
				t.Error("the installed runner's signing expiration was not reported")
			}
		})
	}
}

func TestInspectUSBWDARemountsAfterRestart(t *testing.T) {
	p := NewProvider()
	p.usbWDAStatus = func(context.Context, goios.DeviceEntry) bool { return false }
	p.findRunner = func(goios.DeviceEntry) (string, error) {
		return "com.example.WebDriverAgentRunner.xctrunner", nil
	}
	var mounted atomic.Bool
	p.lockdownXCTestReady = func(context.Context, goios.DeviceEntry) error {
		if mounted.Load() {
			return nil
		}
		return errors.New("image unmounted")
	}
	p.legacyImageFind = func(string) string { return "/tmp/test/DeveloperDiskImage.dmg" }
	p.legacyImagePrepare = func(context.Context, goios.DeviceEntry, string, string) error { mounted.Store(true); return nil }
	p.helperFn = func(context.Context, goios.DeviceEntry, wdaRunner) (time.Time, error) {
		return time.Now().Add(time.Hour), nil
	}
	info := driver.Info{OSVersion: "16.5.1", State: driver.StateOnline}
	got := p.inspectUSBWDA(t.Context(), goios.DeviceEntry{}, info, pathLockdownWDA)
	if got.Setup != driver.SetupPreparing {
		t.Fatalf("inspect after reboot = %+v; want preparing", got)
	}
	deadline := time.Now().Add(time.Second)
	for !mounted.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !mounted.Load() {
		t.Fatal("background mount did not run")
	}
	got = p.inspectUSBWDA(t.Context(), goios.DeviceEntry{}, info, pathLockdownWDA)
	if got.State != driver.StateOnline {
		t.Fatalf("after background mount = %+v", got)
	}
	mounted.Store(false)
	p.prep.forget("")
	p.legacyImageFind = func(string) string { return "" }
	got = p.inspectUSBWDA(t.Context(), goios.DeviceEntry{}, info, pathLockdownWDA)
	if got.Setup != driver.SetupPreparing {
		t.Fatalf("missing image initially = %+v", got)
	}
	for time.Now().Before(deadline) {
		got = p.inspectUSBWDA(t.Context(), goios.DeviceEntry{}, info, pathLockdownWDA)
		if got.Setup == driver.SetupNoDiskImage {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("missing image = %+v", got)
}

func TestInspectUSBWDAMountedWithoutRunner(t *testing.T) {
	p := NewProvider()
	p.usbWDAStatus = func(context.Context, goios.DeviceEntry) bool { return false }
	p.findRunner = func(goios.DeviceEntry) (string, error) { return "", nil }
	p.lockdownXCTestReady = func(context.Context, goios.DeviceEntry) error { return nil }
	p.legacyImageFind = func(string) string { t.Fatal("looked for an image already mounted"); return "" }
	info := driver.Info{OSVersion: "16.5.1", State: driver.StateOnline}
	got := p.inspectUSBWDA(t.Context(), goios.DeviceEntry{}, info, pathLockdownWDA)
	if got.Setup != driver.SetupInstallHelper {
		t.Fatalf("mounted image without runner = %+v", got)
	}
}

func TestInspectUSBWDABackgroundMountDoesNotOverlap(t *testing.T) {
	p := NewProvider()
	var logs syncBuffer
	p.Log = slog.New(slog.NewTextHandler(&logs, nil))
	p.usbWDAStatus = func(context.Context, goios.DeviceEntry) bool { return false }
	p.findRunner = func(goios.DeviceEntry) (string, error) { return "com.example.WebDriverAgentRunner.xctrunner", nil }
	p.lockdownXCTestReady = func(context.Context, goios.DeviceEntry) error { return errors.New("image unmounted") }
	p.legacyImageFind = func(string) string { return "/tmp/test/DeveloperDiskImage.dmg" }
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var calls atomic.Int32
	p.legacyImagePrepare = func(context.Context, goios.DeviceEntry, string, string) error {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return errors.New("ReceiveBytes: device responded with error: DeviceLocked")
	}
	info := driver.Info{OSVersion: "16.5.1", State: driver.StateOnline}
	got := p.inspectUSBWDA(t.Context(), goios.DeviceEntry{}, info, pathLockdownWDA)
	if got.Setup != driver.SetupPreparing {
		t.Fatalf("first inspection = %+v", got)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background mount did not start")
	}
	got = p.inspectUSBWDA(t.Context(), goios.DeviceEntry{}, info, pathLockdownWDA)
	if got.Setup != driver.SetupPreparing || calls.Load() != 1 {
		t.Fatalf("second inspection started another mount: %+v, calls=%d", got, calls.Load())
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got = p.inspectUSBWDA(t.Context(), goios.DeviceEntry{}, info, pathLockdownWDA)
		if got.Setup == driver.SetupUnlock {
			if calls.Load() != 1 {
				t.Fatalf("mount calls = %d", calls.Load())
			}
			if s := logs.String(); !strings.Contains(s, "mounting the iOS 16 developer disk image") ||
				!strings.Contains(s, "could not mount the iOS 16 developer disk image") || !strings.Contains(s, "retry_in=3s") {
				t.Fatalf("log = %q", s)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("locked phone = %+v; want unlock", got)
}

func TestWDAImageSetupLocked(t *testing.T) {
	info := wdaImageSetup(driver.Info{}, errors.New("ReceiveBytes: device responded with error: DeviceLocked"))
	if info.Setup != driver.SetupUnlock || info.Detail != unlockDetail {
		t.Fatalf("locked iPhone = %+v", info)
	}
}

func TestWDAImageSetupNoImage(t *testing.T) {
	info := wdaImageSetup(driver.Info{OSVersion: "16.5.1"}, fmt.Errorf("no compatible developer disk image for iOS 16.5.1: %w", errNoDiskImage))
	if info.Setup != driver.SetupNoDiskImage || !strings.Contains(info.Detail, "iOS 16.5.1") ||
		!strings.Contains(info.Detail, "Add phone › iPhone") || !strings.Contains(info.Detail, mirrorName) {
		t.Fatalf("missing iOS 16 image = %+v", info)
	}
}

func TestStartEarly17RequiresRunningWDA(t *testing.T) {
	s := &wdaStart{}
	err := s.startUSB(t.Context(), driver.Info{OSVersion: "17.3"}, pathExternalWDA)
	var setup *driver.SetupError
	if !errors.As(err, &setup) || setup.Setup != driver.SetupWDAExternal || !errors.Is(err, driver.ErrOffline) || s.run != nil {
		t.Fatalf("startUSB = %v, run = %v", err, s.run)
	}
}
