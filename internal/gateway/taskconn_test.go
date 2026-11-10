package gateway

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
)

// twoWayPhone is an iPhone on iOS 27 that Pinky can reach over CoreDevice
// or through the WebDriverAgent helper, as the iOS provider does.
type twoWayPhone struct {
	*demo.Provider

	mu        sync.Mutex
	way       string // the provider's way: "", "coredevice" or "wda"
	noHelper  bool   // the helper is not installed
	helperErr error  // opening through the helper fails
	passcode  bool   // the helper waits for the passcode
	opens     []string
}

func newTwoWayPhone() (*twoWayPhone, *demo.Device) {
	dev := demo.NewDevice("iphone", "iPhone 13 mini", driver.IOS)
	return &twoWayPhone{Provider: demo.NewProvider(driver.IOS, dev)}, dev
}

func (p *twoWayPhone) backendLocked() string {
	if p.way == wayHelper {
		return wayHelper
	}
	return wayCoreDevice
}

func (p *twoWayPhone) List(ctx context.Context) ([]driver.Info, error) {
	infos, err := p.Provider.List(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range infos {
		infos[i].Backend = p.backendLocked()
		if p.way == wayHelper && p.passcode && infos[i].State == driver.StateOnline {
			infos[i].State, infos[i].Detail = driver.StateNeedsSetup, "Enter the passcode on the iPhone."
		}
	}
	return infos, err
}

func (p *twoWayPhone) Open(ctx context.Context, id string) (driver.Device, error) {
	p.mu.Lock()
	b := p.backendLocked()
	p.opens = append(p.opens, b)
	err := p.helperErr
	p.mu.Unlock()
	if b == wayHelper && err != nil {
		return nil, err
	}
	return p.Provider.Open(ctx, id)
}

func (p *twoWayPhone) SetConnection(id, way string) error {
	switch way {
	case "", wayCoreDevice, wayHelper:
	default:
		return driver.ErrUnsupported
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.way = way
	return nil
}

func (p *twoWayPhone) Connections(_ context.Context, id string) driver.Connections {
	p.mu.Lock()
	defer p.mu.Unlock()
	wda := driver.Connection{ID: wayHelper, Available: !p.noHelper}
	if p.noHelper {
		wda.Reason, wda.Detail = "not_installed", "The WebDriverAgent helper is not installed on this iPhone."
	}
	return driver.Connections{Chosen: p.way, Current: p.backendLocked(),
		Ways: []driver.Connection{{ID: "", Available: true}, {ID: wayCoreDevice, Available: true}, wda}}
}

func (p *twoWayPhone) state() (string, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.way, append([]string(nil), p.opens...)
}

func twoWayGateway(t *testing.T, p *twoWayPhone, opts ...func(*Options)) *Gateway {
	t.Helper()
	g, _ := newTestGateway(t, append([]func(*Options){func(o *Options) { o.Providers = []driver.Provider{p} }}, opts...)...)
	waitUntil(t, "the iPhone online", func() bool { ds := g.Devices(); return len(ds) == 1 && ds[0].Status == DeviceIdle })
	return g
}

func backendOf(g *Gateway) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := g.devices["iphone"]
	if !d.online {
		return ""
	}
	return d.info.Backend
}

func shortSwitchBack(t *testing.T) {
	old := switchBackAfter
	switchBackAfter = 50 * time.Millisecond
	t.Cleanup(func() { switchBackAfter = old })
}

func TestTaskConnectionWords(t *testing.T) {
	for in, want := range map[string]string{"": "", "auto": "", "Helper": wayHelper, "wda": wayHelper, " coredevice ": wayCoreDevice} {
		if got, ok := TaskConnection(in); !ok || got != want {
			t.Errorf("TaskConnection(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := TaskConnection("usb"); ok {
		t.Error("an unknown word is taken")
	}
	for _, app := range []string{"com.dbs.sg.dbsmbanking", "digibank", "招商银行", "cmb.pb", "China Merchants Bank"} {
		if _, ok := refusesSharing(app); !ok {
			t.Errorf("%s is not known to refuse screen sharing", app)
		}
	}
	if _, ok := refusesSharing("com.apple.calculator"); ok {
		t.Error("Calculator refuses screen sharing?")
	}
}

func TestConnectAsksForTheHelper(t *testing.T) {
	shortSwitchBack(t)
	p, _ := newTwoWayPhone()
	g := twoWayGateway(t, p)
	s := g.OpenSession(agent)
	defer s.Close()
	res := s.Connect(context.Background(), ConnectRequest{Task: "Balances", Connection: "helper"})
	mustStatus(t, res, StatusSettled)
	if !strings.Contains(res.Message, "WebDriverAgent helper") || res.Screen == nil {
		t.Fatalf("connect: %q, screen %v", res.Message, res.Screen != nil)
	}
	if b := backendOf(g); b != wayHelper {
		t.Fatalf("backend %q during the task", b)
	}
	// Nothing is saved: the person's choice stays Automatic.
	if c, _ := g.Connections(context.Background(), "iphone"); c.Chosen != wayHelper {
		// The provider holds the task's way; the gateway's record of the
		// person's choice does not.
		t.Fatalf("provider way %q", c.Chosen)
	}
	g.mu.Lock()
	_, saved := g.chosen["iphone"]
	g.mu.Unlock()
	if saved {
		t.Fatal("the task's connection was taken for the person's choice")
	}
	mustStatus(t, s.Release(context.Background(), ReleaseRequest{}), StatusSettled)
	waitUntil(t, "the phone back on CoreDevice", func() bool { return backendOf(g) == wayCoreDevice })
	if way, _ := p.state(); way != "" {
		t.Fatalf("provider way %q after the task", way)
	}
}

func TestLaunchMovesToTheHelper(t *testing.T) {
	shortSwitchBack(t)
	p, _ := newTwoWayPhone()
	g := twoWayGateway(t, p)
	s, _ := connect(t, g, "Bookkeeping")
	if b := backendOf(g); b != wayCoreDevice {
		t.Fatalf("a task that names no such app moved to %q", b)
	}
	res := s.Act(context.Background(), Call{}, Action{Tool: "launch", App: "招商银行"})
	if !strings.Contains(res.Message, "China Merchants Bank refuses to run while the screen is shared") {
		t.Fatalf("launch: %+v", res)
	}
	if b := backendOf(g); b != wayHelper {
		t.Fatalf("backend %q after launching the bank", b)
	}
	// A second launch stays there and says nothing more.
	res = s.Act(context.Background(), Call{}, Action{Tool: "launch", App: "cmb.pb"})
	if strings.Contains(res.Message, "moved") {
		t.Fatalf("launched again: %q", res.Message)
	}
	// The next task within switchBackAfter keeps it; the phone goes back
	// once no task holds it.
	mustStatus(t, s.Release(context.Background(), ReleaseRequest{}), StatusSettled)
	waitUntil(t, "the phone back on CoreDevice", func() bool { return backendOf(g) == wayCoreDevice })
}

func TestAppListMovesToTheHelper(t *testing.T) {
	p, _ := newTwoWayPhone()
	g := twoWayGateway(t, p)
	s := g.OpenSession(agent)
	defer s.Close()
	res := s.Connect(context.Background(), ConnectRequest{Task: "DBS balance", Apps: []string{"digibank"}})
	mustStatus(t, res, StatusSettled)
	if b := backendOf(g); b != wayHelper || !strings.Contains(res.Message, "DBS digibank") {
		t.Fatalf("backend %q, message %q", b, res.Message)
	}
	// The agent can ask to go back.
	res = s.Connect(context.Background(), ConnectRequest{Task: "DBS balance", Connection: "coredevice"})
	mustStatus(t, res, StatusSettled)
	if b := backendOf(g); b != wayCoreDevice {
		t.Fatalf("backend %q after asking for CoreDevice", b)
	}
}

func TestPersonsConnectionIsKept(t *testing.T) {
	p, _ := newTwoWayPhone()
	g := twoWayGateway(t, p)
	if err := g.SetConnection("iphone", wayCoreDevice); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the phone back", func() bool { return backendOf(g) == wayCoreDevice })
	_, before := p.state()
	s := g.OpenSession(agent)
	defer s.Close()
	res := s.Connect(context.Background(), ConnectRequest{Task: "Balances", Connection: "helper"})
	mustStatus(t, res, StatusSettled)
	if !strings.Contains(res.Message, "Settings › Phones") {
		t.Fatalf("connect: %q", res.Message)
	}
	s.Act(context.Background(), Call{}, Action{Tool: "launch", App: "digibank"})
	if _, opens := p.state(); len(opens) != len(before) || backendOf(g) != wayCoreDevice {
		t.Fatalf("opens %v after %v", opens, before)
	}
}

func TestHelperMissingOrFailing(t *testing.T) {
	p, _ := newTwoWayPhone()
	p.noHelper = true
	g := twoWayGateway(t, p)
	s, _ := connect(t, g, "Bookkeeping")
	res := s.Act(context.Background(), Call{}, Action{Tool: "launch", App: "digibank"})
	if !strings.Contains(res.Message, "not installed") {
		t.Fatalf("no helper: %q", res.Message)
	}
	if _, opens := p.state(); len(opens) != 1 {
		t.Fatalf("opened %v without a helper", opens)
	}

	// The helper is there but does not start: back on CoreDevice.
	p.mu.Lock()
	p.noHelper, p.helperErr = false, errors.New("the runner quit")
	p.mu.Unlock()
	res = s.Act(context.Background(), Call{}, Action{Tool: "launch", App: "digibank"})
	if !strings.Contains(res.Message, "stays on CoreDevice") {
		t.Fatalf("failing helper: %+v", res)
	}
	if b := backendOf(g); b != wayCoreDevice {
		t.Fatalf("backend %q after the helper failed", b)
	}
	if way, _ := p.state(); way != "" {
		t.Fatalf("provider way %q after the helper failed", way)
	}
}

func TestHelperWaitsForPasscode(t *testing.T) {
	p, _ := newTwoWayPhone()
	p.passcode = true
	g := twoWayGateway(t, p, func(o *Options) { o.ReconnectGrace = time.Minute })
	s := g.OpenSession(agent)
	defer s.Close()
	start := time.Now()
	res := s.Connect(context.Background(), ConnectRequest{Task: "Balances", Connection: "helper"})
	if res.Status != StatusNeedsSetup || !strings.Contains(res.Message, "passcode") || res.Task == "" {
		t.Fatalf("connect: %+v", res)
	}
	if time.Since(start) > switchWait/2 {
		t.Fatalf("waited %s for a phone that waits for the person", time.Since(start))
	}
	// The person enters it: the task goes on through the helper.
	p.mu.Lock()
	p.passcode = false
	p.mu.Unlock()
	waitUntil(t, "the phone on the helper", func() bool { return backendOf(g) == wayHelper })
	mustStatus(t, s.Observe(context.Background(), ObserveRequest{}), StatusSettled)
}

func TestPersonChoiceEndsTaskConnection(t *testing.T) {
	p, _ := newTwoWayPhone()
	g := twoWayGateway(t, p)
	s := g.OpenSession(agent)
	defer s.Close()
	mustStatus(t, s.Connect(context.Background(), ConnectRequest{Task: "Balances", Connection: "helper"}), StatusSettled)
	if err := g.SetConnection("iphone", driver.ConnectionAuto); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	way := g.devices["iphone"].taskWay
	g.mu.Unlock()
	if way != "" {
		t.Fatalf("task way %q after the person chose", way)
	}
	waitUntil(t, "the phone on CoreDevice", func() bool { return backendOf(g) == wayCoreDevice })
}

func TestTaskConnectionOnAndroid(t *testing.T) {
	g, _ := newTestGateway(t)
	s := g.OpenSession(agent)
	defer s.Close()
	res := s.Connect(context.Background(), ConnectRequest{Task: "Lunch", Connection: "helper"})
	mustStatus(t, res, StatusSettled)
	if !strings.Contains(res.Message, "connects only one way") {
		t.Fatalf("connect: %q", res.Message)
	}
	if res := s.Connect(context.Background(), ConnectRequest{Task: "Lunch", Connection: "usb"}); res.Error != ErrInvalidArgument {
		t.Fatalf("an unknown connection: %+v", res)
	}
}
