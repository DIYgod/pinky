package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
)

// checkerDev is a demo phone that runs a checkup of its own.
type checkerDev struct {
	*demo.Device
	cu driver.Checkup
}

func (c *checkerDev) Checkup(context.Context) (driver.Checkup, error) { return c.cu, nil }

type checkerProvider struct {
	*demo.Provider
	x driver.Device
}

func (p checkerProvider) Open(context.Context, string) (driver.Device, error) { return p.x, nil }

// listedProvider lists one phone in a fixed state and opens nothing.
type listedProvider struct{ info driver.Info }

func (p listedProvider) Platform() driver.Platform { return p.info.Platform }
func (p listedProvider) List(context.Context) ([]driver.Info, error) {
	return []driver.Info{p.info}, nil
}
func (p listedProvider) Open(context.Context, string) (driver.Device, error) {
	return nil, driver.ErrOffline
}

func checkupGateway(t *testing.T, p driver.Provider) *Gateway {
	t.Helper()
	g, err := New(Options{Providers: []driver.Provider{p}, TrustNewAgents: true, Policy: testPolicy(t),
		DiscoverEvery: 20 * time.Millisecond, NoScreenshots: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}

func checkByID(v CheckupView, id string) (driver.Check, bool) {
	for _, c := range v.Checks {
		if c.ID == id {
			return c, true
		}
	}
	return driver.Check{}, false
}

func TestCheckupOnlinePhone(t *testing.T) {
	dev := demo.NewDevice("mi", "Mi 10 Pro", driver.Android)
	x := &checkerDev{Device: dev, cu: driver.Checkup{Family: "xiaomi", System: "HyperOS 2.0", Checks: []driver.Check{
		{ID: driver.CheckInput, Status: driver.CheckFail, Fix: driver.FixInputPermission, Detail: "INJECT_EVENTS"},
	}}}
	g := checkupGateway(t, checkerProvider{demo.NewProvider(driver.Android, dev), x})
	waitOnline(t, g, "mi")
	v, err := g.Checkup(context.Background(), "mi")
	if err != nil {
		t.Fatal(err)
	}
	if v.Family != "xiaomi" || v.System != "HyperOS 2.0" || v.Platform != driver.Android || v.At.IsZero() {
		t.Errorf("view = %+v", v)
	}
	if len(v.Checks) != 3 || v.Checks[0].ID != driver.CheckConnection || v.Checks[1].ID != driver.CheckAuthorized {
		t.Fatalf("checks = %+v", v.Checks)
	}
	if c := v.Checks[0]; c.Status != driver.CheckPass || c.Detail != "usb" {
		t.Errorf("connection = %+v", c)
	}
	if c, _ := checkByID(v, driver.CheckInput); c.Status != driver.CheckFail || c.Fix != driver.FixInputPermission {
		t.Errorf("input = %+v", c)
	}
	if _, err := g.Checkup(context.Background(), "nope"); !errors.Is(err, ErrUnknownDevice) {
		t.Errorf("unknown phone: %v", err)
	}
}

func TestCheckupWithoutChecker(t *testing.T) {
	// The demo's iPhones run no checkup of their own.
	dev := demo.NewDevice("iphone", "iPhone 17", driver.IOS)
	g := checkupGateway(t, demo.NewProvider(driver.IOS, dev))
	waitOnline(t, g, "iphone")
	v, err := g.Checkup(context.Background(), "iphone")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Checks) != 2 || v.Family != "" {
		t.Errorf("a driver without checks of its own: %+v", v)
	}
}

func TestCheckupPhoneNotOnline(t *testing.T) {
	for _, c := range []struct {
		state        driver.State
		conn, auth   driver.CheckStatus
		connFix, fix string
	}{
		{driver.StateUnauthorized, driver.CheckPass, driver.CheckFail, "", driver.FixAuthorize},
		{driver.StateNeedsSetup, driver.CheckPass, driver.CheckFail, "", ""},
		{driver.StateOffline, driver.CheckFail, driver.CheckSkip, driver.FixReconnect, ""},
	} {
		info := driver.Info{ID: "x", Platform: driver.Android, Name: "Phone", Connection: "usb", State: c.state, Detail: "what the driver says"}
		g := checkupGateway(t, listedProvider{info})
		deadline := time.Now().Add(2 * time.Second)
		for len(g.Devices()) == 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		v, err := g.Checkup(context.Background(), "x")
		if err != nil {
			t.Fatalf("%s: %v", c.state, err)
		}
		if len(v.Checks) != 2 {
			t.Fatalf("%s: checks = %+v", c.state, v.Checks)
		}
		conn, auth := v.Checks[0], v.Checks[1]
		if conn.Status != c.conn || conn.Fix != c.connFix || auth.Status != c.auth || auth.Fix != c.fix {
			t.Errorf("%s: connection %+v, authorized %+v", c.state, conn, auth)
		}
		if c.state != driver.StateOffline && auth.Detail != "what the driver says" {
			t.Errorf("%s: the driver's words are lost: %+v", c.state, auth)
		}
	}
}

// waitOnline waits until the gateway has the phone open.
func waitOnline(t *testing.T, g *Gateway, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, d := range g.Devices() {
			if d.Info.ID == id && d.Info.State == driver.StateOnline && d.Status != DeviceOffline {
				if dd, err := g.device(id); err == nil && g.driverOf(dd) != nil {
					return
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s never came online", id)
}
