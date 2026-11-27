//go:build !windows

// Pinky's own usbmuxd on macOS, for an iPhone whose own Personal Hotspot
// the Mac is on (netmux.go, #235). Its socket is in /tmp, which Windows
// does not have.

package ios

import (
	"context"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/mdns"
	"github.com/DIYgod/pinky/internal/driver/ios/netmux"
	goios "github.com/danielpaulus/go-ios/ios"
)

// fakeHotspot is the network as a test has it: the phone's records, this
// Mac's addresses, and whether lockdown answers at the phone's address on
// its hotspot. It is the search of both lost.go and Pinky's own usbmuxd.
type fakeHotspot struct {
	mu      sync.Mutex
	local   []netip.Addr // this Mac's addresses
	records []netip.Addr // the phone's, as its records name them
	answers bool         // lockdown answers at hotspotPhone
}

const hotspotInstance = "08:87:c7:2a:8a:db@fe80::a87:c7ff:fe2a:8adb-supportsRP-26"

func (n *fakeHotspot) Services() []mdns.Service {
	n.mu.Lock()
	defer n.mu.Unlock()
	return []mdns.Service{{Instance: hotspotInstance, Type: mobdevService, Addrs: slices.Clone(n.records)}}
}

func (n *fakeHotspot) Err() error   { return nil }
func (n *fakeHotspot) Close() error { return nil }

func (n *fakeHotspot) addrs() []netip.Addr {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.local)
}

// at puts this Mac at addrs.
func (n *fakeHotspot) at(addrs ...string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.local = parseAddrs(addrs)
}

// advertise has the phone's records name addrs.
func (n *fakeHotspot) advertise(addrs ...string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.records = parseAddrs(addrs)
}

// answer has lockdown answer at hotspotPhone, or not.
func (n *fakeHotspot) answer(ok bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.answers = ok
}

func parseAddrs(list []string) []netip.Addr {
	var out []netip.Addr
	for _, a := range list {
		out = append(out, netip.MustParseAddr(a))
	}
	return out
}

// hotspotTest is a provider as on macOS that uses the phone "IPHONE" over
// Wi-Fi, with this Mac on the phone's own Personal Hotspot: Apple's
// usbmuxd lists nothing, as in #235, and Pinky's own usbmuxd goes in /tmp,
// in front of none. Nothing in it reaches a phone or this computer's
// usbmuxd.
type hotspotTest struct {
	*wifiProvider
	net *fakeHotspot
}

func newHotspotTest(t *testing.T) *hotspotTest {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pinkymux") // short: socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("USBMUXD_SOCKET_ADDRESS", "")
	t.Setenv(ownMuxVar, "") // both restored after the test: the provider sets them
	h := &hotspotTest{wifiProvider: newWiFiProvider(), net: &fakeHotspot{answers: true}}
	h.net.at("127.0.0.1", "fe80::1c2b:3a4d:5e6f:7081", "172.20.10.3")
	h.net.advertise("172.20.10.1", "fe80::a87:c7ff:fe2a:8adb")
	p := h.Provider
	p.WiFiMux = true
	p.nmux.macOS = true
	p.nmux.dir = dir
	p.nmux.upstream = "unix://" + dir + "/none" // not this computer's usbmuxd
	p.nmux.pairs = t.TempDir()
	pair := func(string) (netmux.PairInfo, error) { return netmux.PairInfo{WiFiMAC: "08:87:c7:2a:8a:db"}, nil }
	p.nmux.browseFn = func() (netmux.Source, error) { return h.net, nil }
	p.nmux.pairFn = pair
	p.nmux.probeFn = func(_ context.Context, a netip.Addr) bool {
		h.net.mu.Lock()
		defer h.net.mu.Unlock()
		return a == hotspotPhone && h.net.answers
	}
	p.nmux.localFn = h.net.addrs
	p.lost.anyOS = true
	p.lost.browseFn = func() (bonjourSource, error) { return h.net, nil }
	p.lost.pairFn = pair
	p.lost.localFn = h.net.addrs
	// The phone's developer services are there whenever List looks again.
	p.setup = func(context.Context, goios.DeviceEntry) (bool, error) { return true, nil }
	// usbmuxd's list as go-ios reads it: Apple's lists nothing, and Pinky's
	// own, once it runs, what its search found.
	p.listFn = func() (goios.DeviceList, error) {
		if mux, _ := h.running(); mux == nil {
			return goios.DeviceList{}, nil
		}
		list, err := goios.ListDevices()
		if err != nil {
			return goios.DeviceList{}, nil // no phone found, and no usbmuxd behind it here
		}
		return list, nil
	}
	p.SetWiFi(true)
	p.UseOverWiFi("IPHONE")
	if mux, _ := h.running(); mux != nil {
		t.Fatal("turning Wi-Fi on started a usbmuxd on macOS")
	}
	return h
}

// running returns Pinky's own usbmuxd, nil before it runs, and whether its
// search runs.
func (h *hotspotTest) running() (*netmux.Mux, bool) {
	h.nmux.mu.Lock()
	defer h.nmux.mu.Unlock()
	return h.nmux.mux, h.nmux.stop != nil
}

// phones says where Pinky's own usbmuxd lists phones on the network.
func (h *hotspotTest) phones() string {
	mux, _ := h.running()
	if mux == nil {
		return "no usbmuxd"
	}
	var out []string
	for _, ph := range mux.Phones() {
		out = append(out, ph.UDID+"@"+ph.Addr.String())
	}
	return strings.Join(out, " ")
}

// waitPhones waits until Pinky's own usbmuxd lists want, as its search runs
// by itself.
func (h *hotspotTest) waitPhones(t *testing.T, want string) {
	t.Helper()
	got := h.phones()
	for deadline := time.Now().Add(2 * time.Second); got != want && time.Now().Before(deadline); got = h.phones() {
		time.Sleep(5 * time.Millisecond)
	}
	if got != want {
		t.Fatalf("Pinky's usbmuxd lists %q, want %q", got, want)
	}
}

// TestHotspotMuxOnlyOnTheHotspot: on macOS Pinky runs no usbmuxd of its own
// unless a phone usbmuxd does not list is on its own Personal Hotspot with
// this Mac. With this Mac on another network, also while the phone's
// records still name its address on its hotspot, or with both of them
// clients of a hotspot, the phone is lost as before, and
// USBMUXD_SOCKET_ADDRESS stays unset.
func TestHotspotMuxOnlyOnTheHotspot(t *testing.T) {
	for _, c := range []struct {
		name           string
		local, records []string
	}{
		{"another network", []string{"127.0.0.1", "192.168.1.20"}, []string{"192.168.1.30", "fe80::a87:c7ff:fe2a:8adb"}},
		{"this Mac off the hotspot", []string{"127.0.0.1", "192.168.1.20"}, []string{"172.20.10.1", "fe80::a87:c7ff:fe2a:8adb"}},
		{"both clients of a hotspot", []string{"127.0.0.1", "172.20.10.3"}, []string{"172.20.10.5", "fe80::a87:c7ff:fe2a:8adb"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHotspotTest(t)
			h.net.at(c.local...)
			h.net.advertise(c.records...)
			listed(t, h.Provider)
			h.after(lostAfter)
			if got := listed(t, h.Provider)["IPHONE"]; got.Setup != driver.SetupDeviceService || got.Detail != lostDetail {
				t.Errorf("lostAfter later: %+v; want lost", got)
			}
			if mux, _ := h.running(); mux != nil || os.Getenv("USBMUXD_SOCKET_ADDRESS") != "" || os.Getenv(ownMuxVar) != "" {
				t.Errorf("started a usbmuxd of its own: USBMUXD_SOCKET_ADDRESS = %q", os.Getenv("USBMUXD_SOCKET_ADDRESS"))
			}
		})
	}
}

// TestHotspotMux: a phone usbmuxd does not list, on its own Personal
// Hotspot with this Mac (#235), is looked for there by Pinky's own usbmuxd
// from the first List on, and is online through it, at 172.20.10.1, also
// lostAfter later: not lost. Once this Mac has been off the hotspot for
// hotspotLeave, the search stops and the phone leaves the list, while the
// usbmuxd stays; back on it, the same usbmuxd finds the phone again.
// Turning Wi-Fi off stops the search.
func TestHotspotMux(t *testing.T) {
	h := newHotspotTest(t)
	if got := listed(t, h.Provider); len(got) != 0 {
		t.Fatalf("at once: %+v", got)
	}
	mux, searching := h.running()
	addr := os.Getenv("USBMUXD_SOCKET_ADDRESS")
	if mux == nil || !searching || addr != "unix://"+h.nmux.dir+"/usbmuxd" || os.Getenv(ownMuxVar) != addr {
		t.Fatalf("no usbmuxd of its own, or no search: USBMUXD_SOCKET_ADDRESS = %q, %s = %q", addr, ownMuxVar, os.Getenv(ownMuxVar))
	}
	h.waitPhones(t, "IPHONE@172.20.10.1")
	if got := listed(t, h.Provider)["IPHONE"]; got.State != driver.StateOnline || got.Connection != "wifi" {
		t.Fatalf("through Pinky's usbmuxd: %+v; want online over Wi-Fi", got)
	}
	h.after(lostAfter)
	if got := listed(t, h.Provider)["IPHONE"]; got.State != driver.StateOnline {
		t.Fatalf("lostAfter later: %+v; want online", got)
	}

	// This Mac leaves the hotspot: the search goes on for hotspotLeave, as
	// the Mac's Wi-Fi may drop for a moment, and then stops.
	h.net.at("127.0.0.1", "192.168.1.20")
	listed(t, h.Provider)
	h.after(hotspotLeave - time.Second)
	listed(t, h.Provider)
	h.waitPhones(t, "IPHONE@172.20.10.1")
	h.after(time.Second)
	listed(t, h.Provider)
	h.waitPhones(t, "")
	listed(t, h.Provider)
	if now, searching := h.running(); now != mux || searching {
		t.Errorf("off the hotspot: the same usbmuxd %v, searching %v; want the same, not searching", now == mux, searching)
	}
	if got := os.Getenv("USBMUXD_SOCKET_ADDRESS"); got != addr {
		t.Errorf("off the hotspot: USBMUXD_SOCKET_ADDRESS = %q, want %q", got, addr)
	}

	// Back on it: the same usbmuxd finds the phone again, and it is online
	// once it has been back for awakeAfter, as a phone that dozes (wifi.go).
	h.net.at("127.0.0.1", "172.20.10.3")
	listed(t, h.Provider)
	h.waitPhones(t, "IPHONE@172.20.10.1")
	if now, _ := h.running(); now != mux {
		t.Error("a second usbmuxd started")
	}
	listed(t, h.Provider)
	h.after(awakeAfter)
	if got := listed(t, h.Provider)["IPHONE"]; got.State != driver.StateOnline {
		t.Fatalf("back on the hotspot: %+v; want online", got)
	}

	h.SetWiFi(false)
	h.waitPhones(t, "")
	if _, searching := h.running(); searching {
		t.Error("still searching with Wi-Fi off")
	}
}

// TestHotspotMuxNoAnswer: where lockdown does not answer at 172.20.10.1
// either, the phone is listed as on its own hotspot after the same wait as
// a lost one, counted from when Pinky's own usbmuxd started looking.
func TestHotspotMuxNoAnswer(t *testing.T) {
	h := newHotspotTest(t)
	h.net.answer(false)
	listed(t, h.Provider)
	if mux, _ := h.running(); mux == nil {
		t.Fatal("no usbmuxd of its own")
	}
	h.after(lostAfter - time.Second)
	if got := listed(t, h.Provider); len(got) != 0 {
		t.Fatalf("before lostAfter: %+v", got)
	}
	h.after(time.Second)
	got := listed(t, h.Provider)["IPHONE"]
	if got.Setup != driver.SetupOwnHotspot || got.State != driver.StateNeedsSetup || got.Detail != ownHotspotDetail || got.Connection != "wifi" {
		t.Fatalf("not answering on its hotspot either: %+v", got)
	}
	if got := h.phones(); got != "" {
		t.Errorf("Pinky's usbmuxd lists %q", got)
	}
}

// TestHotspotMuxLeavesAGivenUsbmuxd: with USBMUXD_SOCKET_ADDRESS set by the
// person, Pinky starts no usbmuxd of its own for a phone on its hotspot
// either, and says after the wait that the phone is out of reach there.
func TestHotspotMuxLeavesAGivenUsbmuxd(t *testing.T) {
	const given = "unix:///tmp/pinky-test-given.sock"
	h := newHotspotTest(t)
	t.Setenv("USBMUXD_SOCKET_ADDRESS", given)
	listed(t, h.Provider)
	h.after(lostAfter)
	if got := listed(t, h.Provider)["IPHONE"]; got.Setup != driver.SetupOwnHotspot {
		t.Errorf("lostAfter later: %+v; want on its own hotspot", got)
	}
	if mux, _ := h.running(); mux != nil || os.Getenv("USBMUXD_SOCKET_ADDRESS") != given {
		t.Errorf("started its own usbmuxd in place of the one given: %q", os.Getenv("USBMUXD_SOCKET_ADDRESS"))
	}
}

// TestMuxBase: the usbmuxd's folder goes in TMPDIR on macOS, in
// XDG_RUNTIME_DIR elsewhere when that is set, and in /tmp when the socket's
// path could be too long for the system there.
func TestMuxBase(t *testing.T) {
	const macTemp = "/var/folders/7x/3w1t2k8n5z9b_x4y6c8d0f2g0000gn/T/"
	fits := "/" + strings.Repeat("a", 69) // the socket's path is 103 bytes at most there
	for _, c := range []struct {
		goos, runtimeDir, tempDir, want string
	}{
		{"darwin", "", macTemp, macTemp},
		{"darwin", "/run/user/501", macTemp, macTemp},
		{"darwin", "", fits, fits},
		{"darwin", "", fits + "a", "/tmp"},
		{"linux", "/run/user/1000", "/tmp", "/run/user/1000"},
		{"linux", "", "/var/tmp", "/var/tmp"},
		{"linux", "", fits + "a", fits + "a"},
		{"linux", "/run/user/" + strings.Repeat("1", 80), "/var/tmp", "/tmp"},
	} {
		if got := muxBase(c.goos, c.runtimeDir, c.tempDir); got != c.want {
			t.Errorf("%s, XDG_RUNTIME_DIR %q, TMPDIR %q: %q, want %q", c.goos, c.runtimeDir, c.tempDir, got, c.want)
		}
	}
}
