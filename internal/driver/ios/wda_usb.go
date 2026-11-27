package ios

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/appservice"
	"github.com/danielpaulus/go-ios/ios/instruments"
)

const (
	wdaUSBDetail      = "Connect this iPhone to the computer with a USB cable to use WebDriverAgent."
	wdaExternalDetail = "Set USE_IP=127.0.0.1 in Xcode's test scheme, then start the signed WebDriverAgentRunner test. Without it, others on the same Wi-Fi could control and watch this iPhone. Keep the test running while Pinky is connected and stop it in Xcode afterward."
	wdaLockdownDetail = "Install Pinky on the iPhone from a Mac with Xcode and keep the iPhone connected over USB."
	lockdownXCTest    = "com.apple.testmanagerd.lockdown.secure"
)

func wdaUSBSetup(info driver.Info) driver.Info {
	info.State, info.Setup, info.Detail = driver.StateNeedsSetup, driver.SetupWDAUSB, wdaUSBDetail
	return info
}

func wdaExternalSetup(info driver.Info, kind pathKind) driver.Info {
	info.State, info.Setup, info.Detail = driver.StateNeedsSetup, driver.SetupWDAExternal, wdaExternalDetail
	if kind == pathLockdownWDA {
		info.Setup, info.Detail = driver.SetupInstallHelper, wdaLockdownDetail
	}
	return info
}

func (p *Provider) inspectUSBWDA(ctx context.Context, e goios.DeviceEntry, info driver.Info, kind pathKind) driver.Info {
	runner, ready := p.usbWDAAvailable(ctx, e, kind)
	info.Backend = backendWDA
	if !ready && kind == pathLockdownWDA {
		if runner.BundleID != "" {
			return p.legacyImageMissing(e, info)
		}
		// A phone with the image already mounted needs only the runner.
		// Check XCTest before asking for an image this Mac may not have.
		check := p.lockdownXCTestReady
		if check == nil {
			check = probeLockdownXCTest
		}
		if check(ctx, e) == nil {
			return wdaExternalSetup(info, kind)
		}
		find := p.legacyImageFind
		if find == nil {
			find = func(version string) string { return legacyImagePath(p.DiskImageDir, parseVersion(version)) }
		}
		if find(info.OSVersion) == "" {
			return wdaImageSetup(info, fmt.Errorf("no compatible developer disk image for iOS %s: %w", info.OSVersion, errNoDiskImage))
		}
	}
	switch {
	case !ready:
		return wdaExternalSetup(info, kind)
	case runner.BundleID != "":
		return p.withHelper(ctx, e, info, runner)
	default:
		return info
	}
}

func wdaImageSetup(info driver.Info, err error) driver.Info {
	info.State, info.Setup = driver.StateNeedsSetup, driver.SetupDiskImage
	if errors.Is(err, errNoDiskImage) {
		info.Setup = driver.SetupNoDiskImage
	}
	info.Detail = err.Error()
	return info
}

// usbWDAAvailable checks an already-running server first. On iOS 16 only,
// a signed runner and the lockdown test service are enough to start it.
func (p *Provider) usbWDAAvailable(ctx context.Context, e goios.DeviceEntry, kind pathKind) (wdaRunner, bool) {
	probe := p.usbWDAStatus
	if probe == nil {
		probe = func(ctx context.Context, e goios.DeviceEntry) bool {
			return (&wdaStart{c: newWDAUSB(e)}).ready(ctx)
		}
	}
	if probe(ctx, e) {
		return wdaRunner{}, true
	}
	if kind != pathLockdownWDA {
		return wdaRunner{}, false
	}
	runner, err := bounded(ctx, lockdownTimeout, func() (wdaRunner, error) { return p.runnerApp(e) })
	if err != nil || runner.BundleID == "" {
		return wdaRunner{}, false
	}
	check := p.lockdownXCTestReady
	if check == nil {
		check = probeLockdownXCTest
	}
	return runner, check(ctx, e) == nil
}

func probeLockdownXCTest(ctx context.Context, e goios.DeviceEntry) error {
	_, err := bounded(ctx, 5*time.Second, func() (bool, error) {
		conn, err := goios.ConnectToService(e, lockdownXCTest)
		if err != nil {
			return false, err
		}
		return true, conn.Close()
	})
	return err
}

func usbWDAStartError(info driver.Info, kind pathKind, err error) error {
	info = wdaExternalSetup(info, kind)
	return &driver.SetupError{Setup: info.Setup, Detail: info.Detail, Err: errors.Join(driver.ErrOffline, err)}
}

func (s *wdaStart) startUSB(ctx context.Context, info driver.Info, kind pathKind) error {
	if kind == pathExternalWDA {
		return usbWDAStartError(info, kind, errors.New("WebDriverAgent is not running"))
	}
	app, err := findRunnerApp(s.base)
	if err != nil {
		return usbWDAStartError(info, kind, err)
	}
	if app.BundleID == "" {
		return usbWDAStartError(info, kind, errors.New("WebDriverAgentRunner is not installed"))
	}
	if err := probeLockdownXCTest(ctx, s.base); err != nil {
		if err := prepareLegacyImage(ctx, s.base, info.OSVersion, s.imageDir); err != nil {
			setup := wdaImageSetup(info, err)
			return &driver.SetupError{Setup: setup.Setup, Detail: setup.Detail, Err: errors.Join(driver.ErrOffline, err)}
		}
	}
	s.run = startWDA(s.base, s.base, app, wdaBind(s.base, nil), true)
	return nil
}

// The old Instruments service reports executable paths for the same exact
// bundle-path check used on newer iOS. It also stops a leftover runner
// when a session disconnected before go-ios could stop it.
type lockdownRunnerProcs struct {
	info *instruments.DeviceInfoService
	ctrl *instruments.ProcessControl
}

func (p lockdownRunnerProcs) ListProcesses() ([]appservice.Process, error) {
	list, err := p.info.ProcessList()
	if err != nil {
		return nil, err
	}
	out := make([]appservice.Process, 0, len(list))
	for _, proc := range list {
		out = append(out, appservice.Process{Pid: int(proc.Pid), Path: proc.RealAppName})
	}
	return out, nil
}

func (p lockdownRunnerProcs) KillProcess(pid int) error { return p.ctrl.KillProcess(uint64(pid)) }

func (p lockdownRunnerProcs) Close() error {
	p.info.Close()
	return p.ctrl.Close()
}

func endLockdownRunner(ctx context.Context, e goios.DeviceEntry, appPath string) (int, error) {
	return bounded(ctx, wdaEndTimeout, func() (int, error) {
		info, err := instruments.NewDeviceInfoService(e)
		if err != nil {
			return 0, err
		}
		ctrl, err := instruments.NewProcessControl(e)
		if err != nil {
			info.Close()
			return 0, err
		}
		procs := lockdownRunnerProcs{info: info, ctrl: ctrl}
		defer procs.Close()
		return killRunnerProcesses(ctx, procs, appPath)
	})
}
