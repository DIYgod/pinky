package ios

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/mdns"
)

// fakeBonjour is a search that finds what the test puts on the network.
type fakeBonjour struct {
	mu       sync.Mutex
	services []mdns.Service
	closed   bool
}

func (f *fakeBonjour) Services() []mdns.Service {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mdns.Service(nil), f.services...)
}

func (f *fakeBonjour) Err() error { return nil }

func (f *fakeBonjour) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeBonjour) set(instances ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.services = nil
	for _, in := range instances {
		f.services = append(f.services, mdns.Service{Instance: in, Type: mobdevService})
	}
}

// TestLostPhone: a phone the person uses that usbmuxd stopped listing but
// that shows up on the network for lostAfter is listed as lost, with its
// name; once usbmuxd lists it again it is not. Phones without a pair record
// (Android) are left out, and the search stops when nothing is missing.
func TestLostPhone(t *testing.T) {
	w := newWiFiProvider(entry("iphone", 7, "Network"))
	w.lost.anyOS = true
	net := &fakeBonjour{}
	browses := 0
	w.lost.browseFn = func() (bonjourSource, error) { browses++; return net, nil }
	w.lost.pairFn = func(udid string) (string, error) {
		if udid == "iphone" {
			return "08:87:C7:2A:8A:DB", nil
		}
		return "", errors.New("ReadPair failed with errorcode '22'")
	}
	w.SetWiFi(true)
	w.UseOverWiFi("iphone")
	w.UseOverWiFi("3e8be48e") // an Android phone the gateway knows
	if got := listed(t, w.Provider); got["iphone"].Name != "iphone" {
		t.Fatalf("listed by usbmuxd: %+v", got)
	}
	if browses != 0 {
		t.Fatal("searched while every phone was listed")
	}

	w.set() // usbmuxd lost it
	net.set("08:87:c7:2a:8a:db@fe80::a87:c7ff:fe2a:8adb-supportsRP-26", "c0:17:54:70:5d:7f@fe80::c217:54ff:fe70:5d7f-supportsRP-26")
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("lost at once: %+v", got)
	}
	if browses != 1 {
		t.Fatalf("%d searches, want 1", browses)
	}
	w.after(lostAfter)
	got := listed(t, w.Provider)
	lost := got["iphone"]
	if lost.Setup != driver.SetupDeviceService || lost.State != driver.StateNeedsSetup || lost.Name != "iphone" || lost.Connection != "wifi" || len(got) != 1 {
		t.Fatalf("after lostAfter on the network: %+v", got)
	}

	// Gone from the network too: not lost, just away.
	net.set()
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("off the network: %+v", got)
	}
	net.set("08:87:c7:2a:8a:db@fe80::a87:c7ff:fe2a:8adb-supportsRP-26")
	listed(t, w.Provider)
	w.after(lostAfter - time.Second)
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("back on the network for less than lostAfter: %+v", got)
	}

	// usbmuxd lists it again: the search ends lostIdle later.
	w.set(entry("iphone", 8, "Network"))
	listed(t, w.Provider)
	w.after(lostIdle)
	listed(t, w.Provider)
	if !net.closed {
		t.Fatal("the search went on with nothing missing")
	}
}

// TestRestartDeviceService runs usbmuxd's restart through the system's
// request for an administrator, and reads a declined one as canceled.
func TestRestartDeviceService(t *testing.T) {
	p := NewProvider()
	var got []string
	p.restartFn = func(_ context.Context, command, reason string) error {
		got = append(got, command, reason)
		return nil
	}
	if err := p.RestartDeviceService(context.Background(), "Pinky restarts the device service."); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "/usr/bin/killall usbmuxd" || got[1] != "Pinky restarts the device service." {
		t.Fatalf("ran %q", got)
	}
	if s := appleScriptString(`say "hi" \ bye`); s != `"say \"hi\" \\ bye"` {
		t.Fatalf("appleScriptString = %s", s)
	}
	p.restartFn = func(context.Context, string, string) error {
		return errors.New("restarting the device service: " + driver.ErrCanceled.Error())
	}
	if err := p.RestartDeviceService(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("declined: %v", err)
	}
}
