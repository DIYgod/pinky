package ios

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/DIYgod/pinky/internal/driver/ios/netmux"
	goios "github.com/danielpaulus/go-ios/ios"
)

// Phones over Wi-Fi on Linux and Windows. libimobiledevice's usbmuxd, the
// one Linux systems have, lists phones on a cable only, and Apple's on
// Windows lists a phone on Wi-Fi only while it is on a cable too, so a
// phone that Pinky used went offline when it was unplugged instead of
// moving to Wi-Fi. With
// Provider.WiFiMux, while Pinky uses phones over Wi-Fi, the provider runs a
// usbmuxd of its own (the package netmux) in front of the system's: it
// hands it everything about phones on a cable, and adds the phones it finds
// on the network itself, as macOS's usbmuxd lists them, matched to this
// computer's pair records. go-ios, and so everything Pinky does with a
// phone, goes through it from then on (USBMUXD_SOCKET_ADDRESS, set for
// this process). Turning Wi-Fi off stops the search, and the phones on
// Wi-Fi leave the list; phones on a cable go on through it.
//
// When USBMUXD_SOCKET_ADDRESS is set already (netmuxd, usbmuxd2 or
// another usbmuxd the person runs), Pinky uses that one and leaves the
// network to it.
//
// On macOS Apple's usbmuxd lists phones on Wi-Fi itself, and Pinky runs a
// usbmuxd of its own for one case only: a Mac on an iPhone's own Personal
// Hotspot (#235), where usbmuxd dials only the phone's IPv6 link-local
// address and gives up, while lockdown answers at the phone's address
// there, hotspotPhone. Once lostPhones finds such a phone (lost.go: its
// records name that address, and this Mac has one in hotspotNet),
// hotspotMux starts the usbmuxd, at once, and a search for those phones
// only, at that address only; everything else, phones on a cable or on
// another network among them, goes on to Apple's usbmuxd through it. The
// search stops when Wi-Fi is turned off or this Mac has been off the
// hotspot for hotspotLeave (leaveHotspot), and the phones there leave the
// list; the usbmuxd and USBMUXD_SOCKET_ADDRESS stay until Pinky quits. Its
// socket is in a new 0700 folder in TMPDIR, this user's own (muxBase), and
// it reads pair records only through Apple's usbmuxd, which keeps them
// where only its own account can read them (netmux.LockdownDir is "").
//
// The variable is set for the whole process, so every process Pinky starts
// inherits it: the update helper and the Pinky it starts after an update,
// a restart, an agent and what the agent runs. A Pinky that inherits the
// address of another Pinky's usbmuxd, gone with it, must not take it for
// one the person set, or no iPhone connects (Windows after an update). So
// Pinky sets ownMuxVar to the same address, and a Pinky that starts with
// both equal forgets them (forgetInheritedMux), before go-ios reads the
// address.

// ownMuxVar holds the address Pinky set USBMUXD_SOCKET_ADDRESS to for its
// own usbmuxd.
const ownMuxVar = "PINKY_OWN_USBMUXD"

func init() { forgetInheritedMux() }

// forgetInheritedMux unsets USBMUXD_SOCKET_ADDRESS when it is the address
// another Pinky set for its own usbmuxd, and ownMuxVar always.
func forgetInheritedMux() {
	own := os.Getenv(ownMuxVar)
	if own != "" && os.Getenv("USBMUXD_SOCKET_ADDRESS") == own {
		os.Unsetenv("USBMUXD_SOCKET_ADDRESS")
	}
	os.Unsetenv(ownMuxVar)
}

// muxState is the provider's usbmuxd of its own. Its zero value is ready.
type muxState struct {
	mu      sync.Mutex
	mux     *netmux.Mux
	stop    context.CancelFunc // ends the search while it runs
	done    chan struct{}      // closed once the last search ended
	skipped bool               // USBMUXD_SOCKET_ADDRESS was set, or the socket failed
	on      bool               // Pinky uses phones over Wi-Fi, as wifiMux was last told
	hotspot map[string]bool    // macOS: the phones the search looks for on their own Personal Hotspot
	away    time.Time          // macOS: since when this Mac has had no address on the hotspot, while the search runs

	// For tests.
	anyOS    bool // as on Linux, on any system
	macOS    bool // as on macOS, on any system
	browseFn func() (netmux.Source, error)
	pairFn   func(udid string) (netmux.PairInfo, error)
	probeFn  func(ctx context.Context, addr netip.Addr) bool
	localFn  func() []netip.Addr // this computer's addresses
	dir      string              // where the socket goes instead of a new folder
	pairs    string              // where the pair records are instead of netmux.LockdownDir
	upstream string              // the system's usbmuxd instead of go-ios's default
}

// everywhere reports whether the provider looks for every phone on Wi-Fi
// itself, as on Linux and Windows (ownMuxOS).
func (m *muxState) everywhere() bool { return !m.macOS && (ownMuxOS() || m.anyOS) }

// hotspotOnly reports whether it looks only for phones whose own Personal
// Hotspot this computer is on, as on macOS.
func (m *muxState) hotspotOnly() bool { return m.macOS || (runtime.GOOS == "darwin" && !m.anyOS) }

// stopLocked ends the search; the phones it found leave the list. The
// caller holds m.mu.
func (m *muxState) stopLocked() {
	if m.stop != nil {
		m.stop()
		m.stop = nil
	}
	m.hotspot, m.away = nil, time.Time{}
}

// pairDir is where the system's usbmuxd keeps pair records.
func (m *muxState) pairDir() string {
	if m.pairs != "" {
		return m.pairs
	}
	return netmux.LockdownDir
}

// wifiMux starts the usbmuxd of its own and the search when Pinky uses
// phones over Wi-Fi, and stops the search when it no longer does. On macOS
// turning it on starts nothing: the search there is for phones on their
// own Personal Hotspot, once lostPhones finds one (hotspotMux).
func (p *Provider) wifiMux(on bool) {
	if !p.WiFiMux {
		return
	}
	m := &p.nmux
	m.mu.Lock()
	defer m.mu.Unlock()
	m.on = on
	if !on {
		m.stopLocked()
		return
	}
	if !m.everywhere() {
		return
	}
	if m.mux == nil && !m.skipped {
		p.startMuxLocked()
	}
	if m.mux == nil || m.stop != nil {
		return
	}
	p.findLocked(p.pairedUDIDs, nil)
}

// findLocked starts the search for the phones udids returns, at the
// addresses accept takes (any when nil). It looks once the last search
// has ended, whose end lists no phones: so that a search stopped a moment
// ago does not drop the phones this one found. The caller holds p.nmux.mu.
func (p *Provider) findLocked(udids func() []string, accept func(netip.Addr) bool) {
	m := &p.nmux
	ctx, cancel := context.WithCancel(context.Background())
	m.stop = cancel
	f := &netmux.Finder{Mux: m.mux, UDIDs: udids, Accept: accept, Log: p.logger(), BrowseFn: m.browseFn, LocalFn: m.localFn}
	if m.probeFn != nil {
		f.ProbeFn = m.probeFn
	}
	if m.pairFn != nil {
		f.PairFn = m.pairFn
	}
	last, done := m.done, make(chan struct{})
	m.done = done
	go func() {
		defer close(done)
		if last != nil {
			<-last // canceled already (stopLocked)
		}
		f.Run(ctx)
	}()
}

// hotspotLeave is how long this Mac must have had no address on a phone's
// Personal Hotspot before the search there stops: as long as the Finder
// keeps a listed phone that does not answer (netmux's dropAfter), so that
// the Mac's Wi-Fi dropping for a moment, as when it renews its address,
// does not end Pinky's session on the phone.
const hotspotLeave = 45 * time.Second

// hotspotMux looks for phones on their own Personal Hotspot with the
// usbmuxd of its own, on macOS: lostPhones gives it the phones usbmuxd
// does not list whose records name hotspotPhone while this Mac has an
// address on that network (local). It starts the usbmuxd the first time,
// adds the phones to those the search looks for, and starts the search, at
// hotspotPhone only, when it does not run. Without a usbmuxd of its own
// (USBMUXD_SOCKET_ADDRESS set, or no socket) lostPhones says after its wait
// that the phone is out of reach.
func (p *Provider) hotspotMux(ids []string, local netip.Addr) {
	if len(ids) == 0 || !p.WiFiMux {
		return
	}
	m := &p.nmux
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.on || !m.hotspotOnly() {
		return
	}
	if m.mux == nil && !m.skipped {
		p.startMuxLocked()
	}
	if m.mux == nil {
		return
	}
	var added []string
	for _, id := range ids {
		if !m.hotspot[id] {
			if m.hotspot == nil {
				m.hotspot = map[string]bool{}
			}
			m.hotspot[id] = true
			added = append(added, id)
		}
	}
	if len(added) > 0 {
		p.logger().Info("iphone: this Mac is on the phone's Personal Hotspot, where usbmuxd does not reach it; looking for it there with Pinky's own usbmuxd",
			"devices", added, "addr", hotspotPhone, "local", local)
	}
	if m.stop == nil {
		m.away = time.Time{}
		p.findLocked(p.hotspotUDIDs, func(a netip.Addr) bool { return a == hotspotPhone })
	}
}

// hotspotUDIDs returns the phones the search looks for on their own
// Personal Hotspot, but not those the person removed since; under Only,
// only those phones.
func (p *Provider) hotspotUDIDs() []string {
	m := &p.nmux
	m.mu.Lock()
	ids := make([]string, 0, len(m.hotspot))
	for id := range m.hotspot {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	p.mu.Lock()
	out := ids[:0]
	for _, id := range ids {
		if !p.wifi.forgot[id] && (p.Only == nil || p.Only[id]) {
			out = append(out, id)
		}
	}
	p.mu.Unlock()
	sort.Strings(out)
	return out
}

// leaveHotspot stops the search for phones on their own Personal Hotspot
// once this Mac has had no address on that network for hotspotLeave. List
// calls it each time.
func (p *Provider) leaveHotspot() {
	m := &p.nmux
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stop == nil || len(m.hotspot) == 0 {
		return
	}
	local := m.localFn
	if local == nil {
		local = localAddrs
	}
	now := p.clock()
	switch {
	case slices.ContainsFunc(local(), hotspotNet.Contains):
		m.away = time.Time{}
	case m.away.IsZero():
		m.away = now
	case now.Sub(m.away) >= hotspotLeave:
		m.stopLocked()
		p.logger().Info("iphone: this Mac left the phone's Personal Hotspot; no longer looking for phones there")
	}
}

// startMuxLocked starts the usbmuxd of its own and points go-ios at it.
// The caller holds p.nmux.mu.
func (p *Provider) startMuxLocked() {
	m := &p.nmux
	if v := os.Getenv("USBMUXD_SOCKET_ADDRESS"); v != "" {
		m.skipped = true
		p.logger().Info("iphone: using the usbmuxd in USBMUXD_SOCKET_ADDRESS, which lists phones on Wi-Fi itself or not", "address", v)
		return
	}
	upstream := goios.GetUsbmuxdSocket()
	if m.upstream != "" {
		upstream = m.upstream
	}
	if runtime.GOOS == "windows" && m.dir == "" {
		// Apple's usbmuxd listens on a TCP port on Windows, and go-ios
		// dials one as well; a unix socket's file there can outlive a
		// Pinky that was killed and then refuse the next one.
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			m.skipped = true
			p.logger().Warn("iphone: cannot look for phones on Wi-Fi", "err", err)
			return
		}
		p.serveMuxLocked(upstream, l, "tcp://"+l.Addr().String())
		return
	}
	dir := m.dir
	if dir == "" {
		base := muxBase(runtime.GOOS, os.Getenv("XDG_RUNTIME_DIR"), os.TempDir())
		removeOldMuxDirs(base)
		d, err := os.MkdirTemp(base, "pinky-usbmuxd-") // 0700: this user's only
		if err != nil {
			m.skipped = true
			p.logger().Warn("iphone: cannot look for phones on Wi-Fi", "err", err)
			return
		}
		dir = d
	}
	path := filepath.Join(dir, "usbmuxd")
	l, err := net.Listen("unix", path)
	if err != nil {
		m.skipped = true
		p.logger().Warn("iphone: cannot look for phones on Wi-Fi", "err", err)
		return
	}
	p.serveMuxLocked(upstream, l, "unix://"+path)
}

// muxBase is the folder the usbmuxd's own folder goes in: TMPDIR on macOS
// (tempDir, this user's own; XDG_RUNTIME_DIR, which a shell may set there,
// is not macOS's), XDG_RUNTIME_DIR elsewhere when set, else tempDir; /tmp
// when the socket's path there could be too long for the system's limit
// (sun_path: 104 bytes on macOS, 108 on Linux, the last a NUL).
func muxBase(goos, runtimeDir, tempDir string) string {
	base := tempDir
	if goos != "darwin" && runtimeDir != "" {
		base = runtimeDir
	}
	limit := 107
	if goos == "darwin" {
		limit = 103
	}
	// os.MkdirTemp's random part has 10 digits at most.
	if len(filepath.Join(base, "pinky-usbmuxd-4294967295", "usbmuxd")) > limit {
		return "/tmp"
	}
	return base
}

// removeOldMuxDirs removes the folders that earlier Pinkys left in base.
// Nothing tells the provider that Pinky ends, so it cannot remove its own,
// and every start left one more until the person logged out. A folder is
// left over when nothing listens at its socket any more; one without a
// socket may be a Pinky's that is starting right now, unless it is old.
func removeOldMuxDirs(base string) {
	dirs, _ := filepath.Glob(filepath.Join(base, "pinky-usbmuxd-*"))
	for _, dir := range dirs {
		fi, err := os.Lstat(dir)
		if err != nil || !fi.IsDir() {
			continue
		}
		c, err := net.DialTimeout("unix", filepath.Join(dir, "usbmuxd"), time.Second)
		switch {
		case err == nil:
			c.Close() // another Pinky's, which runs
		case errors.Is(err, syscall.ECONNREFUSED):
			os.RemoveAll(dir)
		case errors.Is(err, fs.ErrNotExist) && time.Since(fi.ModTime()) > time.Minute:
			os.RemoveAll(dir)
		}
	}
}

// serveMuxLocked runs the usbmuxd of its own on l, in front of upstream,
// and points go-ios at it (addr). The caller holds p.nmux.mu.
func (p *Provider) serveMuxLocked(upstream string, l net.Listener, addr string) {
	m := &p.nmux
	m.mux = netmux.New(upstream, p.logger())
	m.mux.PairDir = m.pairDir()
	go m.mux.Serve(l)
	os.Setenv("USBMUXD_SOCKET_ADDRESS", addr)
	os.Setenv(ownMuxVar, addr) // so that the processes it starts forget it
	// Phones on the network close developer services without it
	// (netmux/heartbeat.go). Not in tests, which list made-up phones.
	m.mux.Heartbeat = m.dir == ""
	if m.hotspotOnly() {
		p.logger().Info("iphone: running a usbmuxd of its own for phones whose Personal Hotspot this Mac is on; it hands everything else to the system's usbmuxd", "usbmuxd", upstream, "socket", addr)
		return
	}
	p.logger().Info("iphone: looking for iPhones and iPads on Wi-Fi itself; this computer's usbmuxd does not list them there by itself", "usbmuxd", upstream)
}

// ownMuxOS reports whether Pinky looks for phones on Wi-Fi itself on this
// system: on Linux, whose usbmuxd lists phones on a cable only, and on
// Windows, whose Apple usbmuxd lists an iPhone on Wi-Fi only while it is
// on a cable too (an iPhone 18 Pro Max on iOS 27.2, 2026-11-09).
func ownMuxOS() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "windows"
}

// pairedUDIDs returns the phones this computer paired with: those with a
// pair record where the system's usbmuxd keeps them, and those Pinky has
// seen, but not those the person removed. Under Only, only those phones.
func (p *Provider) pairedUDIDs() []string {
	ids := map[string]bool{}
	for _, id := range netmux.PairedUDIDs(p.nmux.pairDir()) {
		ids[id] = true
	}
	p.mu.Lock()
	for id := range p.known {
		ids[id] = true
	}
	for id := range p.wifi.use {
		ids[id] = true
	}
	// A phone the person removed is not looked for, nor asked, until it is
	// on a cable again (Forget).
	for id := range p.wifi.forgot {
		delete(ids, id)
	}
	p.mu.Unlock()
	var out []string
	for id := range ids {
		if p.Only == nil || p.Only[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
