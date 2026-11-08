package netmux

import (
	"context"
	"encoding/base64"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver/android/mdns"
)

// The tag for a made-up HostID and identifier, worked out apart from this
// code (Python's hmac: HKDF-SHA512 with an empty salt and info, 32 bytes,
// then HMAC-SHA256, the first 8 bytes, base64).
const (
	testHostID     = "8E0A1B2C-0000-4000-8000-123456789ABC"
	testIdentifier = "pinky-test-identifier"
	testTag        = "2PqSPmQCI0g="
)

func TestAuthTag(t *testing.T) {
	tag := AuthTag(testHostID, testIdentifier)
	if got := base64.StdEncoding.EncodeToString(tag[:]); got != testTag {
		t.Errorf("tag = %s, want %s", got, testTag)
	}
}

func TestMatch(t *testing.T) {
	pairs := map[string]PairInfo{
		"IPHONE": {HostID: testHostID, WiFiMAC: "08:87:C7:2A:8A:DB"},
		"IPAD":   {HostID: "OTHER-HOST", WiFiMAC: "f0:2f:4b:e8:d3:4a"},
	}
	cases := []struct {
		name string
		svc  mdns.Service
		want string
	}{
		{"by TXT, private address in the name", mdns.Service{
			Instance: "fa:df:90:f6:dd:2a@fe80::f8df:90ff:fef6:dd2a-supportsRP-26",
			TXT:      []string{"identifier=" + testIdentifier, "authTag=AAAAAAAAAAA=", "authTag#1= " + testTag + " "},
		}, "IPHONE"},
		{"by the Wi-Fi address in the name (before iOS 26.4)", mdns.Service{
			Instance: "f0:2f:4b:e8:d3:4a@fe80::1-supportsRP-24",
		}, "IPAD"},
		{"addresses compare in any case", mdns.Service{Instance: "08:87:c7:2a:8a:db@fe80::2"}, "IPHONE"},
		{"a tag for another computer", mdns.Service{
			Instance: "aa:bb:cc:dd:ee:ff@fe80::3",
			TXT:      []string{"identifier=" + testIdentifier, "authTag=AAAAAAAAAAA="},
		}, ""},
		{"no TXT, unknown address", mdns.Service{Instance: "aa:bb:cc:dd:ee:ff@fe80::3"}, ""},
	}
	for _, c := range cases {
		if got := Match(c.svc, pairs); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

type fakeSource struct {
	mu   sync.Mutex
	svcs []mdns.Service
}

func (f *fakeSource) Services() []mdns.Service {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mdns.Service(nil), f.svcs...)
}
func (f *fakeSource) Err() error   { return nil }
func (f *fakeSource) Close() error { return nil }

func iphoneService(addr string) mdns.Service {
	return mdns.Service{
		Instance: "fa:df:90:f6:dd:2a@fe80::f8df:90ff:fef6:dd2a-supportsRP-26", Type: ServiceType,
		Port: 32498, Addrs: []netip.Addr{netip.MustParseAddr("fe80::55"), netip.MustParseAddr(addr)},
		TXT: []string{"identifier=" + testIdentifier, "authTag=" + testTag},
	}
}

func TestFinder(t *testing.T) {
	m := New("unix:///nonexistent", nil)
	src := &fakeSource{svcs: []mdns.Service{iphoneService("192.168.33.187")}}
	now := time.Unix(1_000_000, 0)
	answers := true
	reads := 0
	f := &Finder{
		Mux:      m,
		UDIDs:    func() []string { return []string{"IPHONE", "BROKEN"} },
		BrowseFn: func() (Source, error) { return src, nil },
		PairFn: func(udid string) (PairInfo, error) {
			reads++
			if udid == "BROKEN" {
				return PairInfo{}, errors.New("no record")
			}
			return PairInfo{HostID: testHostID}, nil
		},
		ProbeFn: func(ctx context.Context, addr netip.Addr) bool { return answers },
		NowFn:   func() time.Time { return now },
	}
	s := newFindState(f, f.logOrDiscard())
	ctx := context.Background()

	s.tick(ctx)
	if got := m.Phones(); len(got) != 1 || got[0].UDID != "IPHONE" || got[0].Addr.String() != "192.168.33.187" {
		t.Fatalf("phones = %v", got)
	}
	if reads != 2 {
		t.Errorf("pair records read %d times, want 2", reads)
	}
	// Pair records are read once; one that failed again after pairRetry.
	now = now.Add(3 * time.Second)
	s.tick(ctx)
	if reads != 2 {
		t.Errorf("pair records read again: %d", reads)
	}
	now = now.Add(pairRetry)
	s.tick(ctx)
	if reads != 3 {
		t.Errorf("the failed pair record was not read again: %d", reads)
	}

	listed := func() bool { return len(m.Phones()) == 1 }
	setSvcs := func(svcs ...mdns.Service) {
		src.mu.Lock()
		src.svcs = svcs
		src.mu.Unlock()
	}

	// Lockdown does not answer: still listed for dropAfter, then dropped.
	answers = false
	now = now.Add(3 * time.Second)
	s.tick(ctx)
	now = now.Add(dropAfter - 4*time.Second)
	s.tick(ctx)
	if !listed() {
		t.Error("dropped before dropAfter")
	}
	now = now.Add(2 * time.Second)
	s.tick(ctx)
	if listed() {
		t.Error("still listed after dropAfter")
	}
	// It answers again: listed again, at once.
	answers = true
	s.tick(ctx)
	if !listed() {
		t.Fatal("not listed again")
	}

	// The record lacks its TXT strings for a while, as on arch at 01:25
	// (the iPad then dropped, and Pinky's session on it with it): matched
	// by the name it had, it stays listed.
	noTXT := iphoneService("192.168.33.187")
	noTXT.TXT = nil
	setSvcs(noTXT)
	now = now.Add(3 * time.Second)
	s.tick(ctx)
	if !listed() {
		t.Error("dropped when its record lost its TXT strings")
	}
	// It has no address for a while, or goes away: lockdown is asked at
	// the address it had, and it stays listed while that answers.
	noAddr := iphoneService("192.168.33.187")
	noAddr.Addrs = nil
	setSvcs(noAddr)
	s.tick(ctx)
	setSvcs()
	now = now.Add(3 * time.Second)
	s.tick(ctx)
	if got := m.Phones(); len(got) != 1 || got[0].Addr.String() != "192.168.33.187" {
		t.Errorf("without its record: %v", got)
	}
	// Without a record and without answers, it is dropped after dropAfter
	// and not asked again.
	answers = false
	now = now.Add(dropAfter + time.Second)
	s.tick(ctx)
	if listed() {
		t.Error("listed without a record or answers")
	}
	answers = true
	now = now.Add(3 * time.Second)
	s.tick(ctx)
	if listed() {
		t.Error("listed again without a record")
	}

	// A record with only an IPv6 address, of a phone not listed, is not
	// listed.
	v6 := iphoneService("1.2.3.4")
	v6.Addrs = []netip.Addr{netip.MustParseAddr("fe80::1")}
	setSvcs(v6)
	s.tick(ctx)
	if listed() {
		t.Error("listed with only a link-local address")
	}
}

func TestFinderRunStops(t *testing.T) {
	m := New("unix:///nonexistent", nil)
	src := &fakeSource{svcs: []mdns.Service{iphoneService("192.168.33.187")}}
	f := &Finder{
		Mux:      m,
		UDIDs:    func() []string { return []string{"IPHONE"} },
		Interval: 10 * time.Millisecond,
		BrowseFn: func() (Source, error) { return src, nil },
		PairFn:   func(string) (PairInfo, error) { return PairInfo{HostID: testHostID}, nil },
		ProbeFn:  func(context.Context, netip.Addr) bool { return true },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for len(m.Phones()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(m.Phones()) != 1 {
		t.Fatal("not found")
	}
	cancel()
	<-done
	if len(m.Phones()) != 0 {
		t.Error("phones listed after the search stopped")
	}
}

func TestPairedUDIDs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"00008140-001E79483A00401C.plist", "SystemConfiguration.plist", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(PairedUDIDs(dir), ","); got != "00008140-001E79483A00401C" {
		t.Errorf("UDIDs = %q", got)
	}
	if got := PairedUDIDs(filepath.Join(dir, "missing")); got != nil {
		t.Errorf("UDIDs of a missing folder = %q", got)
	}
}
