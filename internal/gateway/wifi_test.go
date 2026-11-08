package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// airProvider lists demo iPhones over a cable or Wi-Fi as conn says, and
// behaves as driver.WiFiFinder asks: a phone on Wi-Fi only is listed with
// SetupConfirmWiFi and not opened until UseOverWiFi, none is listed over
// Wi-Fi while it is off, and a forgotten one not at all.
type airProvider struct {
	*demo.Provider

	mu        sync.Mutex
	on        bool
	use       map[string]bool
	forgot    map[string]bool
	conn      map[string]string   // by id; "usb" when unset
	opens     map[string][]string // the connection of each Open, by id
	sessions  map[string][]*overDevice
	openDelay time.Duration // Open over Wi-Fi takes this long
}

func newAirProvider(devs ...*demo.Device) *airProvider {
	return &airProvider{Provider: demo.NewProvider(driver.IOS, devs...), use: map[string]bool{}, forgot: map[string]bool{},
		conn: map[string]string{}, opens: map[string][]string{}, sessions: map[string][]*overDevice{}}
}

func (p *airProvider) connOf(id string) string {
	if c := p.conn[id]; c != "" {
		return c
	}
	return "usb"
}

func (p *airProvider) List(ctx context.Context) ([]driver.Info, error) {
	infos, err := p.Provider.List(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []driver.Info
	for _, info := range infos {
		info.Connection = p.connOf(info.ID)
		if info.Connection == "wifi" {
			if !p.on || p.forgot[info.ID] || info.State == driver.StateOffline {
				continue // usbmuxd lists a phone on the network only while it is there
			}
			if !p.use[info.ID] {
				info.State, info.Setup, info.Detail = driver.StateNeedsSetup, driver.SetupConfirmWiFi, "Found on Wi-Fi."
			}
		}
		out = append(out, info)
	}
	return out, err
}

func (p *airProvider) Open(ctx context.Context, id string) (driver.Device, error) {
	p.mu.Lock()
	c, used, delay := p.connOf(id), p.use[id], p.openDelay
	p.mu.Unlock()
	if c == "wifi" && !used {
		return nil, errors.New("found on Wi-Fi; not used yet")
	}
	if c == "wifi" && delay > 0 {
		time.Sleep(delay)
	}
	dev, err := p.Provider.Open(ctx, id)
	if err != nil {
		return nil, err
	}
	od := &overDevice{Device: dev, conn: c}
	p.mu.Lock()
	p.opens[id] = append(p.opens[id], c)
	p.sessions[id] = append(p.sessions[id], od)
	p.mu.Unlock()
	return od, nil
}

func (p *airProvider) SetWiFi(on bool) {
	p.mu.Lock()
	p.on = on
	p.mu.Unlock()
}

func (p *airProvider) UseOverWiFi(id string) {
	p.mu.Lock()
	p.use[id] = true
	p.mu.Unlock()
}

func (p *airProvider) Forget(id string) {
	p.mu.Lock()
	p.forgot[id] = true
	delete(p.use, id)
	p.mu.Unlock()
}

func (p *airProvider) Remember(id string) {
	p.mu.Lock()
	delete(p.forgot, id)
	p.mu.Unlock()
}

func (p *airProvider) Disconnect(context.Context, string) error { return nil }

func (p *airProvider) set(id, conn string) {
	p.mu.Lock()
	p.conn[id] = conn
	p.mu.Unlock()
}

func (p *airProvider) state(id string) (used, forgot bool, opens []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.use[id], p.forgot[id], slices.Clone(p.opens[id])
}

// overDevice is a session over one connection, which dies with it.
type overDevice struct {
	driver.Device
	conn string
	dead atomic.Bool
}

func (d *overDevice) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	if d.dead.Load() {
		return nil, fmt.Errorf("the tunnel stopped: %w", driver.ErrOffline)
	}
	return d.Device.Observe(ctx, opt)
}

// pull kills the sessions of a phone, as pulling its cable does, before
// usbmuxd stops listing it there.
func (p *airProvider) pull(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.sessions[id] {
		d.dead.Store(true)
	}
}

func (d *overDevice) Info() driver.Info {
	info := d.Device.Info()
	info.Connection = d.conn
	return info
}

func viewOf(g *Gateway, id string) (DeviceView, bool) {
	for _, v := range g.Devices() {
		if v.Info.ID == id {
			return v, true
		}
	}
	return DeviceView{}, false
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestWiFiFoundPhone: a phone found on Wi-Fi that Pinky has not used waits
// for the person, unseen by agents; Use opens it, and is remembered.
func TestWiFiFoundPhone(t *testing.T) {
	dir := t.TempDir()
	newProvider := func() *airProvider {
		p := newAirProvider(demo.NewDevice("ipad", "Mum's iPad", driver.IOS), demo.NewDevice("iphone", "iPhone", driver.IOS))
		p.set("ipad", "wifi")
		return p
	}
	p := newProvider()
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p}; o.DataDir = dir })
	if _, ok := viewOf(g, "ipad"); ok {
		t.Fatal("a phone on Wi-Fi was listed with Wi-Fi off")
	}
	g.SetWiFi(true)
	waitFor(t, "the found iPad", func() bool { v, ok := viewOf(g, "ipad"); return ok && v.StatusLine == "Found on Wi-Fi" })
	v, _ := viewOf(g, "ipad")
	if v.Status != DeviceNeedsSetup || v.Info.Setup != driver.SetupConfirmWiFi {
		t.Fatalf("found iPad = %+v", v)
	}
	if used, _, opens := p.state("ipad"); used || len(opens) != 0 {
		t.Fatalf("the found iPad was used (%v) or opened (%v)", used, opens)
	}
	// The iPhone on the cable is the person's, and the provider is told.
	if used, _, _ := p.state("iphone"); !used {
		t.Fatal("a phone on a cable was not given to the provider as the person's")
	}

	// Agents do not see it.
	s := g.OpenSession(agent)
	t.Cleanup(s.Close)
	res := s.Connect(context.Background(), ConnectRequest{Task: "look", Device: "Mum's iPad"})
	if _, list, _ := strings.Cut(res.Message, "Connected phones:"); !res.IsError() || strings.Contains(list, "iPad") {
		t.Fatalf("an agent reached or saw the found iPad: %+v", res)
	}
	if res := s.Connect(context.Background(), ConnectRequest{Task: "look", Device: "ipad"}); !res.IsError() {
		t.Fatalf("an agent reached the found iPad by id: %+v", res)
	}

	if err := g.UseFoundPhone("nobody"); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("UseFoundPhone of an unknown phone: %v", err)
	}
	if err := g.UseFoundPhone("ipad"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the iPad online", func() bool { v, _ := viewOf(g, "ipad"); return v.Status == DeviceIdle })
	if _, _, opens := p.state("ipad"); !slices.Equal(opens, []string{"wifi"}) {
		t.Fatalf("opens %v", opens)
	}
	g.Flush()
	g.Close()

	// Remembered: after a restart the provider knows both before it lists.
	p2 := newProvider()
	newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p2}; o.DataDir = dir })
	for _, id := range []string{"ipad", "iphone"} {
		if used, _, _ := p2.state(id); !used {
			t.Fatalf("after a restart %s is not the person's", id)
		}
	}
}

// TestWiFiIgnoreFoundPhone: Ignore keeps a found phone off the list while
// it is on Wi-Fi; plugged in, it is back, and the person's.
func TestWiFiIgnoreFoundPhone(t *testing.T) {
	p := newAirProvider(demo.NewDevice("ipad", "Mum's iPad", driver.IOS))
	p.set("ipad", "wifi")
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p}; o.DataDir = t.TempDir() })
	g.SetWiFi(true)
	waitFor(t, "the found iPad", func() bool { _, ok := viewOf(g, "ipad"); return ok })
	if err := g.IgnoreFoundPhone(context.Background(), "ipad"); err != nil {
		t.Fatal(err)
	}
	if _, ok := viewOf(g, "ipad"); ok {
		t.Fatal("an ignored phone stayed on the list")
	}
	if _, forgot, _ := p.state("ipad"); !forgot {
		t.Fatal("the provider was not told to leave the ignored phone alone")
	}
	p.set("ipad", "usb")
	waitFor(t, "the iPad plugged in", func() bool { v, ok := viewOf(g, "ipad"); return ok && v.Status == DeviceIdle })
	if used, forgot, _ := p.state("ipad"); !used || forgot {
		t.Fatalf("plugged in: used %v, forgot %v", used, forgot)
	}
}

// TestWiFiCableMoves: a phone whose cable is pulled moves to Wi-Fi as the
// same phone and its task goes on: a call made while it moves waits for it.
// Plugged in again, it moves back to the cable.
func TestWiFiCableMoves(t *testing.T) {
	p := newAirProvider(demo.NewDevice("iphone", "iPhone", driver.IOS))
	p.openDelay = 150 * time.Millisecond
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p} })
	g.SetWiFi(true)
	s, _ := connect(t, g, "Check the weather")
	ctx := context.Background()

	p.set("iphone", "wifi") // the cable is pulled
	waitFor(t, "the move", func() bool { v, _ := viewOf(g, "iphone"); return v.Status == DeviceOffline })
	res := s.Observe(ctx, ObserveRequest{})
	if res.IsError() || res.Screen == nil {
		t.Fatalf("a call during the move: %+v", res)
	}
	if v, _ := viewOf(g, "iphone"); v.Info.Connection != "wifi" || v.TaskID == "" {
		t.Fatalf("after the move: %+v", v)
	}

	p.set("iphone", "usb") // plugged in again
	waitFor(t, "back on the cable", func() bool {
		_, _, opens := p.state("iphone")
		v, _ := viewOf(g, "iphone")
		return len(opens) == 3 && v.Status != DeviceOffline
	})
	if res := s.Observe(ctx, ObserveRequest{}); res.IsError() {
		t.Fatalf("after moving back: %+v", res)
	}
	if _, _, opens := p.state("iphone"); !slices.Equal(opens, []string{"usb", "wifi", "usb"}) {
		t.Fatalf("opens %v", opens)
	}

	// Wi-Fi turned off: the phone on Wi-Fi goes offline.
	p.set("iphone", "wifi")
	waitFor(t, "on Wi-Fi again", func() bool {
		v, _ := viewOf(g, "iphone")
		return v.Info.Connection == "wifi" && v.Status != DeviceOffline
	})
	g.SetWiFi(false)
	waitFor(t, "offline with Wi-Fi off", func() bool { v, _ := viewOf(g, "iphone"); return v.Status == DeviceOffline })
}

// TestWiFiFoundPhoneGone: a found phone that goes away leaves the list, as
// it was never the person's; one they chose to use stays, offline.
func TestWiFiFoundPhoneGone(t *testing.T) {
	ipad, ipad2 := demo.NewDevice("ipad", "Mum's iPad", driver.IOS), demo.NewDevice("ipad2", "My iPad", driver.IOS)
	p := newAirProvider(ipad, ipad2)
	p.set("ipad", "wifi")
	p.set("ipad2", "wifi")
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p} })
	g.SetWiFi(true)
	waitFor(t, "both iPads", func() bool { _, a := viewOf(g, "ipad"); _, b := viewOf(g, "ipad2"); return a && b })
	ipad.SetOffline(true)
	waitFor(t, "the found iPad gone", func() bool { _, ok := viewOf(g, "ipad"); return !ok })

	if err := g.UseFoundPhone("ipad2"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "my iPad online", func() bool { v, _ := viewOf(g, "ipad2"); return v.Status == DeviceIdle })
	ipad2.SetOffline(true)
	waitFor(t, "my iPad offline", func() bool { v, ok := viewOf(g, "ipad2"); return ok && v.Status == DeviceOffline })
}

// TestWiFiCablePulledMidRead: the cable is pulled while the agent reads the
// screen, before usbmuxd stops listing the phone on it. The read that hits
// the dead session reads again once the phone is back on Wi-Fi.
func TestWiFiCablePulledMidRead(t *testing.T) {
	p := newAirProvider(demo.NewDevice("iphone", "iPhone", driver.IOS))
	p.openDelay = 100 * time.Millisecond
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p}; o.DiscoverEvery = time.Hour })
	g.SetWiFi(true)
	s, _ := connect(t, g, "Check the weather")
	p.pull("iphone")
	go func() {
		time.Sleep(150 * time.Millisecond)
		p.set("iphone", "wifi") // usbmuxd catches up
		g.Refresh(context.Background())
	}()
	res := s.Observe(context.Background(), ObserveRequest{})
	if res.IsError() || res.Screen == nil {
		t.Fatalf("the read across the pull: %+v", res)
	}
	if v, _ := viewOf(g, "iphone"); v.Info.Connection != "wifi" {
		t.Fatalf("after the pull: %+v", v)
	}
}

// TestWiFiLockedPhone: a phone locked on Wi-Fi, which the provider lists
// offline until it is unlocked, reads as such, and agents are told to ask
// for an unlock.
func TestWiFiLockedPhone(t *testing.T) {
	ipad := demo.NewDevice("ipad", "My iPad", driver.IOS)
	p := &lockedProvider{airProvider: newAirProvider(ipad)}
	p.set("ipad", "wifi")
	p.UseOverWiFi("ipad")
	p.locked.Store(true)
	g, _ := newTestGateway(t, func(o *Options) { o.Providers = []driver.Provider{p} })
	g.SetWiFi(true)
	waitFor(t, "the locked iPad", func() bool { v, _ := viewOf(g, "ipad"); return v.StatusLine == "Locked on Wi-Fi" })
	if v, _ := viewOf(g, "ipad"); v.Status != DeviceOffline {
		t.Fatalf("at rest, the locked iPad = %+v", v)
	}
	// An agent leases it as a phone that waits for a step, and goes on
	// once it is unlocked.
	s := g.OpenSession(agent)
	t.Cleanup(s.Close)
	res := s.Connect(context.Background(), ConnectRequest{Task: "look", Device: "ipad"})
	if res.Status != StatusNeedsSetup || res.Setup != driver.SetupUnlock {
		t.Fatalf("connect to the locked iPad: %+v", res)
	}
	if v, _ := viewOf(g, "ipad"); v.Status != DeviceNeedsSetup {
		t.Fatalf("with an agent waiting, the locked iPad = %+v", v)
	}
	p.locked.Store(false)
	waitFor(t, "the iPad unlocked", func() bool { v, _ := viewOf(g, "ipad"); return v.Status == DeviceWorking })
	if res := s.Observe(context.Background(), ObserveRequest{}); res.IsError() || res.Screen == nil {
		t.Fatalf("a read once unlocked: %+v", res)
	}
}

// lockedProvider lists its phones on Wi-Fi locked while locked is set, as
// the iOS provider lists a phone locked on Wi-Fi.
type lockedProvider struct {
	*airProvider
	locked atomic.Bool
}

func (p *lockedProvider) List(ctx context.Context) ([]driver.Info, error) {
	infos, err := p.airProvider.List(ctx)
	for i, info := range infos {
		if info.Connection == "wifi" && info.State == driver.StateOnline && p.locked.Load() {
			infos[i].State, infos[i].Setup, infos[i].Detail = driver.StateNeedsSetup, driver.SetupUnlock, "Unlock the iPhone."
		}
	}
	return infos, err
}
