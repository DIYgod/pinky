package ios

import (
	"context"
	"errors"
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
		{"16 missing developer services", "16.5", driver.SetupInstallHelper, false, true, false, driver.StateNeedsSetup},
		{"16 missing runner", "16.5", driver.SetupInstallHelper, false, false, true, driver.StateNeedsSetup},
		{"16 missing profile", "16.5", driver.SetupInstallHelper, false, true, true, driver.StateNeedsSetup},
		{"17 already running", "17.3.1", "", true, true, false, driver.StateOnline},
		{"17 installed but stopped", "17.0", driver.SetupWDAExternal, false, true, true, driver.StateNeedsSetup},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := NewProvider()
			p.usbWDAStatus = func(context.Context, goios.DeviceEntry) bool { return test.running }
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

func TestStartEarly17RequiresRunningWDA(t *testing.T) {
	s := &wdaStart{}
	err := s.startUSB(t.Context(), driver.Info{OSVersion: "17.3"}, pathExternalWDA)
	var setup *driver.SetupError
	if !errors.As(err, &setup) || setup.Setup != driver.SetupWDAExternal || !errors.Is(err, driver.ErrOffline) || s.run != nil {
		t.Fatalf("startUSB = %v, run = %v", err, s.run)
	}
}
