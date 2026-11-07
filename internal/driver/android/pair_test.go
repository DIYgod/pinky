package android

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
)

// fakeADB is an adb server with a phone that pairs over Wi-Fi.
type fakeADB struct {
	t  *testing.T
	ln net.Listener

	mu         sync.Mutex
	services   []string // mDNS services, "instance\ttype\taddr"
	code       string   // the code or password the phone accepts
	guid       string   // reported by pair; "" for an old server
	devices    []string // host:devices-l lines
	connects   []string
	pairs      int
	autoSerial string // a serial the server adds by itself after pairing
}

func newFakeADB(t *testing.T) (*fakeADB, *Provider) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeADB{t: t, ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(c)
		}
	}()
	return f, NewProvider(Config{Client: &adb.Client{Addr: ln.Addr().String()}})
}

func (f *fakeADB) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return
	}
	size, _ := strconv.ParseUint(string(n[:]), 16, 32)
	buf := make([]byte, size)
	io.ReadFull(r, buf)
	req := string(buf)
	reply := func(s string) { fmt.Fprintf(c, "OKAY%04x%s", len(s), s) }
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case req == "host:mdns:check":
		reply("mdns daemon version [adb discovery 0.0.0]")
	case req == "host:mdns:services":
		reply(strings.Join(f.services, "\n") + "\n")
	case strings.HasPrefix(req, "host:pair:"):
		code, addr, _ := strings.Cut(strings.TrimPrefix(req, "host:pair:"), ":")
		f.pairs++
		// As the real server: OKAY, then a second status once it is over.
		if code != f.code {
			msg := "Failed: Wrong password or connection was dropped."
			fmt.Fprintf(c, "OKAYFAIL%04x%s", len(msg), msg)
			return
		}
		io.WriteString(c, "OKAY")
		if f.autoSerial != "" {
			f.devices = append(f.devices, f.autoSerial+"\tdevice product:x model:Pixel_9 device:x transport_id:7")
		}
		if f.guid == "" {
			reply("Successfully paired to " + addr)
			return
		}
		reply("Successfully paired to " + addr + " [guid=" + f.guid + "]")
	case strings.HasPrefix(req, "host:connect:"):
		addr := strings.TrimPrefix(req, "host:connect:")
		f.connects = append(f.connects, addr)
		f.devices = append(f.devices, addr+"\tdevice product:x model:Pixel_9 device:x transport_id:8")
		reply("connected to " + addr)
	case req == "host:devices-l":
		reply(strings.Join(f.devices, "\n") + "\n")
	default:
		msg := "unsupported"
		fmt.Fprintf(c, "FAIL%04x%s", len(msg), msg)
	}
}

func TestQRPairing(t *testing.T) {
	f, p := newFakeADB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pg, err := p.StartPairing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	qr := pg.QR()
	name, pass, ok := parseQR(qr)
	if !ok || !strings.HasPrefix(name, "pinky-") || len(pass) < 12 {
		t.Fatalf("QR() = %q", qr)
	}
	f.mu.Lock()
	f.code = pass
	f.guid = "adb-R5CN20ABC-x1y2z3"
	f.mu.Unlock()
	// The phone shows up a moment after the person scans the code.
	go func() {
		time.Sleep(700 * time.Millisecond)
		f.mu.Lock()
		f.services = []string{
			"pinky-other\t_adb-tls-pairing._tcp.\t192.168.1.9:30000",
			name + "\t_adb-tls-pairing._tcp.\t192.168.1.42:37123",
			"adb-R5CN20ABC-x1y2z3\t_adb-tls-connect._tcp.\t192.168.1.42:41000",
		}
		f.mu.Unlock()
	}()
	info, err := pg.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "192.168.1.42:41000" || info.Connection != "wifi" || info.State != driver.StateOnline {
		t.Errorf("Wait = %+v", info)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.connects) != 1 || f.connects[0] != "192.168.1.42:41000" {
		t.Errorf("connects = %q", f.connects)
	}
}

func parseQR(qr string) (name, pass string, ok bool) {
	rest, ok := strings.CutPrefix(qr, "WIFI:T:ADB;")
	if !ok || !strings.HasSuffix(rest, ";;") {
		return "", "", false
	}
	for _, field := range strings.Split(strings.TrimSuffix(rest, ";;"), ";") {
		k, v, _ := strings.Cut(field, ":")
		switch k {
		case "S":
			name = v
		case "P":
			pass = v
		}
	}
	return name, pass, name != "" && pass != ""
}

func TestQRPairingClose(t *testing.T) {
	_, p := newFakeADB(t)
	pg, err := p.StartPairing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		pg.Close()
	}()
	if _, err := pg.Wait(context.Background()); err == nil {
		t.Error("Wait after Close returned no error")
	}
}

func TestPairWithCode(t *testing.T) {
	f, p := newFakeADB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.mu.Lock()
	f.code = "482913"
	f.guid = "adb-R5CN20ABC-x1y2z3"
	// A current adb server connects by itself after pairing.
	f.autoSerial = "adb-R5CN20ABC-x1y2z3._adb-tls-connect._tcp"
	f.mu.Unlock()
	if _, err := p.PairWithCode(ctx, "192.168.1.42:37123", "12345"); err == nil {
		t.Error("a five-digit code was accepted")
	}
	if _, err := p.PairWithCode(ctx, "192.168.1.42", "482913"); err == nil {
		t.Error("an address without a port was accepted")
	}
	if _, err := p.PairWithCode(ctx, "192.168.1.42:37123", "000000"); !errors.Is(err, adb.ErrPairing) {
		t.Errorf("wrong code: %v", err)
	}
	info, err := p.PairWithCode(ctx, " 192.168.1.42:37123 ", "482913")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "adb-R5CN20ABC-x1y2z3._adb-tls-connect._tcp" || info.Connection != "wifi" {
		t.Errorf("PairWithCode = %+v", info)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.connects) != 0 {
		t.Errorf("connected by hand although the server had: %q", f.connects)
	}
}

func TestConnectAddr(t *testing.T) {
	svcs := []adb.MDNSService{
		{Instance: "adb-A-1", Type: adb.ServiceConnect, Addr: "192.168.1.5:40000"},
		{Instance: "adb-B-2", Type: adb.ServiceConnect, Addr: "192.168.1.42:41000"},
		{Instance: "pinky-x", Type: adb.ServicePairing, Addr: "192.168.1.42:37123"},
	}
	if got := connectAddr(svcs, "adb-B-2", "10.0.0.1"); got != "192.168.1.42:41000" {
		t.Errorf("by guid = %q", got)
	}
	if got := connectAddr(svcs, "", "192.168.1.42"); got != "192.168.1.42:41000" {
		t.Errorf("by host = %q", got)
	}
	if got := connectAddr(svcs, "adb-C-3", "192.168.1.42"); got != "" {
		t.Errorf("unknown guid = %q", got)
	}
}
