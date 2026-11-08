package ios

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	goios "github.com/danielpaulus/go-ios/ios"
)

// wifiProvider is a provider over a usbmuxd list the test changes, that
// counts how often it inspects (prepares) each phone and reads a found
// phone's name.
type wifiProvider struct {
	*Provider
	mu       sync.Mutex
	list     []goios.DeviceEntry
	inspects map[string]int
	reads    map[string]int
	readErr  error
	now      time.Time
}

func newWiFiProvider(list ...goios.DeviceEntry) *wifiProvider {
	w := &wifiProvider{Provider: NewProvider(), list: list, inspects: map[string]int{}, reads: map[string]int{}, now: time.Unix(1000, 0)}
	w.Provider.now = func() time.Time { w.mu.Lock(); defer w.mu.Unlock(); return w.now }
	w.listFn = func() (goios.DeviceList, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		return goios.DeviceList{DeviceList: append([]goios.DeviceEntry(nil), w.list...)}, nil
	}
	w.inspectFn = func(_ context.Context, e goios.DeviceEntry) driver.Info {
		w.mu.Lock()
		defer w.mu.Unlock()
		id := e.Properties.SerialNumber
		w.inspects[id]++
		conn := "usb"
		if e.Properties.ConnectionType == "Network" {
			conn = "wifi"
		}
		return driver.Info{ID: id, Platform: driver.IOS, Name: id, Connection: conn, State: driver.StateOnline, Backend: backendCoreDevice}
	}
	w.wifi.namesFn = func(_ context.Context, e goios.DeviceEntry) (phoneNames, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.reads[e.Properties.SerialNumber]++
		if w.readErr != nil {
			return phoneNames{}, w.readErr
		}
		return phoneNames{name: "Mum's iPad", productType: "iPad13,4", version: "27.0.1"}, nil
	}
	return w
}

func (w *wifiProvider) set(list ...goios.DeviceEntry) {
	w.mu.Lock()
	w.list = list
	w.mu.Unlock()
}

func (w *wifiProvider) after(d time.Duration) {
	w.mu.Lock()
	w.now = w.now.Add(d)
	w.mu.Unlock()
}

func (w *wifiProvider) counts(id string) (inspects, reads int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inspects[id], w.reads[id]
}

func listed(t *testing.T, p *Provider) map[string]driver.Info {
	t.Helper()
	infos, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]driver.Info{}
	for _, info := range infos {
		out[info.ID] = info
	}
	return out
}

// TestWiFiFoundPhoneLeftAlone: a phone usbmuxd lists only over Wi-Fi that
// is not the person's is listed by name, with SetupConfirmWiFi, and is
// neither inspected (which prepares it) nor opened, until UseOverWiFi.
func TestWiFiFoundPhoneLeftAlone(t *testing.T) {
	w := newWiFiProvider(entry("ipad", 7, "Network"), entry("phone", 3, "USB"))
	if got := listed(t, w.Provider); len(got) != 1 || got["phone"].State != driver.StateOnline {
		t.Fatalf("Wi-Fi off: %+v; want the phone on the cable only", got)
	}
	w.SetWiFi(true)
	got := listed(t, w.Provider)
	ipad := got["ipad"]
	if ipad.State != driver.StateNeedsSetup || ipad.Setup != driver.SetupConfirmWiFi || ipad.Name != "Mum's iPad" ||
		ipad.Model != "iPad Pro 11-inch (3rd generation)" || ipad.Connection != "wifi" || ipad.OSVersion != "27.0.1" {
		t.Fatalf("found iPad = %+v", ipad)
	}
	if n, _ := w.counts("ipad"); n != 0 {
		t.Fatalf("the found iPad was inspected %d times; it must be left alone", n)
	}
	if _, err := w.Open(context.Background(), "ipad"); err == nil || !strings.Contains(err.Error(), "found on Wi-Fi") {
		t.Fatalf("Open of the found iPad = %v; want a refusal", err)
	}
	if n, _ := w.counts("ipad"); n != 0 {
		t.Fatal("Open inspected the found iPad")
	}
	if c := w.Connections(context.Background(), "ipad"); c.Ways[1].Available || c.Ways[1].Reason != "offline" {
		t.Fatalf("Connections of the found iPad = %+v; want nothing asked of it", c)
	}

	// Listed again, and again as a new attach (an iPad that sleeps comes
	// back with a new DeviceID): its name is not read again.
	listed(t, w.Provider)
	w.set(entry("ipad", 8, "Network"), entry("phone", 3, "USB"))
	w.after(time.Minute)
	listed(t, w.Provider)
	if _, reads := w.counts("ipad"); reads != 1 {
		t.Fatalf("the name was read %d times; want once", reads)
	}

	w.UseOverWiFi("ipad")
	got = listed(t, w.Provider)
	if got["ipad"].State != driver.StateOnline || got["ipad"].Connection != "wifi" {
		t.Fatalf("after Use: %+v", got["ipad"])
	}
	if n, _ := w.counts("ipad"); n != 1 {
		t.Fatalf("after Use the iPad was inspected %d times, want 1", n)
	}

	// Turned off, phones over Wi-Fi go; the cable stays.
	w.SetWiFi(false)
	if got := listed(t, w.Provider); len(got) != 1 || got["phone"].ID == "" {
		t.Fatalf("Wi-Fi off again: %+v", got)
	}
}

// TestWiFiFoundPhoneSilent: a found phone that does not answer is not
// listed, and is asked again after recheckOffline.
func TestWiFiFoundPhoneSilent(t *testing.T) {
	w := newWiFiProvider(entry("ipad", 7, "Network"))
	w.SetWiFi(true)
	w.readErr = errors.New("no answer within 8s")
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("a silent found phone was listed: %+v", got)
	}
	listed(t, w.Provider)
	if _, reads := w.counts("ipad"); reads != 1 {
		t.Fatalf("%d reads within recheckOffline, want 1", reads)
	}
	w.readErr = nil
	w.after(recheckOffline)
	if got := listed(t, w.Provider); got["ipad"].Setup != driver.SetupConfirmWiFi {
		t.Fatalf("after it answered: %+v", got)
	}
}

// TestWiFiForgottenPhone: a phone the person removed or ignored is not
// listed over Wi-Fi at all, and not even read; on a cable it is, and is the
// person's once the gateway says so.
func TestWiFiForgottenPhone(t *testing.T) {
	w := newWiFiProvider(entry("ipad", 7, "Network"))
	w.SetWiFi(true)
	w.UseOverWiFi("ipad")
	w.Forget("ipad")
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("a forgotten phone was listed: %+v", got)
	}
	if n, reads := w.counts("ipad"); n != 0 || reads != 0 {
		t.Fatalf("a forgotten phone was touched: %d inspects, %d reads", n, reads)
	}
	if _, err := w.Open(context.Background(), "ipad"); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("Open of a forgotten phone = %v", err)
	}
	w.set(entry("ipad", 9, "USB"), entry("ipad", 7, "Network"))
	if got := listed(t, w.Provider); got["ipad"].State != driver.StateOnline || got["ipad"].Connection != "usb" {
		t.Fatalf("on a cable: %+v", got)
	}
	w.Remember("ipad")
	w.UseOverWiFi("ipad")
	w.set(entry("ipad", 7, "Network"))
	if got := listed(t, w.Provider); got["ipad"].State != driver.StateOnline || got["ipad"].Connection != "wifi" {
		t.Fatalf("unplugged after it came back: %+v", got)
	}
}

// TestWiFiCableMoves: a phone the person uses moves from the cable to
// Wi-Fi and back as the same phone, inspected again for each connection.
func TestWiFiCableMoves(t *testing.T) {
	w := newWiFiProvider(entry("phone", 3, "USB"), entry("phone", 7, "Network"))
	w.SetWiFi(true)
	w.UseOverWiFi("phone")
	if got := listed(t, w.Provider)["phone"]; got.Connection != "usb" {
		t.Fatalf("on both: %+v; want the cable", got)
	}
	w.set(entry("phone", 7, "Network"))
	if got := listed(t, w.Provider)["phone"]; got.Connection != "wifi" || got.State != driver.StateOnline {
		t.Fatalf("unplugged: %+v", got)
	}
	w.set(entry("phone", 11, "USB"), entry("phone", 7, "Network"))
	if got := listed(t, w.Provider)["phone"]; got.Connection != "usb" {
		t.Fatalf("plugged in again: %+v", got)
	}
	if n, _ := w.counts("phone"); n != 3 {
		t.Fatalf("%d inspections, want one per connection", n)
	}
}

func TestBounded(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	start := time.Now()
	_, err := bounded(context.Background(), 20*time.Millisecond, func() (int, error) { <-block; return 1, nil })
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("bounded = %v after %v", err, time.Since(start))
	}
	v, err := bounded(context.Background(), time.Second, func() (int, error) { return 7, nil })
	if v != 7 || err != nil {
		t.Fatalf("bounded = %d, %v", v, err)
	}
}

// TestWiFiFoundPhoneHeld: a found phone that drops off usbmuxd's list, as
// an iPad that sleeps does every minute or so, stays listed for foundHold.
func TestWiFiFoundPhoneHeld(t *testing.T) {
	w := newWiFiProvider(entry("ipad", 7, "Network"))
	w.SetWiFi(true)
	listed(t, w.Provider)
	w.set()
	w.after(foundHold - time.Second)
	if got := listed(t, w.Provider); got["ipad"].Setup != driver.SetupConfirmWiFi {
		t.Fatalf("asleep for a moment: %+v", got)
	}
	w.set(entry("ipad", 8, "Network")) // back, as a new attach
	listed(t, w.Provider)
	w.set()
	w.after(foundHold - time.Second)
	if got := listed(t, w.Provider); len(got) != 1 {
		t.Fatalf("the hold did not start again when it came back: %+v", got)
	}
	w.after(2 * time.Second)
	if got := listed(t, w.Provider); len(got) != 0 {
		t.Fatalf("gone longer than foundHold: %+v", got)
	}
	if _, reads := w.counts("ipad"); reads != 1 {
		t.Fatalf("%d reads, want 1", reads)
	}
}
