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
// take HID input. Older phones go other ways; see pathFor.
const minIOS = 27

// Provider discovers iPhones through usbmuxd (Apple Devices on Windows,
// usbmuxd on Linux, built in on macOS).
type Provider struct {
	// Wireless also lists devices usbmuxd reaches over Wi-Fi. It is off by
	// default: those include every iPad and iPhone ever paired with Xcode
	// or Finder over the network, and the gateway opens whatever it lists.
	Wireless bool

	// DiskImageDir is Pinky's own folder for Apple's developer disk image
	// (an iOS_DDI folder in it), looked in before Xcode's; "" for none.
	DiskImageDir string

	mu          sync.Mutex
	ddi         map[string]ddiCheck // by UDID
	known       map[string]known    // by UDID, what List last found
	setup       func(ctx context.Context, e goios.DeviceEntry) (bool, error)
	legacySetup func(ctx context.Context, e goios.DeviceEntry) (bool, error)
	findRunner  func(e goios.DeviceEntry) (string, error)
	ways        map[string]string    // by UDID: the connection the person chose (see SetConnection)
	wdaWaits    map[string]*wdaStart // by UDID: a runner waiting for the passcode (see wdaState)
	wdaHolds    map[string]wdaHold   // by UDID: no new runner until then

	// For tests: how a phone is inspected, and the clock.
	inspectFn func(ctx context.Context, e goios.DeviceEntry) driver.Info
	now       func() time.Time

	prep       preparer // Developer Mode and the developer disk image (prepare.go)
	muxErr     error    // usbmuxd's answer to the last List
	muxChecked bool
	muxFails   int                              // Lists in a row that usbmuxd did not answer
	muxPhones  int                              // phones the last answered List found
	listFn     func() (goios.DeviceList, error) // for tests: usbmuxd's list
}

// recheckOffline is how often List asks a phone that is not online again,
// and one driven without the WebDriverAgent helper, so a helper installed
// meanwhile is noticed. Other online phones are asked again only when their
// usbmuxd entry changes, which it does when the phone reconnects or
// restarts.
const recheckOffline = 30 * time.Second

// wdaHold keeps Pinky from starting the helper on a phone again for a
// while, after the phone asked for its passcode to allow automation and
// nobody entered it while the runner waited. The request stays on the
// phone, and a passcode entered there later counts for the next start.
// Each miss in a row doubles the wait.
type wdaHold struct {
	key    entryKey
	until  time.Time
	misses int
}

// How long Pinky waits after a missed passcode before starting the helper
// again, at first and at most.
const (
	wdaHoldFirst = 2 * time.Minute
	wdaHoldMax   = 30 * time.Minute
)

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
	return &Provider{ddi: map[string]ddiCheck{}, known: map[string]known{}, setup: probeServices, legacySetup: probeLegacyServices}
}

// Platform implements driver.Provider.
func (p *Provider) Platform() driver.Platform { return driver.IOS }

// List implements driver.Provider. It checks what Pinky needs and says in
// Detail how to fix what is missing. A gateway calls it every few seconds,
// so it asks each phone again only when needed (see recheckOffline).
func (p *Provider) List(ctx context.Context) ([]driver.Info, error) {
	list, ok, err := p.listUsbmuxd()
	if !ok {
		return nil, err // nil without usbmuxd: CheckHost says what to install
	}
	entries := p.entries(list)
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
	if ok && k.key == key && k.info.State == driver.StateOnline && k.info.Backend == backendWDA {
		if info, waiting := p.wdaState(ctx, key, k.info); waiting {
			return info
		}
	}
	if ok && k.key == key && ((k.info.State == driver.StateOnline && k.info.Backend != backendAccessibility) || p.clock().Sub(k.at) < recheckFor(k.info)) {
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

// forgetExcept drops what List knew about phones that are gone, and stops
// the helper's runners that waited on them.
func (p *Provider) forgetExcept(entries []goios.DeviceEntry) {
	listed := make(map[string]bool, len(entries))
	for _, e := range entries {
		listed[e.Properties.SerialNumber] = true
	}
	var stop []*wdaStart
	p.mu.Lock()
	for id := range p.known {
		if !listed[id] {
			delete(p.known, id)
		}
	}
	for id, s := range p.wdaWaits {
		if !listed[id] {
			stop = append(stop, s)
			delete(p.wdaWaits, id)
		}
	}
	for id := range p.wdaHolds {
		if !listed[id] {
			delete(p.wdaHolds, id)
		}
	}
	p.mu.Unlock()
	for _, s := range stop {
		go s.stop()
	}
}

// wdaState is how a phone driven through the helper is while its runner
// waits for the passcode (see errAutomation), or for a while after nobody
// entered it: needing setup, so the gateway does not open it, with what to
// do. waiting is false for any other phone, and once the helper answers
// (the next Open takes the waiting runner over).
func (p *Provider) wdaState(ctx context.Context, key entryKey, info driver.Info) (driver.Info, bool) {
	id := info.ID
	p.mu.Lock()
	s := p.wdaWaits[id]
	if s != nil && keyOf(s.base) != key {
		// The phone reconnected or restarted: that runner is gone with
		// the old connection, and nobody missed anything.
		delete(p.wdaWaits, id)
		go s.stop()
		s = nil
	}
	p.mu.Unlock()
	if s != nil {
		if s.ready(ctx) {
			return info, false
		}
		if s.running() {
			info.State, info.Detail = driver.StateNeedsSetup, automationAsk
			return info, true
		}
		// The runner gave up waiting: nobody entered the passcode.
		p.mu.Lock()
		if p.wdaWaits[id] == s {
			delete(p.wdaWaits, id)
			h := p.wdaHolds[id]
			if h.key != key {
				h = wdaHold{key: key}
			}
			h.misses++
			wait := wdaHoldFirst << (h.misses - 1)
			if wait > wdaHoldMax || wait <= 0 {
				wait = wdaHoldMax
			}
			h.until = p.clock().Add(wait)
			if p.wdaHolds == nil {
				p.wdaHolds = map[string]wdaHold{}
			}
			p.wdaHolds[id] = h
		}
		p.mu.Unlock()
		go s.stop()
	}
	p.mu.Lock()
	h, held := p.wdaHolds[id]
	p.mu.Unlock()
	if held && h.key == key && p.clock().Before(h.until) {
		info.State, info.Detail = driver.StateNeedsSetup, automationMissed
		return info, true
	}
	return info, false
}

// openWDA opens a phone through the helper, taking over the runner that
// waited for the passcode. When the phone asks for it, the runner is kept
// for List to watch (see wdaState) instead of started again every pass.
func (p *Provider) openWDA(ctx context.Context, info driver.Info) (driver.Device, error) {
	p.mu.Lock()
	s := p.wdaWaits[info.ID]
	delete(p.wdaWaits, info.ID)
	p.mu.Unlock()
	if s == nil {
		var err error
		if s, err = startWDAFor(ctx, info); err != nil {
			return nil, err
		}
	}
	d, err := s.open(ctx, info)
	switch {
	case err == nil:
		p.mu.Lock()
		delete(p.wdaHolds, info.ID)
		p.mu.Unlock()
		return d, nil
	case errors.Is(err, errAutomation):
		p.mu.Lock()
		if p.wdaWaits == nil {
			p.wdaWaits = map[string]*wdaStart{}
		}
		p.wdaWaits[info.ID] = s
		p.mu.Unlock()
	default:
		s.stop()
	}
	return nil, err
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
		if !isPairingError(err) {
			info.State = driver.StateOffline
			info.Detail = "The iPhone does not answer: " + err.Error()
			return info
		}
		p.prep.askTrust(e)
		return trustState(info, err)
	}
	info.Name = values.Value.DeviceName
	info.Model = modelName(values.Value.ProductType)
	info.OSVersion = values.Value.ProductVersion
	kind, why := pathFor(parseVersion(info.OSVersion))
	if kind == pathUnsupported {
		info.State = driver.StateNeedsSetup
		info.Detail = why
		if parseVersion(info.OSVersion).Major != 0 {
			info.Setup = driver.SetupUpdate
		}
		return info
	}
	dev, err := imagemounter.IsDevModeEnabled(e)
	if err == nil && !dev {
		return p.devModeOff(e, info)
	}
	switch p.way(info.ID) {
	case backendWDA:
		return p.inspectWDA(ctx, e, info)
	case backendCoreDevice:
		if kind != pathCoreDevice {
			info.State = driver.StateNeedsSetup
			info.Detail = fmt.Sprintf(coreDeviceTooOld, info.OSVersion)
			return info
		}
	default:
		if kind == pathLegacy {
			return p.inspectLegacy(ctx, e, info)
		}
	}
	ok, err := p.services(ctx, e)
	switch {
	case err != nil:
		info.State = driver.StateOffline
		info.Detail = "Could not reach the iPhone's developer services: " + err.Error()
	case !ok:
		return p.diskImageMissing(e, info, p.setup)
	}
	return info
}

// services reports whether the screen and HID services are published, which
// they are once the developer disk image is mounted. It needs a tunnel, so
// results are cached: a mounted image stays mounted until the phone restarts.
func (p *Provider) services(ctx context.Context, e goios.DeviceEntry) (bool, error) {
	return p.servicesWith(ctx, e, "display", p.setup)
}

// servicesWith is services with the check to run, cached by the phone and
// the check's name.
func (p *Provider) servicesWith(ctx context.Context, e goios.DeviceEntry, check string, setup func(context.Context, goios.DeviceEntry) (bool, error)) (bool, error) {
	id := e.Properties.SerialNumber + "|" + check
	p.mu.Lock()
	c, ok := p.ddi[id]
	p.mu.Unlock()
	if ok && ((c.ok && time.Since(c.at) < 5*time.Minute) || (!c.ok && time.Since(c.at) < 10*time.Second)) {
		if c.err != "" {
			return false, errors.New(c.err)
		}
		return c.ok, nil
	}
	ok, err := setup(ctx, e)
	c = ddiCheck{ok: ok, at: time.Now()}
	if err != nil {
		c.err = err.Error()
	}
	p.mu.Lock()
	p.ddi[id] = c
	p.mu.Unlock()
	return ok, err
}

// inspectLegacy finishes inspecting a phone on iOS 17.4 to 26: the
// developer services must be there (the developer disk image mounted), and
// the WebDriverAgent helper decides the backend.
func (p *Provider) inspectLegacy(ctx context.Context, e goios.DeviceEntry, info driver.Info) driver.Info {
	ok, err := p.servicesWith(ctx, e, "developer", p.legacySetup)
	switch {
	case err != nil:
		info.State = driver.StateOffline
		info.Detail = "Could not reach the iPhone's developer services: " + err.Error()
		return info
	case !ok:
		return p.diskImageMissing(e, info, p.legacySetup)
	}
	info.Backend = backendAccessibility
	if id, err := p.runner(e); err == nil && id != "" {
		info.Backend = backendWDA
	}
	return info
}

// inspectWDA finishes inspecting a phone the person chose to drive through
// the WebDriverAgent helper, on any iOS Pinky supports: the developer
// services must be there (testmanagerd runs the helper), and the helper
// must be installed.
func (p *Provider) inspectWDA(ctx context.Context, e goios.DeviceEntry, info driver.Info) driver.Info {
	ok, err := p.servicesWith(ctx, e, "developer", p.legacySetup)
	switch {
	case err != nil:
		info.State = driver.StateOffline
		info.Detail = "Could not reach the iPhone's developer services: " + err.Error()
		return info
	case !ok:
		return p.diskImageMissing(e, info, p.legacySetup)
	}
	if id, err := p.runner(e); err != nil || id == "" {
		info.State = driver.StateNeedsSetup
		info.Detail = wdaMissing
		return info
	}
	info.Backend = backendWDA
	return info
}

func (p *Provider) runner(e goios.DeviceEntry) (string, error) {
	find := p.findRunner
	if find == nil {
		find = findWDARunner
	}
	return find(e)
}

// Sentences for a chosen connection the phone cannot use, and for the
// passcode the phone asks for before the helper can control it.
const (
	coreDeviceTooOld = "Connecting without the helper needs iOS 27 or later; this iPhone runs iOS %s. Choose Automatic or WebDriverAgent in Settings > Phones."
	wdaMissing       = "The WebDriverAgent helper is not installed on this iPhone. Install it once from Xcode (docs/notes/ios.md, \"Installing the WebDriverAgent helper\"), or choose another connection in Settings > Phones."
	automationAsk    = "Enter the passcode on the iPhone: iOS asks for it now and then before the WebDriverAgent helper can control the phone."
	automationMissed = "The iPhone asked for its passcode to let the WebDriverAgent helper control it. If it still asks, enter the passcode there. Pinky tries again in a few minutes, or at once when you reconnect the iPhone or choose its connection again in Settings > Phones."
)

// way is the connection the person chose for a phone.
func (p *Provider) way(id string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ways[id]
}

// SetConnection implements driver.ConnectionChooser: "coredevice",
// "wda" or driver.ConnectionAuto. The phone is inspected again at the next
// List, and its next Open goes the chosen way.
func (p *Provider) SetConnection(id, way string) error {
	switch way {
	case driver.ConnectionAuto, backendCoreDevice, backendWDA:
	default:
		return fmt.Errorf("connection %q: %w", way, driver.ErrUnsupported)
	}
	p.mu.Lock()
	if p.ways == nil {
		p.ways = map[string]string{}
	}
	if way == driver.ConnectionAuto {
		delete(p.ways, id)
	} else {
		p.ways[id] = way
	}
	delete(p.known, id)
	s := p.wdaWaits[id]
	delete(p.wdaWaits, id)
	delete(p.wdaHolds, id)
	p.mu.Unlock()
	if s != nil {
		go s.stop()
	}
	return nil
}

// Connections implements driver.ConnectionChooser: Automatic, CoreDevice
// (iOS 27 and later) and WebDriverAgent (the helper installed), with what
// each needs that the phone lacks. It asks lockdown for the version and the
// installation proxy for the helper, both quick.
func (p *Provider) Connections(ctx context.Context, id string) driver.Connections {
	out := driver.Connections{Chosen: p.way(id)}
	p.mu.Lock()
	k, seen := p.known[id]
	p.mu.Unlock()
	if seen && k.info.State == driver.StateOnline {
		out.Current = k.info.Backend
	}
	auto := driver.Connection{ID: driver.ConnectionAuto, Available: true}
	cd := driver.Connection{ID: backendCoreDevice}
	wda := driver.Connection{ID: backendWDA}
	offline := func(c *driver.Connection, err error) {
		c.Reason, c.Detail = "offline", "The iPhone does not answer: "+err.Error()
	}
	e, err := findEntry(id, false)
	if err != nil {
		offline(&cd, err)
		offline(&wda, err)
		out.Ways = []driver.Connection{auto, cd, wda}
		return out
	}
	version := k.info.OSVersion
	if !seen || version == "" {
		if values, err := goios.GetValues(e); err == nil {
			version = values.Value.ProductVersion
		} else {
			offline(&cd, err)
			offline(&wda, err)
			out.Ways = []driver.Connection{auto, cd, wda}
			return out
		}
	}
	kind, why := pathFor(parseVersion(version))
	switch kind {
	case pathCoreDevice:
		cd.Available = true
	case pathUnsupported:
		cd.Reason, cd.Detail = "os_version", why
	default:
		cd.Reason, cd.Detail = "os_version", fmt.Sprintf(coreDeviceTooOld, version)
	}
	switch {
	case kind == pathUnsupported:
		wda.Reason, wda.Detail = "os_version", why
	default:
		if rid, err := p.runner(e); err != nil {
			offline(&wda, err)
		} else if rid == "" {
			wda.Reason, wda.Detail = "not_installed", wdaMissing
		} else {
			wda.Available = true
		}
	}
	if kind == pathUnsupported {
		auto.Available, auto.Reason, auto.Detail = false, "os_version", why
	}
	out.Ways = []driver.Connection{auto, cd, wda}
	return out
}

// probeLegacyServices reports whether the developer services Pinky uses
// below iOS 27 are published: instruments (screenshots) and the
// accessibility daemon, both from the developer disk image.
func probeLegacyServices(ctx context.Context, e goios.DeviceEntry) (bool, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	l, err := dialLinkEntry(cctx, e)
	if err != nil {
		return false, err
	}
	defer l.Close()
	return l.has(instrumentsService), nil
}

// instrumentsService is DTServiceHub, instruments over RemoteXPC (iOS 17+).
const instrumentsService = "com.apple.instruments.dtservicehub"

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
	dev, err := p.openBackend(ctx, info)
	if err != nil {
		return nil, err
	}
	if s, ok := dev.(imageDirSetter); ok {
		s.setImageDir(p.DiskImageDir) // for the checkup (checkup.go)
	}
	return dev, nil
}

// openBackend opens the phone the way its Info.Backend says.
func (p *Provider) openBackend(ctx context.Context, info driver.Info) (driver.Device, error) {
	switch info.Backend {
	case backendWDA:
		return p.openWDA(ctx, info)
	case backendAccessibility:
		return OpenAccessibility(ctx, info)
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
// Pinky can drive (iOS 17.4 runs on iPhone XS, XR and later).
func modelName(productType string) string {
	if n, ok := models[productType]; ok {
		return n
	}
	return productType
}

var models = map[string]string{
	"iPhone11,2": "iPhone XS", "iPhone11,4": "iPhone XS Max", "iPhone11,6": "iPhone XS Max", "iPhone11,8": "iPhone XR",
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

var (
	_ driver.Provider          = (*Provider)(nil)
	_ driver.ConnectionChooser = (*Provider)(nil)
)
