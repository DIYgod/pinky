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
	// Wireless also lists devices usbmuxd reaches over Wi-Fi. It is off by
	// default: those include every iPad and iPhone ever paired with Xcode
	// or Finder over the network, and the gateway opens whatever it lists.
	Wireless bool

	mu    sync.Mutex
	ddi   map[string]ddiCheck // by UDID
	known map[string]known    // by UDID, what List last found
	setup func(ctx context.Context, e goios.DeviceEntry) (bool, error)

	// For tests: how a phone is inspected, and the clock.
	inspectFn func(ctx context.Context, e goios.DeviceEntry) driver.Info
	now       func() time.Time
}

// recheckOffline is how often List asks a phone that is not online again.
// An online phone is asked again only when its usbmuxd entry changes, which
// it does when the phone reconnects or restarts.
const recheckOffline = 30 * time.Second

// known is an inspected phone and the usbmuxd entry it was seen through.
type known struct {
	key  entryKey
	info driver.Info
	at   time.Time
}

// entryKey identifies one usbmuxd connection to a phone. usbmuxd gives a
// phone a new DeviceID each time it attaches.
type entryKey struct {
	deviceID   int
	connection string
}

func keyOf(e goios.DeviceEntry) entryKey {
	return entryKey{deviceID: e.DeviceID, connection: e.Properties.ConnectionType}
}

type ddiCheck struct {
	ok  bool
	at  time.Time
	err string
}

// NewProvider returns an iOS provider.
func NewProvider() *Provider {
	return &Provider{ddi: map[string]ddiCheck{}, known: map[string]known{}, setup: probeServices}
}

// Platform implements driver.Provider.
func (p *Provider) Platform() driver.Platform { return driver.IOS }

// List implements driver.Provider. It checks what Pinky needs and says in
// Detail how to fix what is missing. A gateway calls it every few seconds,
// so it asks each phone again only when needed (see recheckOffline).
func (p *Provider) List(ctx context.Context) ([]driver.Info, error) {
	list, err := goios.ListDevices()
	if err != nil {
		return nil, fmt.Errorf("usbmuxd: %w (on Windows install Apple Devices, on Linux the usbmuxd package)", err)
	}
	entries := p.entries(list.DeviceList)
	out := make([]driver.Info, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = p.info(ctx, e)
		}()
	}
	wg.Wait()
	p.forgetExcept(entries)
	return out, nil
}

// entries picks one usbmuxd entry per phone, sorted by UDID. A phone on USB
// and Wi-Fi at once is listed twice; USB wins.
func (p *Provider) entries(list []goios.DeviceEntry) []goios.DeviceEntry {
	byID := map[string]goios.DeviceEntry{}
	for _, e := range list {
		if e.Properties.ConnectionType == "Network" && !p.Wireless {
			continue
		}
		id := e.Properties.SerialNumber
		if prev, ok := byID[id]; ok && prev.Properties.ConnectionType == "USB" {
			continue
		}
		byID[id] = e
	}
	out := make([]goios.DeviceEntry, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Properties.SerialNumber < out[j].Properties.SerialNumber })
	return out
}

// info returns what is known about the phone behind e. It inspects the
// phone again only when its entry changed, or every recheckOffline while it
// is not online, so a phone that stays attached costs no lockdown round trip.
func (p *Provider) info(ctx context.Context, e goios.DeviceEntry) driver.Info {
	id, key := e.Properties.SerialNumber, keyOf(e)
	p.mu.Lock()
	k, ok := p.known[id]
	p.mu.Unlock()
	if ok && k.key == key && (k.info.State == driver.StateOnline || p.clock().Sub(k.at) < recheckOffline) {
		return k.info
	}
	inspect := p.inspectFn
	if inspect == nil {
		inspect = p.inspect
	}
	info := inspect(ctx, e)
	if ctx.Err() != nil {
		return info // cut short; do not remember it
	}
	p.mu.Lock()
	if p.known == nil {
		p.known = map[string]known{}
	}
	p.known[id] = known{key: key, info: info, at: p.clock()}
	p.mu.Unlock()
	return info
}

// forgetExcept drops what List knew about phones that are gone.
func (p *Provider) forgetExcept(entries []goios.DeviceEntry) {
	listed := make(map[string]bool, len(entries))
	for _, e := range entries {
		listed[e.Properties.SerialNumber] = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for id := range p.known {
		if !listed[id] {
			delete(p.known, id)
		}
	}
}

func (p *Provider) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
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
	l, err := dialLinkEntry(cctx, e)
	if err != nil {
		return false, err
	}
	defer l.Close()
	return l.has(displayService) && l.has(universalHIDService), nil
}

// Open implements driver.Provider. It checks only the phone it opens, and
// reuses what List found when the phone's entry has not changed.
func (p *Provider) Open(ctx context.Context, id string) (driver.Device, error) {
	entry, err := findEntry(id, false)
	if err != nil {
		return nil, fmt.Errorf("%v: %w", err, driver.ErrNotFound)
	}
	if entry.Properties.ConnectionType == "Network" && !p.Wireless {
		return nil, fmt.Errorf("%s is reachable only over Wi-Fi and Wireless is off: %w", id, driver.ErrNotFound)
	}
	info := p.info(ctx, entry)
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
