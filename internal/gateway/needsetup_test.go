package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
)

// stepProvider lists one phone as the test sets it: waiting for a step,
// offline, online (opened as a demo phone), or not at all (unplugged).
type stepProvider struct {
	dev  *demo.Device
	mu   sync.Mutex
	info *driver.Info // nil: not listed
}

func (p *stepProvider) Platform() driver.Platform { return driver.Android }

func (p *stepProvider) List(context.Context) ([]driver.Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.info == nil {
		return nil, nil
	}
	return []driver.Info{*p.info}, nil
}

func (p *stepProvider) Open(context.Context, string) (driver.Device, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.info == nil || p.info.State != driver.StateOnline {
		return nil, driver.ErrOffline
	}
	return demo.Android{Device: p.dev}, nil
}

// list lists the phone as info from the next discovery on; nil unplugs it.
func (p *stepProvider) list(info *driver.Info) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if info == nil {
		p.info = nil
		return
	}
	cp := *info
	p.info = &cp
}

// viewUntil waits until the gateway's view of the phone passes ok.
func viewUntil(t *testing.T, g *Gateway, id, what string, ok func(DeviceView) bool) DeviceView {
	t.Helper()
	var last DeviceView
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, v := range g.Devices() {
			if v.Info.ID == id {
				if last = v; ok(v) {
					return v
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: the phone is %q %q, %+v", what, last.Status, last.StatusLine, last.Info)
	return last
}

// TestListedPhoneNeedsSetup: a phone its driver lists as waiting for a
// step (allow USB debugging, the computer's USB permission) is connected,
// not offline: it needs setup, and keeps the step and the driver's words
// for the client. Unplugged, it is offline and no longer claims the step,
// and the client hears of it; plugged in again it needs setup again. Once
// allowed the gateway opens it, and a phone that needs a step after it
// was open needs setup too.
func TestListedPhoneNeedsSetup(t *testing.T) {
	p := &stepProvider{dev: demo.NewDevice("px", "Pixel 8a", driver.Android)}
	allow := driver.Info{ID: "px", Platform: driver.Android, Name: "Pixel 8a", Model: "Pixel 8a", Connection: "usb", Backend: "scrcpy",
		State: driver.StateUnauthorized, Detail: "Unlock the phone and allow USB debugging for this computer."}
	p.list(&allow)
	g := checkupGateway(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := g.Subscribe(ctx)
	needsSetup := func(v DeviceView) bool { return v.Status == DeviceNeedsSetup }

	v := viewUntil(t, g, "px", "waiting to be allowed", needsSetup)
	if v.StatusLine != "Needs setup" || v.Info.State != driver.StateUnauthorized || v.Info.Detail != allow.Detail || v.Features != (Features{}) {
		t.Errorf("waiting to be allowed: %+v", v)
	}

	usb := allow
	usb.State, usb.Setup, usb.Detail = driver.StateNeedsSetup, driver.SetupUSBPermission, "This computer has no permission to use the phone over USB."
	p.list(&usb)
	v = viewUntil(t, g, "px", "another step", func(v DeviceView) bool { return v.Info.Setup == driver.SetupUSBPermission })
	if v.Status != DeviceNeedsSetup || v.StatusLine != "Needs setup" || v.Info.State != driver.StateNeedsSetup || v.Info.Detail != usb.Detail {
		t.Errorf("another step: %+v", v)
	}
	cu, err := g.Checkup(ctx, "px")
	if err != nil {
		t.Fatal(err)
	}
	if conn, _ := checkByID(cu, driver.CheckConnection); conn.Status != driver.CheckPass {
		t.Errorf("the connection of a phone that needs setup: %+v", conn)
	}
	if auth, _ := checkByID(cu, driver.CheckAuthorized); auth.Status != driver.CheckFail || auth.Fix != driver.SetupUSBPermission {
		t.Errorf("the step of a phone that needs setup: %+v", auth)
	}

	// Unplugged while it waits: offline, without the step or the words
	// that went with it, and an event says so.
	for len(events) > 0 {
		<-events
	}
	p.list(nil)
	v = viewUntil(t, g, "px", "unplugged", func(v DeviceView) bool { return v.Status == DeviceOffline })
	if v.StatusLine != "Offline" || v.Info.State != driver.StateOffline || v.Info.Setup != "" || v.Info.Detail != "" {
		t.Errorf("unplugged: %+v", v)
	}
	heard := false
	for deadline := time.After(2 * time.Second); !heard; {
		select {
		case e := <-events:
			heard = e.Kind == EventDevices && e.DeviceID == "px"
		case <-deadline:
			t.Fatal("no event for the phone that went away")
		}
	}
	cu, err = g.Checkup(ctx, "px")
	if err != nil {
		t.Fatal(err)
	}
	if conn, _ := checkByID(cu, driver.CheckConnection); conn.Status != driver.CheckFail || conn.Fix != driver.FixReconnect {
		t.Errorf("the connection of an unplugged phone: %+v", conn)
	}
	if auth, _ := checkByID(cu, driver.CheckAuthorized); auth.Status != driver.CheckSkip {
		t.Errorf("an unplugged phone still waits for a step: %+v", auth)
	}

	// Plugged in again, and listed offline with a reason before it went
	// away again: the reason goes with it.
	p.list(&allow)
	viewUntil(t, g, "px", "plugged in again", needsSetup)
	off := allow
	off.State, off.Detail = driver.StateOffline, "adb reports the phone as offline."
	p.list(&off)
	v = viewUntil(t, g, "px", "listed offline", func(v DeviceView) bool { return v.Status == DeviceOffline })
	if v.StatusLine != "Offline · "+off.Detail || v.Info.Detail != off.Detail {
		t.Errorf("listed offline: %+v", v)
	}
	p.list(nil)
	viewUntil(t, g, "px", "unplugged when offline", func(v DeviceView) bool { return v.StatusLine == "Offline" && v.Info.Detail == "" })

	// Allowed: online, and opened.
	online := allow
	online.State, online.Detail = driver.StateOnline, ""
	p.list(&online)
	waitOnline(t, g, "px")
	if v = viewUntil(t, g, "px", "online", func(v DeviceView) bool { return v.Status == DeviceIdle }); v.StatusLine != "Idle" {
		t.Errorf("online: %+v", v)
	}

	// It needs a step again: closed, and needs setup.
	again := allow
	again.State, again.Detail = driver.StateNeedsSetup, "Turn USB debugging on again."
	p.list(&again)
	v = viewUntil(t, g, "px", "a step after it was open", needsSetup)
	if v.StatusLine != "Needs setup" || v.Info.Detail != again.Detail || v.Features != (Features{}) {
		t.Errorf("a step after it was open: %+v", v)
	}
}
