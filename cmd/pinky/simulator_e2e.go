//go:build e2e

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/ios"
)

// In the e2e build, PINKY_WDA_URL adds a WebDriverAgent server, such as one
// running on the iOS Simulator, as an iPhone, so the desktop app can be
// checked and filmed with a simulator. Release builds never list one.
//
//	PINKY_WDA_URL=http://127.0.0.1:18200 PINKY_MJPEG_URL=http://127.0.0.1:19200 \
//	  PINKY_WDA_NAME="Test iPhone" PINKY_WDA_OS=26.5 PINKY_SIM_UDID=<udid> build/pinky-e2e
//
// The phone's id is the URL, so PINKY_DEVICES can name it. With
// PINKY_WDA_ONLY=1 it is the only phone: Pinky does not look for Android
// phones or for iPhones on USB at all. PINKY_SIM_UDID names the simulator,
// whose app list (from simctl) lets agents open apps by name, as on a phone.
func init() {
	url := os.Getenv("PINKY_WDA_URL")
	if url == "" {
		return
	}
	name := os.Getenv("PINKY_WDA_NAME")
	if name == "" {
		name = "iPhone Simulator"
	}
	p := &wdaURLProvider{
		info: driver.Info{
			ID:         url,
			Platform:   driver.IOS,
			Name:       name,
			Model:      os.Getenv("PINKY_WDA_MODEL"),
			OSVersion:  os.Getenv("PINKY_WDA_OS"),
			Connection: "usb",
			Backend:    "wda",
			State:      driver.StateOnline,
		},
		mjpeg: os.Getenv("PINKY_MJPEG_URL"),
		udid:  os.Getenv("PINKY_SIM_UDID"),
	}
	factory := func(*slog.Logger) (driver.Provider, error) { return p, nil }
	if os.Getenv("PINKY_WDA_ONLY") != "" {
		providerFactories = []func(*slog.Logger) (driver.Provider, error){factory}
		return
	}
	providerFactories = append(providerFactories, factory)
}

// wdaURLProvider lists one iPhone: the WebDriverAgent server at a URL.
type wdaURLProvider struct {
	info  driver.Info
	mjpeg string
	udid  string
}

func (p *wdaURLProvider) Platform() driver.Platform { return driver.IOS }

func (p *wdaURLProvider) List(context.Context) ([]driver.Info, error) {
	return []driver.Info{p.info}, nil
}

func (p *wdaURLProvider) Open(ctx context.Context, id string) (driver.Device, error) {
	if id != p.info.ID {
		return nil, fmt.Errorf("%s: %w", id, driver.ErrNotFound)
	}
	d, err := ios.OpenWDAURL(ctx, p.info.ID, p.mjpeg)
	if err != nil || p.udid == "" {
		return d, err
	}
	apps, err := simApps(ctx, p.udid)
	if err != nil {
		return d, nil // apps open by bundle id only, as without a UDID
	}
	return &simDevice{WDADevice: d, apps: apps}, nil
}

// simDevice is a simulator's WDA device that knows the simulator's apps,
// which a phone reports through its installation proxy. Every other method,
// optional ones included, is the WDA device's own.
type simDevice struct {
	*ios.WDADevice
	apps []driver.App
}

func (d *simDevice) Apps(context.Context) ([]driver.App, error) { return d.apps, nil }

func (d *simDevice) Launch(ctx context.Context, app string) error {
	want := strings.TrimSpace(app)
	for _, a := range d.apps {
		if strings.EqualFold(a.Name, want) {
			want = a.ID
			break
		}
	}
	return d.WDADevice.Launch(ctx, want)
}

// simApps lists a simulator's apps with their names, through simctl.
func simApps(ctx context.Context, udid string) ([]driver.App, error) {
	raw, err := exec.CommandContext(ctx, "xcrun", "simctl", "listapps", udid).Output()
	if err != nil {
		return nil, fmt.Errorf("simctl listapps: %w", err)
	}
	conv := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", "-")
	conv.Stdin = bytes.NewReader(raw)
	js, err := conv.Output()
	if err != nil {
		return nil, fmt.Errorf("plutil: %w", err)
	}
	var m map[string]struct {
		CFBundleDisplayName string
		CFBundleName        string
		ApplicationType     string
	}
	if err := json.Unmarshal(js, &m); err != nil {
		return nil, err
	}
	apps := make([]driver.App, 0, len(m))
	for id, a := range m {
		name := a.CFBundleDisplayName
		if name == "" {
			name = a.CFBundleName
		}
		apps = append(apps, driver.App{ID: id, Name: name, System: a.ApplicationType == "System"})
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	return apps, nil
}
