package android

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/tailnet"
)

// Phones away from the computer's network, through Tailscale
// (driver.Tailnet). Wireless debugging needs the phone on Wi-Fi, turns
// itself off when the phone leaves the network it was turned on for
// (Android 11 to 16), takes a new port each time it comes on, and is found
// with mDNS, which does not cross a VPN. A phone away from home is reached
// instead at its Tailscale address on a fixed port, where its adb listens
// once set up (adb tcpip, over the cable or Wi-Fi): on every network the
// phone joins, for the computers it trusts, until it restarts. Tailscale
// encrypts the way there. The driver keeps the phones set up so in
// Config.AwayFile, and connects to them through Tailscale whenever
// Tailscale says they are online and they have no such connection yet,
// at home too, so that a phone leaving home stays connected.

// AwayPort is where a phone's adb listens for Pinky through Tailscale:
// adb's own port for `adb tcpip`.
const AwayPort = 5555

// awayPhone is a phone set up to be reached through Tailscale.
type awayPhone struct {
	ID   string `json:"id"`             // the phone's id, its hardware serial
	Name string `json:"name,omitempty"` // for the logs
	Node string `json:"node,omitempty"` // its Tailscale node id
	Addr string `json:"addr"`           // its Tailscale IPv4 address
	Port int    `json:"port"`
}

// serial is the adb serial of its connection through Tailscale.
func (a awayPhone) serial() string { return net.JoinHostPort(a.Addr, strconv.Itoa(a.port())) }

func (a awayPhone) port() int {
	if a.Port <= 0 {
		return AwayPort
	}
	return a.Port
}

// awayStore keeps the phones set up for away from home in a file, or in
// memory when it has no path.
type awayStore struct {
	path string
	logf func(string, ...any)

	mu     sync.Mutex
	loaded bool
	phones []awayPhone
}

type awayFile struct {
	Phones []awayPhone `json:"phones"`
}

func (s *awayStore) loadLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	if s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logf("android: reading the phones set up for away from home: %v", err)
		}
		return
	}
	var f awayFile
	if err := json.Unmarshal(b, &f); err != nil {
		s.logf("android: reading %s: %v", s.path, err)
		return
	}
	for _, a := range f.Phones {
		if a.ID != "" && a.Addr != "" {
			s.phones = append(s.phones, a)
		}
	}
}

func (s *awayStore) list() []awayPhone {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return slices.Clone(s.phones)
}

func (s *awayStore) get(id string) (awayPhone, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	for _, a := range s.phones {
		if a.ID == id {
			return a, true
		}
	}
	return awayPhone{}, false
}

// put adds the phone or replaces what is kept about it.
func (s *awayStore) put(a awayPhone) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	i := slices.IndexFunc(s.phones, func(o awayPhone) bool { return o.ID == a.ID })
	if i >= 0 && s.phones[i] == a {
		return nil
	}
	if i >= 0 {
		s.phones[i] = a
	} else {
		s.phones = append(s.phones, a)
	}
	return s.saveLocked()
}

// remove forgets the phone; it reports whether it was kept.
func (s *awayStore) remove(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	n := len(s.phones)
	s.phones = slices.DeleteFunc(s.phones, func(a awayPhone) bool { return a.ID == id })
	if len(s.phones) == n {
		return false, nil
	}
	return true, s.saveLocked()
}

func (s *awayStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(awayFile{Phones: s.phones}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// tailnetStatus reads Tailscale on this computer (Config.Tailnet).
func (p *Provider) tailnetStatus(ctx context.Context) (tailnet.Status, error) {
	st, err := p.cfg.Tailnet(ctx)
	if errors.Is(err, tailnet.ErrNotInstalled) {
		return tailnet.Status{State: tailnet.NotInstalled}, nil
	}
	return st, err
}

// Tailnet implements driver.Tailnet.
func (p *Provider) Tailnet(ctx context.Context) (driver.TailnetStatus, error) {
	st, err := p.tailnetStatus(ctx)
	if err != nil {
		return driver.TailnetStatus{}, err
	}
	out := driver.TailnetStatus{State: string(st.State), Detail: st.Detail, Tailnet: st.Tailnet, Self: st.Self.Name}
	entries, _ := p.cfg.Client.Devices(ctx)
	away := p.away.list()
	for _, d := range st.AndroidPeers() {
		addr := d.IPv4().String()
		ph := driver.TailnetPhone{Name: d.Name, Addr: addr, Online: d.Online, Relayed: d.Relayed()}
		if !d.Online {
			ph.LastSeen = d.LastSeen
		}
		for _, a := range away {
			if a.Node == d.ID || a.Addr == addr {
				ph.ID = a.ID
			}
		}
		for _, e := range entries {
			if e.State != adb.StateDevice || serialHost(e.Serial) != addr {
				continue
			}
			ph.Connected = true
			if ph.ID == "" {
				if pr, err := p.deviceProps(ctx, e); err == nil {
					ph.ID = phoneID(e, pr)
				}
			}
		}
		out.Phones = append(out.Phones, ph)
	}
	return out, nil
}

// ConnectTailnet implements driver.Tailnet. The address is the phone's
// Tailscale address or name, with a port or without one (AwayPort): a
// phone set up for away from home listens there; one on Wi-Fi with
// Wireless debugging on also takes connections at the port Wireless
// debugging shows, through Tailscale too, when it is paired with this
// computer. A phone reached at AwayPort is kept as set up.
func (p *Provider) ConnectTailnet(ctx context.Context, addr string) (driver.Info, error) {
	st, err := p.tailnetStatus(ctx)
	if err != nil {
		return driver.Info{}, err
	}
	if !st.Running() {
		return driver.Info{}, fmt.Errorf("android: Tailscale is not running on this computer (%s): %w", st.State, driver.ErrUnsupported)
	}
	host, port, err := splitTailnetAddr(strings.TrimSpace(addr), st)
	if err != nil {
		return driver.Info{}, err
	}
	serial := net.JoinHostPort(host, strconv.Itoa(port))
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := p.cfg.Client.Connect(ctx, serial); err != nil {
		return driver.Info{}, connectErr(serial, err)
	}
	info, err := p.waitConnected(ctx, serial)
	if err != nil {
		return info, err
	}
	if port == AwayPort && info.State == driver.StateOnline {
		a := awayPhone{ID: info.ID, Name: info.Name, Addr: host, Port: port}
		if d, ok := st.Device(host); ok {
			a.Node = d.ID
		}
		if err := p.away.put(a); err != nil {
			p.cfg.logf("android: keeping %s as set up for away from home: %v", info.ID, err)
		}
		p.tail.soon()
	}
	return info, nil
}

// waitConnected waits for the connection with this serial to come online,
// and returns the phone. A phone that asks the person to allow this
// computer is returned as it is (unauthorized) once it has asked for a
// while: the person may be away from it.
func (p *Provider) waitConnected(ctx context.Context, serial string) (driver.Info, error) {
	var asking time.Time
	for {
		entries, err := p.cfg.Client.Devices(ctx)
		if err != nil {
			return driver.Info{}, wrapErr(err)
		}
		for _, e := range entries {
			if e.Serial != serial {
				continue
			}
			switch e.State {
			case adb.StateDevice:
				return p.infoFor(ctx, serial)
			case adb.StateUnauthorized:
				if asking.IsZero() {
					asking = time.Now()
				}
				if time.Since(asking) > 10*time.Second {
					return infoFromEntry(e), nil
				}
			}
		}
		if err := sleepCtx(ctx, pairPoll); err != nil {
			return driver.Info{}, fmt.Errorf("android: adb connected to %s, but the phone did not come online: %w", serial, driver.ErrOffline)
		}
	}
}

// splitTailnetAddr reads an address typed or picked for ConnectTailnet: a
// Tailscale address or a device's name on the tailnet, with or without a
// port.
func splitTailnetAddr(addr string, st tailnet.Status) (string, int, error) {
	host, port := addr, AwayPort
	if h, ps, err := net.SplitHostPort(addr); err == nil {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, fmt.Errorf("android: %q has no port such as 5555", addr)
		}
		host, port = h, n
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if !tailnet.Contains(a) {
			return "", 0, fmt.Errorf("android: %s is not a Tailscale address (100.64.0.0/10)", host)
		}
		return a.Unmap().String(), port, nil
	}
	// A name: the device's MagicDNS name or host name.
	for _, d := range st.Peers {
		dn := strings.ToLower(d.DNSName)
		h := strings.ToLower(strings.TrimSuffix(host, "."))
		if h != "" && (h == dn || h == strings.ToLower(d.Name) || strings.HasPrefix(dn, h+".")) && d.IPv4().IsValid() {
			return d.IPv4().String(), port, nil
		}
	}
	return "", 0, fmt.Errorf("android: no device named %q on the tailnet", host)
}

// connectErr words a failed connection through Tailscale: refused means
// the phone is there but its adb does not listen at that port.
func connectErr(serial string, err error) error {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "refused") {
		return fmt.Errorf("android: %s: %v: %w", serial, err, driver.ErrNotListening)
	}
	return wrapErr(err)
}

// serialHost is the host of a connection made by address, "" for any
// other.
func serialHost(serial string) string {
	h, _, err := net.SplitHostPort(serial)
	if err != nil {
		return ""
	}
	return h
}

// viaTailnet reports whether a connection goes through Tailscale: it was
// made by address, to a Tailscale address.
func viaTailnet(serial string) bool {
	a, err := netip.ParseAddr(serialHost(serial))
	return err == nil && tailnet.Contains(a)
}

// transports returns the phone's online connections: its connection
// through Tailscale, and the best other one (USB before Wi-Fi).
func (p *Provider) transports(ctx context.Context, entries []adb.DeviceEntry, id string) (local, tail adb.DeviceEntry) {
	for _, e := range entries {
		if e.State != adb.StateDevice {
			continue
		}
		pr, err := p.deviceProps(ctx, e)
		if err != nil || (e.Serial != id && phoneID(e, pr) != id) {
			continue
		}
		switch {
		case viaTailnet(e.Serial):
			tail = e
		case local.Serial == "" || (!e.Wireless() && local.Wireless()):
			local = e
		}
	}
	return local, tail
}

// phoneTailnetAddrs returns the phone's Tailscale addresses, IPv4 first,
// as its network interfaces have them: none when Tailscale is not on.
func (p *Provider) phoneTailnetAddrs(ctx context.Context, serial string) ([]netip.Addr, error) {
	out, err := p.cfg.Client.Shell(ctx, serial, "ip -o addr show 2>/dev/null")
	if err != nil {
		return nil, wrapErr(err)
	}
	return parseTailnetAddrs(string(out)), nil
}

// parseTailnetAddrs reads `ip -o addr show` for Tailscale addresses:
//
//	31: tun0    inet 100.101.2.2/32 scope global tun0\       valid_lft forever preferred_lft forever
//
// Carriers give phones addresses in 100.64.0.0/10 too, so only a VPN's
// interface (tun*) counts, or one with an address in Tailscale's own IPv6
// range.
func parseTailnetAddrs(out string) []netip.Addr {
	type iface struct {
		addrs []netip.Addr
		ours  bool
	}
	var order []string
	ifs := map[string]*iface{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || (f[2] != "inet" && f[2] != "inet6") {
			continue
		}
		pfx, err := netip.ParsePrefix(f[3])
		if err != nil || !tailnet.Contains(pfx.Addr()) {
			continue
		}
		name := f[1]
		in := ifs[name]
		if in == nil {
			in = &iface{}
			ifs[name] = in
			order = append(order, name)
		}
		a := pfx.Addr().Unmap()
		in.addrs = append(in.addrs, a)
		if strings.HasPrefix(name, "tun") || a.Is6() {
			in.ours = true
		}
	}
	var v4, v6 []netip.Addr
	for _, name := range order {
		if !ifs[name].ours {
			continue
		}
		for _, a := range ifs[name].addrs {
			if a.Is4() {
				v4 = append(v4, a)
			} else {
				v6 = append(v6, a)
			}
		}
	}
	return append(v4, v6...)
}

// listenPort returns the port the phone's adb listens on the network at
// (adb tcpip), 0 when it does not.
func (p *Provider) listenPort(ctx context.Context, serial string) int {
	out, err := p.cfg.Client.Shell(ctx, serial, "getprop service.adb.tcp.port")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return max(n, 0)
}

// Away implements driver.Tailnet.
func (p *Provider) Away(ctx context.Context, id string) (driver.Away, error) {
	a, on := p.away.get(id)
	out := driver.Away{On: on, Port: AwayPort}
	if on {
		out.Addr, out.Port = a.Addr, a.port()
	}
	st, err := p.tailnetStatus(ctx)
	if err != nil {
		return out, err
	}
	entries, err := p.cfg.Client.Devices(ctx)
	if err != nil {
		return out, wrapErr(err)
	}
	local, tail := p.transports(ctx, entries, id)
	switch {
	case tail.Serial != "":
		out.Listening = true
		h, ps, _ := net.SplitHostPort(tail.Serial)
		out.Addr = h
		out.Port, _ = strconv.Atoi(ps)
	case local.Serial != "":
		if addrs, err := p.phoneTailnetAddrs(ctx, local.Serial); err == nil {
			out.Addr = ""
			for _, a := range addrs {
				if _, known := st.Device(a.String()); known || out.Addr == "" {
					out.Addr = a.String()
				}
			}
		}
		out.Listening = p.listenPort(ctx, local.Serial) == out.Port
	}
	switch {
	case !st.Running():
		out.Missing = driver.AwayNoTailscaleHere
		out.Detail = "Tailscale is not running on this computer"
		if st.Detail != "" {
			out.Detail += " (" + st.Detail + ")"
		}
		out.Detail += "."
	case out.Addr == "" && local.Serial != "":
		out.Missing = driver.AwayNoTailscaleThere
		out.Detail = "Tailscale is not connected on the phone: install it there, or open it, and sign in to the same account as on this computer."
	case out.Addr != "":
		if _, ok := st.Device(out.Addr); !ok {
			out.Missing = driver.AwayNoTailscaleThere
			out.Detail = "The phone's Tailscale is signed in to another tailnet than this computer's."
		}
	}
	if out.Missing == "" && local.Serial == "" && tail.Serial == "" && !on {
		out.Missing = driver.AwayNotConnected
		out.Detail = "Connect the phone to this computer by cable or Wi-Fi to set it up."
	}
	return out, nil
}

// SetAway implements driver.Tailnet.
func (p *Provider) SetAway(ctx context.Context, id string, on bool) (driver.Away, error) {
	if !on {
		if err := p.awayOff(ctx, id); err != nil {
			return driver.Away{}, err
		}
		return p.Away(ctx, id)
	}
	st, err := p.tailnetStatus(ctx)
	if err != nil {
		return driver.Away{}, err
	}
	if !st.Running() {
		return driver.Away{}, fmt.Errorf("android: Tailscale is not running on this computer (%s): %w", st.State, driver.ErrUnsupported)
	}
	entries, err := p.cfg.Client.Devices(ctx)
	if err != nil {
		return driver.Away{}, wrapErr(err)
	}
	local, tail := p.transports(ctx, entries, id)
	name := id
	var addr netip.Addr
	switch {
	case tail.Serial != "":
		// Connected through Tailscale already: its adb listens there.
		h, ps, _ := net.SplitHostPort(tail.Serial)
		addr, _ = netip.ParseAddr(h)
		if n, _ := strconv.Atoi(ps); n != AwayPort {
			return driver.Away{}, fmt.Errorf("android: %s is connected through Tailscale at port %d, which changes when Wireless debugging turns on again; connect it by cable or on this computer's Wi-Fi to set it up: %w", id, n, driver.ErrUnsupported)
		}
	case local.Serial == "":
		return driver.Away{}, fmt.Errorf("android: %s: connect the phone by cable or Wi-Fi to set it up: %w", id, driver.ErrOffline)
	default:
		addrs, err := p.phoneTailnetAddrs(ctx, local.Serial)
		if err != nil {
			return driver.Away{}, err
		}
		for _, a := range addrs {
			if _, ok := st.Device(a.String()); ok && a.Is4() {
				addr = a
				break
			}
		}
		if !addr.IsValid() {
			return driver.Away{}, fmt.Errorf("android: %s is not on this computer's tailnet: install Tailscale on the phone and sign in to the same account: %w", id, driver.ErrUnsupported)
		}
		if pr, err := p.deviceProps(ctx, local); err == nil {
			name = pr.Name
		}
		if p.listenPort(ctx, local.Serial) != AwayPort {
			p.cfg.logf("android: %s: having adb listen on the network at port %d, to reach the phone through Tailscale at %s", id, AwayPort, addr)
			if err := p.cfg.Client.TCPIP(ctx, local.Serial, AwayPort); err != nil {
				return driver.Away{}, fmt.Errorf("android: %s: adb tcpip: %w", id, wrapErr(err))
			}
		}
	}
	a := awayPhone{ID: id, Name: name, Addr: addr.String(), Port: AwayPort}
	if d, ok := st.Device(a.Addr); ok {
		a.Node = d.ID
	}
	if tail.Serial == "" {
		// adbd restarts: connect through Tailscale once it listens.
		if err := p.connectWhenUp(ctx, a.serial()); err != nil {
			return driver.Away{}, err
		}
	}
	if err := p.away.put(a); err != nil {
		return driver.Away{}, err
	}
	p.tail.soon()
	return p.Away(ctx, id)
}

// connectWhenUp connects to a phone whose adb is restarting to listen on
// the network, trying for up to 20 s.
func (p *Provider) connectWhenUp(ctx context.Context, serial string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		err := p.cfg.Client.Connect(ctx, serial)
		if err == nil {
			_, err = p.waitConnected(ctx, serial)
			return err
		}
		if sleepCtx(ctx, time.Second) != nil {
			return connectErr(serial, err)
		}
	}
}

// awayOff stops a phone's adb listening on the network and forgets its
// Tailscale address. A phone that cannot be reached keeps listening until
// it restarts.
func (p *Provider) awayOff(ctx context.Context, id string) error {
	_, kept := p.away.get(id)
	if _, err := p.away.remove(id); err != nil {
		return err
	}
	entries, err := p.cfg.Client.Devices(ctx)
	if err != nil {
		return wrapErr(err)
	}
	local, tail := p.transports(ctx, entries, id)
	serial := local.Serial
	if serial == "" {
		serial = tail.Serial
	}
	if serial != "" && (kept || p.listenPort(ctx, serial) > 0) {
		p.cfg.logf("android: %s: adb stops listening on the network", id)
		if err := p.cfg.Client.USB(ctx, serial); err != nil {
			return fmt.Errorf("android: %s: adb usb: %w", id, wrapErr(err))
		}
	}
	if tail.Serial != "" {
		if err := p.cfg.Client.Disconnect(ctx, tail.Serial); err != nil && !errors.Is(err, adb.ErrDeviceNotFound) {
			p.cfg.logf("android: disconnecting %s: %v", tail.Serial, err)
		}
	}
	return nil
}

// Reconnecting the phones set up for away from home. List starts a pass
// now and then; a pass connects, through Tailscale, each such phone that
// Tailscale says is online and that has no connection there yet.

const (
	tailSoon  = 3 * time.Second  // after a phone came or went, or one was set up
	tailAway  = 30 * time.Second // while a phone set up is not connected
	tailIdle  = time.Minute      // while every one is
	tailFirst = 30 * time.Second // the wait after a failed connection, doubled each time
	tailMax   = 10 * time.Minute
)

type tailLink struct {
	p   *Provider
	now func() time.Time

	mu      sync.Mutex
	running bool
	next    time.Time
	online  string
	tries   map[string]tailTry // failed connections, by phone id
	noted   map[string]string
}

type tailTry struct {
	count int
	until time.Time
}

func newTailLink(p *Provider) *tailLink {
	return &tailLink{p: p, now: time.Now, tries: map[string]tailTry{}, noted: map[string]string{}}
}

// soon has the next pass come in a few seconds.
func (t *tailLink) soon() {
	t.mu.Lock()
	if n := t.now().Add(tailSoon); t.next.After(n) {
		t.next = n
	}
	t.mu.Unlock()
}

// kick starts a pass in the background when one is due.
func (t *tailLink) kick(entries []adb.DeviceEntry) {
	phones := t.p.away.list()
	if len(phones) == 0 {
		return
	}
	now := t.now()
	online := inUseSerials(entries)
	t.mu.Lock()
	if online != t.online {
		t.online = online
		if n := now.Add(tailSoon); t.next.After(n) {
			t.next = n
		}
	}
	if t.running || now.Before(t.next) {
		t.mu.Unlock()
		return
	}
	t.running = true
	t.mu.Unlock()
	go t.pass(phones, entries)
}

func (t *tailLink) pass(phones []awayPhone, entries []adb.DeviceEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	missing := t.reconnect(ctx, phones, entries)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running = false
	wait := tailIdle
	if missing {
		wait = tailAway
	}
	t.next = t.now().Add(wait)
}

// reconnect connects the phones set up for away from home that Tailscale
// says are online and that have no connection through it. It reports
// whether one is still not connected.
func (t *tailLink) reconnect(ctx context.Context, phones []awayPhone, entries []adb.DeviceEntry) (missing bool) {
	p := t.p
	var want []awayPhone
	for _, a := range phones {
		if !p.forgot(a.ID) && !serialInUse(entries, a.serial()) {
			want = append(want, a)
		}
	}
	if len(want) == 0 {
		return false
	}
	st, err := p.tailnetStatus(ctx)
	switch {
	case err != nil:
		t.note("", "android: reading Tailscale's status: "+err.Error())
		return true
	case !st.Running():
		t.note("", "android: Tailscale is not running on this computer ("+string(st.State)+"); the phones set up for away from home wait for it")
		return true
	}
	t.note("", "")
	for _, a := range want {
		d, ok := st.Device(a.Node)
		if !ok {
			d, ok = st.Device(a.Addr)
		}
		if ok && d.IPv4().IsValid() && d.IPv4().String() != a.Addr {
			// Tailscale gave the phone a new address (it signed in again).
			p.cfg.logf("android: %s's Tailscale address changed from %s to %s", a.ID, a.Addr, d.IPv4())
			a.Addr = d.IPv4().String()
			if err := p.away.put(a); err != nil {
				p.cfg.logf("android: keeping %s's new address: %v", a.ID, err)
			}
			if serialInUse(entries, a.serial()) {
				continue
			}
		}
		if ok && !d.Online {
			t.note(a.ID, "android: "+a.ID+" is set up for away from home, but Tailscale says it is offline (its Tailscale is off, or it has no network)")
			missing = true
			continue
		}
		if t.holdBack(a.ID) {
			missing = true
			continue
		}
		if err := p.cfg.Client.Connect(ctx, a.serial()); err != nil {
			t.fail(a, connectErr(a.serial(), err))
			missing = true
			continue
		}
		t.note(a.ID, "android: connected to "+a.ID+" through Tailscale at "+a.serial())
		t.mu.Lock()
		delete(t.tries, a.ID)
		t.mu.Unlock()
	}
	return missing
}

func (t *tailLink) holdBack(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.tries[id]
	return ok && t.now().Before(f.until)
}

// fail notes a failed connection and when to try again.
func (t *tailLink) fail(a awayPhone, err error) {
	t.mu.Lock()
	f := t.tries[a.ID]
	f.count++
	wait := tailFirst
	for i := 1; i < f.count && wait < tailMax; i++ {
		wait *= 2
	}
	wait = min(wait, tailMax)
	f.until = t.now().Add(wait)
	t.tries[a.ID] = f
	t.mu.Unlock()
	why := err.Error()
	if errors.Is(err, driver.ErrNotListening) {
		why = "its adb does not listen on the network (the phone restarted, or someone turned it off)"
	}
	t.note(a.ID, fmt.Sprintf("android: connecting to %s through Tailscale at %s: %s; trying again in %v", a.ID, a.serial(), why, wait))
}

// note logs a message about a phone ("" for Tailscale) when it differs
// from the last one; "" only clears the last one.
func (t *tailLink) note(id, msg string) {
	t.mu.Lock()
	same := t.noted[id] == msg
	t.noted[id] = msg
	t.mu.Unlock()
	if !same && msg != "" {
		t.p.cfg.logf("%s", msg)
	}
}

var _ driver.Tailnet = (*Provider)(nil)
