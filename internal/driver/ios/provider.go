package ios

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/imagemounter"
)

// minIOS is the first iOS whose CoreDevice services stream the screen and
// take HID input.
const minIOS = 27

// Provider discovers iPhones through usbmuxd (Apple Devices on Windows,
// usbmuxd on Linux, built in on macOS).
type Provider struct {
	mu    sync.Mutex
	ddi   map[string]ddiCheck // by UDID
	setup func(ctx context.Context, e goios.DeviceEntry) (bool, error)
}

type ddiCheck struct {
	ok  bool
	at  time.Time
	err string
}

// NewProvider returns an iOS provider.
func NewProvider() *Provider {
	return &Provider{ddi: map[string]ddiCheck{}, setup: probeServices}
}

// Platform implements driver.Provider.
func (p *Provider) Platform() driver.Platform { return driver.IOS }

// List implements driver.Provider. It checks what Pinky needs and says in
// Detail how to fix what is missing.
func (p *Provider) List(ctx context.Context) ([]driver.Info, error) {
	list, err := goios.ListDevices()
	if err != nil {
		return nil, fmt.Errorf("usbmuxd: %w (on Windows install Apple Devices, on Linux the usbmuxd package)", err)
	}
	// A phone on USB and Wi-Fi at once is listed twice; USB wins.
	byID := map[string]goios.DeviceEntry{}
	for _, e := range list.DeviceList {
		id := e.Properties.SerialNumber
		if prev, ok := byID[id]; ok && prev.Properties.ConnectionType == "USB" {
			continue
		}
		byID[id] = e
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]driver.Info, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = p.inspect(ctx, byID[id])
		}()
	}
	wg.Wait()
	return out, nil
}

func (p *Provider) inspect(ctx context.Context, e goios.DeviceEntry) driver.Info {
	info := driver.Info{
		ID:         e.Properties.SerialNumber,
		Platform:   driver.IOS,
		Connection: "usb",
		Backend:    "coredevice",
		State:      driver.StateOnline,
	}
	if e.Properties.ConnectionType == "Network" {
		info.Connection = "wifi"
	}
	values, err := goios.GetValues(e)
	if err != nil {
		info.State = driver.StateUnauthorized
		info.Detail = "Unlock the iPhone and tap Trust on the \"Trust This Computer?\" prompt, then enter its passcode."
		if !isPairingError(err) {
			info.State = driver.StateOffline
			info.Detail = "The iPhone does not answer: " + err.Error()
		}
		return info
	}
	info.Name = values.Value.DeviceName
	info.Model = modelName(values.Value.ProductType)
	info.OSVersion = values.Value.ProductVersion
	if major(info.OSVersion) < minIOS {
		info.State = driver.StateNeedsSetup
		info.Detail = fmt.Sprintf("Pinky needs iOS %d or later to show and control an iPhone without installing anything on it; this one runs iOS %s.", minIOS, info.OSVersion)
		return info
	}
	dev, err := imagemounter.IsDevModeEnabled(e)
	if err == nil && !dev {
		info.State = driver.StateNeedsSetup
		info.Detail = "Turn on Developer Mode on the iPhone (Settings > Privacy & Security > Developer Mode), restart it and confirm."
		return info
	}
	ok, err := p.services(ctx, e)
	switch {
	case err != nil:
		info.State = driver.StateOffline
		info.Detail = "Could not reach the iPhone's developer services: " + err.Error()
	case !ok:
		info.State = driver.StateNeedsSetup
		info.Detail = "The developer disk image is not mounted. Connect the iPhone to this computer and open Xcode's Devices window (Window > Devices and Simulators) once, so Xcode prepares it for development."
	}
	return info
}

// services reports whether the screen and HID services are published, which
// they are once the developer disk image is mounted. It needs a tunnel, so
// results are cached: a mounted image stays mounted until the phone restarts.
func (p *Provider) services(ctx context.Context, e goios.DeviceEntry) (bool, error) {
	id := e.Properties.SerialNumber
	p.mu.Lock()
	c, ok := p.ddi[id]
	p.mu.Unlock()
	if ok && ((c.ok && time.Since(c.at) < 5*time.Minute) || (!c.ok && time.Since(c.at) < 10*time.Second)) {
		if c.err != "" {
			return false, errors.New(c.err)
		}
		return c.ok, nil
	}
	ok, err := p.setup(ctx, e)
	c = ddiCheck{ok: ok, at: time.Now()}
	if err != nil {
		c.err = err.Error()
	}
	p.mu.Lock()
	p.ddi[id] = c
	p.mu.Unlock()
	return ok, err
}

func probeServices(ctx context.Context, e goios.DeviceEntry) (bool, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	l, err := dialLink(cctx, e.Properties.SerialNumber)
	if err != nil {
		return false, err
	}
	defer l.Close()
	return l.has(displayService) && l.has(universalHIDService), nil
}

// Open implements driver.Provider. It checks only the phone it opens.
func (p *Provider) Open(ctx context.Context, id string) (driver.Device, error) {
	entry, err := findEntry(id)
	if err != nil {
		return nil, fmt.Errorf("%v: %w", err, driver.ErrNotFound)
	}
	info := p.inspect(ctx, entry)
	if info.State != driver.StateOnline {
		return nil, fmt.Errorf("%s is %s: %s", id, info.State, info.Detail)
	}
	return Open(ctx, info)
}

func isPairingError(err error) bool {
	m := strings.ToLower(err.Error())
	for _, s := range []string{"pair", "invalidhostid", "passwordprotected", "trust", "sslhandshake"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

func major(version string) int {
	v, _, _ := strings.Cut(version, ".")
	n, _ := strconv.Atoi(v)
	return n
}

// modelName turns a product type into a marketing name, for the models
// Pinky can drive (iOS 27 runs on iPhone 11 and later).
func modelName(productType string) string {
	if n, ok := models[productType]; ok {
		return n
	}
	return productType
}

var models = map[string]string{
	"iPhone12,1": "iPhone 11", "iPhone12,3": "iPhone 11 Pro", "iPhone12,5": "iPhone 11 Pro Max", "iPhone12,8": "iPhone SE (2nd generation)",
	"iPhone13,1": "iPhone 12 mini", "iPhone13,2": "iPhone 12", "iPhone13,3": "iPhone 12 Pro", "iPhone13,4": "iPhone 12 Pro Max",
	"iPhone14,4": "iPhone 13 mini", "iPhone14,5": "iPhone 13", "iPhone14,2": "iPhone 13 Pro", "iPhone14,3": "iPhone 13 Pro Max",
	"iPhone14,6": "iPhone SE (3rd generation)", "iPhone14,7": "iPhone 14", "iPhone14,8": "iPhone 14 Plus",
	"iPhone15,2": "iPhone 14 Pro", "iPhone15,3": "iPhone 14 Pro Max", "iPhone15,4": "iPhone 15", "iPhone15,5": "iPhone 15 Plus",
	"iPhone16,1": "iPhone 15 Pro", "iPhone16,2": "iPhone 15 Pro Max",
	"iPhone17,1": "iPhone 16 Pro", "iPhone17,2": "iPhone 16 Pro Max", "iPhone17,3": "iPhone 16", "iPhone17,4": "iPhone 16 Plus", "iPhone17,5": "iPhone 16e",
	"iPhone18,1": "iPhone 17 Pro", "iPhone18,2": "iPhone 17 Pro Max", "iPhone18,3": "iPhone 17", "iPhone18,4": "iPhone Air",
	"iPhone19,2": "iPhone 18 Pro",
}

var _ driver.Provider = (*Provider)(nil)
