package ios

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/installationproxy"
	"github.com/danielpaulus/go-ios/ios/testmanagerd"
)

// The WebDriverAgent runner is a UI test bundle inside an app whose bundle
// id ends in ".xctrunner" (Xcode names it after the test target:
// <prefix>.WebDriverAgentRunner.xctrunner). The person builds and installs
// it once with their own signing team (see docs/notes/ios.md); Pinky finds
// it on the phone and starts its test through testmanagerd, the way Xcode's
// "Test" does, every time it opens the phone.

// wdaTestBundle is the runner's test bundle.
const wdaTestBundle = "WebDriverAgentRunner.xctest"

// wdaEnv is the runner's environment. WDA listens on every interface of
// the phone by default, Wi-Fi included, where anyone on the network could
// drive the phone around Pinky's approvals. Pinky reaches it through
// usbmuxd, which connects on the phone's loopback, so it binds both servers
// there.
var wdaEnv = map[string]any{
	"USE_IP":            "127.0.0.1",
	"USE_PORT":          fmt.Sprint(wdaPort),
	"MJPEG_SERVER_PORT": fmt.Sprint(mjpegPort),
}

// findWDARunner returns the bundle id of the WebDriverAgent runner
// installed on the phone, or "" when there is none.
func findWDARunner(e goios.DeviceEntry) (string, error) {
	conn, err := installationproxy.New(e)
	if err != nil {
		return "", fmt.Errorf("installation proxy: %w", err)
	}
	defer conn.Close()
	apps, err := conn.BrowseUserApps()
	if err != nil {
		return "", fmt.Errorf("list apps: %w", err)
	}
	return pickWDARunner(apps), nil
}

// pickWDARunner chooses the runner among the installed apps: a test runner
// named after WebDriverAgent. Pinky's own build wins over others.
func pickWDARunner(apps []installationproxy.AppInfo) string {
	var best string
	for _, a := range apps {
		id, _ := a["CFBundleIdentifier"].(string)
		if !strings.HasSuffix(id, ".xctrunner") {
			continue
		}
		name, _ := a["CFBundleName"].(string)
		exe, _ := a["CFBundleExecutable"].(string)
		if !strings.Contains(strings.ToLower(id+" "+name+" "+exe), "webdriveragentrunner") {
			continue
		}
		if best == "" || strings.Contains(strings.ToLower(id), "pinky") {
			best = id
		}
	}
	return best
}

// wdaRun is the test run hosting WebDriverAgent on a phone.
type wdaRun struct {
	cancel context.CancelFunc
	done   chan struct{}

	mu  sync.Mutex
	err error
}

// startWDA starts the runner's test. entry must reach the phone's
// testmanagerd: the plain usbmuxd entry below iOS 17, the tunnel's entry
// from 17.4. The run lasts until stop, or until the test ends on the phone
// (the person closed the runner, the phone locked for long, a reboot).
func startWDA(entry goios.DeviceEntry, bundleID string) *wdaRun {
	ctx, cancel := context.WithCancel(context.Background())
	r := &wdaRun{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		dir, err := os.MkdirTemp("", "pinky-wda-")
		if err == nil {
			defer os.RemoveAll(dir)
		}
		_, err = testmanagerd.RunTestWithConfig(ctx, testmanagerd.TestConfig{
			BundleId:           bundleID,
			TestRunnerBundleId: bundleID,
			XctestConfigName:   wdaTestBundle,
			Env:                wdaEnv,
			Device:             entry,
			Listener:           testmanagerd.NewTestListener(io.Discard, io.Discard, dir),
		})
		if err == nil && ctx.Err() == nil {
			err = errors.New("the WebDriverAgent test ended")
		}
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
	}()
	return r
}

// alive reports whether the run is still going, and if not, why it ended.
func (r *wdaRun) alive() (bool, error) {
	select {
	case <-r.done:
		r.mu.Lock()
		defer r.mu.Unlock()
		return false, r.err
	default:
		return true, nil
	}
}

// stop ends the run and waits a little for the phone to let go of it.
func (r *wdaRun) stop() {
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
	}
}
