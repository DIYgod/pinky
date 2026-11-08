package android

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/tailnet"
)

const (
	tailAddr   = "100.101.2.2"
	tailSerial = tailAddr + ":5555"
	localWiFi  = "192.168.1.42:41000" // the phone over wireless debugging
	piHW       = "R5CN20ABC"          // its hardware serial
)

// fakeTailnet is Tailscale on the computer, as Config.Tailnet reads it.
type fakeTailnet struct {
	mu    sync.Mutex
	st    tailnet.Status
	err   error
	reads int
}

func (f *fakeTailnet) read(context.Context) (tailnet.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.st, f.err
}

func (f *fakeTailnet) set(fn func(st *tailnet.Status)) {
	f.mu.Lock()
	fn(&f.st)
	f.mu.Unlock()
}

func runningTailnet() *fakeTailnet {
	return &fakeTailnet{st: tailnet.Status{
		State: tailnet.Running, Tailnet: "someone@example.com",
		Self: tailnet.Device{Name: "macbook", Addrs: []netip.Addr{netip.MustParseAddr("100.101.1.1")}},
		Peers: []tailnet.Device{
			{ID: "nPixel", Name: "pixel-9", OS: "android", Online: true, Addrs: []netip.Addr{netip.MustParseAddr(tailAddr)}},
			{ID: "nNAS", Name: "nas", OS: "linux", Online: true, Addrs: []netip.Addr{netip.MustParseAddr("100.101.4.4")}},
		},
	}}
}

// phoneProps is the answer to deviceProps' getprop line.
const phoneProps = "Pixel 9\n16\n36\nPixel 9\n" + piHW + "\n"

// fakePhone is the phone's side of the services the tailnet code uses: its
// interfaces, and adb listening on the network.
type fakePhone struct {
	mu       sync.Mutex
	f        *fakeADB
	addrs    string // `ip -o addr show`
	port     int    // service.adb.tcp.port
	restarts []string
}

func (ph *fakePhone) run(serial, service string) (string, bool) {
	ph.mu.Lock()
	defer ph.mu.Unlock()
	switch {
	case strings.HasPrefix(service, "shell:ip -o addr show"):
		return ph.addrs, true
	case service == "shell:getprop service.adb.tcp.port":
		if ph.port == 0 {
			return "\n", true
		}
		return strconv.Itoa(ph.port) + "\n", true
	case strings.HasPrefix(service, "tcpip:"):
		ph.restarts = append(ph.restarts, service)
		ph.port = 5555
		go ph.f.set(func(f *fakeADB) { f.connectOK[tailSerial] = true })
		return "restarting in TCP mode port: 5555\n", true
	case service == "usb:":
		ph.restarts = append(ph.restarts, service+serial)
		ph.port = 0
		go ph.f.set(func(f *fakeADB) { delete(f.connectOK, tailSerial) })
		return "restarting in USB mode\n", true
	}
	return "", false
}

func newTailnetFake(t *testing.T) (*fakeADB, *fakePhone, *fakeTailnet, *Provider) {
	f := startFakeADB(t)
	ts := runningTailnet()
	ph := &fakePhone{f: f, addrs: "1: lo    inet 127.0.0.1/8 scope host lo\n" +
		"30: wlan0    inet 192.168.1.42/24 brd 192.168.1.255 scope global wlan0\n" +
		"31: tun0    inet " + tailAddr + "/32 scope global tun0\n" +
		"31: tun0    inet6 fd7a:115c:a1e0::2/128 scope global\n"}
	f.set(func(f *fakeADB) {
		f.props = map[string]string{localWiFi: phoneProps, tailSerial: phoneProps}
		f.connectOK = map[string]bool{}
		f.run = ph.run
	})
	cfg := f.config()
	cfg.Tailnet = ts.read
	cfg.AwayFile = filepath.Join(t.TempDir(), "away-phones.json")
	return f, ph, ts, NewProvider(cfg)
}

func TestParseTailnetAddrs(t *testing.T) {
	out := "1: lo    inet 127.0.0.1/8 scope host lo\\       valid_lft forever preferred_lft forever\n" +
		"30: wlan0    inet 192.168.5.137/24 brd 192.168.5.255 scope global wlan0\n" +
		"31: tun0    inet6 fd7a:115c:a1e0::2/128 scope global\n" +
		"31: tun0    inet 100.101.2.2/32 scope global tun0\n" +
		"40: rmnet_data1    inet 100.70.8.9/30 scope global rmnet_data1\n" // carrier NAT shares the range
	got := parseTailnetAddrs(out)
	want := []netip.Addr{netip.MustParseAddr("100.101.2.2"), netip.MustParseAddr("fd7a:115c:a1e0::2")}
	if !slices.Equal(got, want) {
		t.Fatalf("addrs = %v; want %v", got, want)
	}
	if got := parseTailnetAddrs("30: wlan0    inet 192.168.5.137/24\n"); len(got) != 0 {
		t.Fatalf("no Tailscale: %v", got)
	}
}

func TestSplitTailnetAddr(t *testing.T) {
	st := runningTailnet().st
	st.Peers[0].DNSName = "pixel-9.tail1234.ts.net"
	for in, want := range map[string]string{
		"100.101.2.2":             "100.101.2.2:5555",
		"100.101.2.2:41000":       "100.101.2.2:41000",
		"pixel-9":                 "100.101.2.2:5555",
		"pixel-9.tail1234.ts.net": "100.101.2.2:5555",
		"PIXEL-9:37000":           "100.101.2.2:37000",
	} {
		h, p, err := splitTailnetAddr(in, st)
		if err != nil || h+":"+strconv.Itoa(p) != want {
			t.Errorf("%q: %s:%d, %v; want %s", in, h, p, err, want)
		}
	}
	for _, in := range []string{"192.168.1.42:5555", "100.101.2.2:x", "galaxy", ""} {
		if h, p, err := splitTailnetAddr(in, st); err == nil {
			t.Errorf("%q: %s:%d, want an error", in, h, p)
		}
	}
}

func TestViaTailnet(t *testing.T) {
	if !viaTailnet(tailSerial) || viaTailnet(localWiFi) || viaTailnet("adb-x._adb-tls-connect._tcp") || viaTailnet("3e8be48e") {
		t.Fatal("viaTailnet")
	}
	e := entriesFrom(tailSerial + "\tdevice product:x model:Pixel_9 device:x transport_id:8")
	if info := infoFromEntry(e[0]); info.Connection != "wifi" || info.Via != driver.ViaTailscale {
		t.Fatalf("info = %+v", info)
	}
	// The local network before Tailscale, USB before both.
	local := driver.Info{State: driver.StateOnline, Connection: "wifi"}
	tail := driver.Info{State: driver.StateOnline, Connection: "wifi", Via: driver.ViaTailscale}
	usb := driver.Info{State: driver.StateOnline, Connection: "usb"}
	if !better(local, tail) || better(tail, local) || !better(usb, tail) || better(tail, usb) || !better(usb, local) {
		t.Fatal("better")
	}
	if !better(tail, driver.Info{State: driver.StateOffline, Connection: "usb"}) {
		t.Fatal("an online Tailscale connection before an offline cable")
	}
}

func entriesFrom(lines ...string) []adb.DeviceEntry {
	return adb.ParseDevices(strings.Join(lines, "\n"))
}

func TestAwayStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "away-phones.json")
	s := &awayStore{path: path, logf: t.Logf}
	if err := s.put(awayPhone{ID: piHW, Name: "Pixel 9", Node: "nPixel", Addr: tailAddr, Port: 5555}); err != nil {
		t.Fatal(err)
	}
	if err := s.put(awayPhone{ID: "other", Addr: "100.101.3.3"}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v, %v", fi, err)
	}
	// Read back from the file.
	again := &awayStore{path: path, logf: t.Logf}
	got, ok := again.get(piHW)
	if !ok || got.Node != "nPixel" || got.serial() != tailSerial || len(again.list()) != 2 {
		t.Fatalf("read back %+v, %v; list %+v", got, ok, again.list())
	}
	if o, _ := again.get("other"); o.serial() != "100.101.3.3:5555" {
		t.Fatalf("no port means 5555: %s", o.serial())
	}
	if removed, err := again.remove(piHW); !removed || err != nil {
		t.Fatalf("remove: %v, %v", removed, err)
	}
	if removed, _ := again.remove(piHW); removed {
		t.Fatal("removed twice")
	}
	third := &awayStore{path: path, logf: t.Logf}
	if _, ok := third.get(piHW); ok || len(third.list()) != 1 {
		t.Fatalf("after remove: %+v", third.list())
	}
	// A broken file is no phones, not a failure.
	os.WriteFile(path, []byte("{"), 0o600)
	if l := (&awayStore{path: path, logf: t.Logf}).list(); len(l) != 0 {
		t.Fatalf("broken file: %+v", l)
	}
}

func TestTailnetList(t *testing.T) {
	f, _, ts, p := newTailnetFake(t)
	ctx := context.Background()
	st, err := p.Tailnet(ctx)
	if err != nil || st.State != driver.TailnetRunning || st.Tailnet != "someone@example.com" || st.Self != "macbook" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if len(st.Phones) != 1 || st.Phones[0].Name != "pixel-9" || st.Phones[0].Addr != tailAddr || !st.Phones[0].Online ||
		st.Phones[0].Connected || st.Phones[0].ID != "" {
		t.Fatalf("phones = %+v", st.Phones)
	}
	// Connected through Tailscale: Pinky knows which phone it is.
	f.set(func(f *fakeADB) {
		f.devices = []string{tailSerial + "\tdevice product:x model:Pixel_9 device:x transport_id:8"}
	})
	st, _ = p.Tailnet(ctx)
	if !st.Phones[0].Connected || st.Phones[0].ID != piHW {
		t.Fatalf("connected: %+v", st.Phones)
	}
	// Not installed is a state, not an error.
	ts.set(func(s *tailnet.Status) { *s = tailnet.Status{} })
	ts.mu.Lock()
	ts.err = tailnet.ErrNotInstalled
	ts.mu.Unlock()
	if st, err := p.Tailnet(ctx); err != nil || st.State != driver.TailnetNotInstalled {
		t.Fatalf("not installed: %+v, %v", st, err)
	}
}

func TestConnectTailnet(t *testing.T) {
	f, _, ts, p := newTailnetFake(t)
	ctx := context.Background()
	// adb does not listen there.
	if _, err := p.ConnectTailnet(ctx, tailAddr); !errors.Is(err, driver.ErrNotListening) {
		t.Fatalf("refused: %v", err)
	}
	if _, kept := p.away.get(piHW); kept {
		t.Fatal("kept a phone that refused")
	}
	// It does: connected, and kept as set up for away from home.
	f.set(func(f *fakeADB) { f.connectOK[tailSerial] = true })
	info, err := p.ConnectTailnet(ctx, "pixel-9")
	if err != nil || info.ID != piHW || info.Via != driver.ViaTailscale || info.State != driver.StateOnline {
		t.Fatalf("connect: %+v, %v", info, err)
	}
	if a, kept := p.away.get(piHW); !kept || a.Node != "nPixel" || a.serial() != tailSerial {
		t.Fatalf("kept %+v, %v", a, kept)
	}
	// At Wireless debugging's port: connected, not kept (the port changes).
	p.away.remove(piHW)
	f.set(func(f *fakeADB) { f.connectOK[tailAddr+":37000"] = true; f.props[tailAddr+":37000"] = phoneProps })
	if _, err := p.ConnectTailnet(ctx, tailAddr+":37000"); err != nil {
		t.Fatal(err)
	}
	if _, kept := p.away.get(piHW); kept {
		t.Fatal("kept a phone reached at Wireless debugging's port")
	}
	// Not an address on the tailnet; Tailscale off.
	if _, err := p.ConnectTailnet(ctx, "192.168.1.42:5555"); err == nil {
		t.Fatal("connected to a local address")
	}
	ts.set(func(s *tailnet.Status) { s.State = tailnet.Stopped })
	if _, err := p.ConnectTailnet(ctx, tailAddr); !errors.Is(err, driver.ErrUnsupported) {
		t.Fatalf("Tailscale off: %v", err)
	}
}

func TestSetAway(t *testing.T) {
	f, ph, ts, p := newTailnetFake(t)
	ctx := context.Background()
	f.set(func(f *fakeADB) {
		f.devices = []string{localWiFi + "\tdevice product:x model:Pixel_9 device:x transport_id:7"}
	})
	a, err := p.Away(ctx, piHW)
	if err != nil || a.On || a.Listening || a.Addr != tailAddr || a.Missing != "" {
		t.Fatalf("before: %+v, %v", a, err)
	}
	a, err = p.SetAway(ctx, piHW, true)
	if err != nil || !a.On || !a.Listening || a.Addr != tailAddr || a.Port != 5555 {
		t.Fatalf("on: %+v, %v", a, err)
	}
	ph.mu.Lock()
	restarts := slices.Clone(ph.restarts)
	ph.mu.Unlock()
	if !slices.Equal(restarts, []string{"tcpip:5555"}) {
		t.Fatalf("restarts = %q", restarts)
	}
	if kept, ok := p.away.get(piHW); !ok || kept.Node != "nPixel" || kept.Name != "Pixel 9" {
		t.Fatalf("kept %+v", kept)
	}
	// On again: adb listens already, nothing restarts.
	if _, err := p.SetAway(ctx, piHW, true); err != nil {
		t.Fatal(err)
	}
	ph.mu.Lock()
	n := len(ph.restarts)
	ph.mu.Unlock()
	if n != 1 {
		t.Fatalf("restarted adb again: %d", n)
	}
	// Off: adb stops listening, the Tailscale connection goes.
	a, err = p.SetAway(ctx, piHW, false)
	if err != nil || a.On {
		t.Fatalf("off: %+v, %v", a, err)
	}
	ph.mu.Lock()
	restarts = slices.Clone(ph.restarts)
	ph.mu.Unlock()
	if restarts[len(restarts)-1] != "usb:"+localWiFi {
		t.Fatalf("restarts = %q", restarts)
	}
	f.mu.Lock()
	disc := slices.Clone(f.disconnects)
	f.mu.Unlock()
	if !slices.Contains(disc, tailSerial) {
		t.Fatalf("disconnects = %q", disc)
	}
	if _, kept := p.away.get(piHW); kept {
		t.Fatal("still kept")
	}

	// The phone has no Tailscale: say so, change nothing.
	ph.mu.Lock()
	ph.addrs = "30: wlan0    inet 192.168.1.42/24 scope global wlan0\n"
	ph.mu.Unlock()
	if a, _ := p.Away(ctx, piHW); a.Missing != driver.AwayNoTailscaleThere {
		t.Fatalf("no Tailscale on the phone: %+v", a)
	}
	if _, err := p.SetAway(ctx, piHW, true); err == nil {
		t.Fatal("set up a phone without Tailscale")
	}
	// None here.
	ts.set(func(s *tailnet.Status) { s.State = tailnet.NotRunning })
	if a, _ := p.Away(ctx, piHW); a.Missing != driver.AwayNoTailscaleHere {
		t.Fatalf("no Tailscale here: %+v", a)
	}
	// Not connected at all.
	ts.set(func(s *tailnet.Status) { s.State = tailnet.Running })
	f.set(func(f *fakeADB) { f.devices = nil })
	if a, _ := p.Away(ctx, piHW); a.Missing != driver.AwayNotConnected {
		t.Fatalf("not connected: %+v", a)
	}
	if _, err := p.SetAway(ctx, piHW, true); !errors.Is(err, driver.ErrOffline) {
		t.Fatalf("set up a phone that is not connected: %v", err)
	}
}

func TestTailReconnect(t *testing.T) {
	f, _, ts, p := newTailnetFake(t)
	ctx := context.Background()
	now := time.Now()
	p.tail.now = func() time.Time { return now }
	p.away.put(awayPhone{ID: piHW, Node: "nPixel", Addr: tailAddr, Port: 5555})
	connects := func() []string {
		f.mu.Lock()
		defer f.mu.Unlock()
		return slices.Clone(f.connects)
	}
	// adb does not listen: a failed try, then none until the wait is over.
	if missing := p.tail.reconnect(ctx, p.away.list(), entries(t, p)); !missing || len(connects()) != 1 {
		t.Fatalf("refused: missing %v, connects %q", missing, connects())
	}
	p.tail.reconnect(ctx, p.away.list(), entries(t, p))
	if len(connects()) != 1 {
		t.Fatalf("tried again at once: %q", connects())
	}
	now = now.Add(tailFirst + time.Second)
	f.set(func(f *fakeADB) { f.connectOK[tailSerial] = true })
	if missing := p.tail.reconnect(ctx, p.away.list(), entries(t, p)); missing || len(connects()) != 2 {
		t.Fatalf("listening: missing %v, connects %q", missing, connects())
	}
	// Connected: nothing to do.
	if missing := p.tail.reconnect(ctx, p.away.list(), entries(t, p)); missing || len(connects()) != 2 {
		t.Fatalf("connected: missing %v, connects %q", missing, connects())
	}
	// Offline on the tailnet: no try.
	f.set(func(f *fakeADB) { f.devices = nil })
	ts.set(func(s *tailnet.Status) { s.Peers[0].Online = false })
	if missing := p.tail.reconnect(ctx, p.away.list(), entries(t, p)); !missing || len(connects()) != 2 {
		t.Fatalf("offline: missing %v, connects %q", missing, connects())
	}
	// A new address: kept, and used.
	ts.set(func(s *tailnet.Status) {
		s.Peers[0].Online = true
		s.Peers[0].Addrs = []netip.Addr{netip.MustParseAddr("100.101.9.9")}
	})
	f.set(func(f *fakeADB) { f.connectOK["100.101.9.9:5555"] = true })
	p.tail.reconnect(ctx, p.away.list(), entries(t, p))
	if c := connects(); c[len(c)-1] != "100.101.9.9:5555" {
		t.Fatalf("connects %q", c)
	}
	if a, _ := p.away.get(piHW); a.Addr != "100.101.9.9" {
		t.Fatalf("kept %+v", a)
	}
	// Removed from Pinky: left alone.
	f.set(func(f *fakeADB) { f.devices = nil })
	p.Forget(piHW)
	n := len(connects())
	if missing := p.tail.reconnect(ctx, p.away.list(), entries(t, p)); missing || len(connects()) != n {
		t.Fatalf("forgotten: missing %v, connects %q", missing, connects())
	}
	// Tailscale off here: nothing to try.
	p.Remember(piHW)
	ts.set(func(s *tailnet.Status) { s.State = tailnet.Stopped })
	if missing := p.tail.reconnect(ctx, p.away.list(), entries(t, p)); !missing || len(connects()) != n {
		t.Fatalf("Tailscale off: missing %v, connects %q", missing, connects())
	}
}
