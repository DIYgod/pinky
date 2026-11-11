//go:build ios

// Command core is the prototype's Go side as a C library for the iOS app
// and its packet-tunnel extension (go build -buildmode=c-archive; see
// ../build.sh). Either process can host it: the extension keeps
// running while other apps are in front, the app only as long as iOS lets
// it.
//
// It connects to the phone it runs on through its own loopback: the
// extension's reflector turns packets for 10.7.0.1 back to the phone, where
// remotepairingd answers as it would a computer on the network. Over that
// it pair-verifies with the pairing the app carries, opens a TLS-PSK tunnel
// and hands it to Pinky's iOS driver.
package main

/*
#include <stdlib.h>
#include <os/log.h>
#include <mach/mach.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <netinet/in.h>
#include <arpa/inet.h>

static void pinky_log(const char *s) { os_log(OS_LOG_DEFAULT, "%{public}s", s); }

// pinky_tunnel_index is the index of the interface holding an IPv4 address
// in 10.7.0.0/16, the tunnel's own (10.7.0.2 in our reflector, 10.7.1.1 or
// 10.7.0.2 in LocalDevVPN's), or 0. getifaddrs, because the sandbox denies
// the routing sysctl Go's net.Interfaces reads.
static unsigned int pinky_tunnel_index(void) {
	struct ifaddrs *list, *i;
	unsigned int idx = 0;
	if (getifaddrs(&list) != 0) return 0;
	for (i = list; i; i = i->ifa_next) {
		if (!i->ifa_addr || i->ifa_addr->sa_family != AF_INET) continue;
		uint32_t a = ntohl(((struct sockaddr_in *)i->ifa_addr)->sin_addr.s_addr);
		if ((a >> 16) == ((10u << 8) | 7u)) { idx = if_nametoindex(i->ifa_name); break; }
	}
	freeifaddrs(list);
	return idx;
}

static unsigned long long pinky_footprint(void) {
	task_vm_info_data_t info;
	mach_msg_type_number_t count = TASK_VM_INFO_COUNT;
	if (task_info(mach_task_self(), TASK_VM_INFO, (task_info_t)&info, &count) != KERN_SUCCESS) return 0;
	return info.phys_footprint;
}
*/
import "C"

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/ios"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/ios/standalone/agent"
	"github.com/DIYgod/pinky/ios/standalone/engine"
	"github.com/DIYgod/pinky/ios/standalone/rppair"
)

func main() {}

// config is PinkyStart's JSON.
type config struct {
	Pairing   string   `json:"pairing"` // the pairing file, base64
	UDID      string   `json:"udid"`
	Addr      string   `json:"addr"`      // remotepairingd through the reflector, "10.7.0.1:49152"
	Interface string   `json:"interface"` // "tunnel" binds to the tunnel interface; "" routes, then binds if that fails
	Self      []string `json:"self"`      // Pinky's bundle ids
	Host      string   `json:"host"`      // "extension" or "app", for the log
	MemoryMB  int      `json:"memory_mb"` // Go's soft memory limit
	Heartbeat int      `json:"heartbeat_s"`
	Model     string   `json:"model"`
}

var (
	mu  sync.Mutex
	eng *engine.Engine
	cfg config
)

func logLine(s string) {
	cs := C.CString(s)
	C.pinky_log(cs)
	C.free(unsafe.Pointer(cs))
}

type osLog struct{}

func (osLog) Write(p []byte) (int, error) {
	logLine("pinky " + strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func reply(v any, err error) *C.char {
	m := map[string]any{"ok": err == nil}
	if err != nil {
		m["error"] = err.Error()
	} else if v != nil {
		m["result"] = v
	}
	b, _ := json.Marshal(m)
	return C.CString(string(b))
}

// PinkyStart starts the engine and its connection to the phone. It returns
// {"ok":true} or {"ok":false,"error":...}; free the result with PinkyFree.
//
//export PinkyStart
func PinkyStart(js *C.char) *C.char {
	mu.Lock()
	defer mu.Unlock()
	if eng != nil {
		return reply(nil, nil)
	}
	var c config
	if err := json.Unmarshal([]byte(C.GoString(js)), &c); err != nil {
		return reply(nil, err)
	}
	raw, err := base64.StdEncoding.DecodeString(c.Pairing)
	if err != nil {
		return reply(nil, fmt.Errorf("pairing: %w", err))
	}
	id, err := rppair.ParseIdentity(raw)
	if err != nil {
		return reply(nil, fmt.Errorf("pairing: %w", err))
	}
	if c.Addr == "" {
		c.Addr = "10.7.0.1:49152"
	}
	if c.MemoryMB > 0 {
		debug.SetMemoryLimit(int64(c.MemoryMB) << 20)
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = 10
	}
	cfg = c
	eng = engine.New(engine.Config{Self: c.Self, Log: func(s string) { logLine("pinky " + s) }, Agent: agent.Config{Model: c.Model}})
	go connectLoop(id)
	go heartbeat(time.Duration(c.Heartbeat) * time.Second)
	logLine(fmt.Sprintf("pinky started in the %s (%s %s)", c.Host, runtime.Version(), runtime.GOARCH))
	return reply(nil, nil)
}

// PinkyCall answers one engine request (engine.Request as JSON).
//
//export PinkyCall
func PinkyCall(js *C.char) *C.char {
	mu.Lock()
	e := eng
	mu.Unlock()
	if e == nil {
		return reply(nil, errors.New("not started"))
	}
	return C.CString(string(e.Handle(context.Background(), []byte(C.GoString(js)))))
}

// PinkyStop stops the task at once (the Live Activity's Stop).
//
//export PinkyStop
func PinkyStop() {
	mu.Lock()
	e := eng
	mu.Unlock()
	if e != nil {
		e.StopTask()
	}
}

// PinkyUIActive records whether Pinky's own UI is in front.
//
//export PinkyUIActive
func PinkyUIActive(on C.int) {
	mu.Lock()
	e := eng
	mu.Unlock()
	if e != nil {
		e.SetUIActive(on != 0)
	}
}

// PinkyFree frees a string PinkyStart or PinkyCall returned.
//
//export PinkyFree
func PinkyFree(p *C.char) { C.free(unsafe.Pointer(p)) }

func connectLoop(id rppair.Identity) {
	wait := time.Second
	for {
		start := time.Now()
		d, err := connect(id)
		if err != nil {
			eng.SetState("failed: " + err.Error())
			time.Sleep(wait)
			wait = min(wait*2, 30*time.Second)
			continue
		}
		wait = time.Second
		rax := false
		for _, s := range d.Services() {
			rax = rax || strings.HasSuffix(s, ".remoteAXService")
		}
		logLine(fmt.Sprintf("pinky connected in %v: %d services, remoteAXService %v", time.Since(start).Round(time.Millisecond), len(d.Services()), rax))
		eng.SetPhone(&phone{Device: d, ids: map[string]string{"Pinky Local": "cc.diygod.pinky.standalone"}})
		<-d.Done()
		eng.SetState("tunnel closed")
	}
}

// phone is the device the engine drives. Its reads log why the remote
// accessibility service failed, which the inspector fallback's own error
// hides.
//
// Over the tunnel alone the driver cannot read the phone's app catalog (the
// installation proxy it uses comes through usbmuxd), so a read names the app
// in front by its label only. The wrapper gives it its bundle id back: Pinky
// Local's own, which the engine's guard needs, and that of the app the
// engine last launched, learned from the first read after the launch.
type phone struct {
	*ios.Device
	mu       sync.Mutex
	ids      map[string]string // the label in front -> bundle id
	launched string            // the app launched last, until a read names it
}

func (p *phone) LaunchID(ctx context.Context, bundleID string) error {
	err := p.Device.LaunchID(ctx, bundleID)
	p.mu.Lock()
	if err == nil {
		p.launched = bundleID
	}
	p.mu.Unlock()
	return err
}

func (p *phone) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	s, st, err := p.ObserveWithStats(ctx, opt)
	if st.RAXError != nil {
		logLine("pinky observe: remoteAXService failed: " + st.RAXError.Error())
	}
	if st.Total > 2*time.Second {
		logLine(fmt.Sprintf("pinky observe: slow read: walk %v, total %v", st.Walk.Round(time.Millisecond), st.Total.Round(time.Millisecond)))
	}
	if err == nil && s.App.ID == "" && s.App.Name != "" {
		p.mu.Lock()
		if id, ok := p.ids[s.App.Name]; ok {
			s.App.ID = id
		} else if p.launched != "" {
			p.ids[s.App.Name] = p.launched
			s.App.ID = p.launched
			logLine(fmt.Sprintf("pinky observe: %q in front is %s", s.App.Name, p.launched))
		}
		p.launched = ""
		p.mu.Unlock()
	}
	return s, err
}

// bound is the interface index dials bind to, 0 for none. The extension
// binds to the tunnel interface from the start, since its own sockets skip
// its tunnel; the app follows the routes and binds only if that fails.
var bound int

func tunnelInterface() (int, error) {
	if i := int(C.pinky_tunnel_index()); i != 0 {
		return i, nil
	}
	return 0, errors.New("no interface has an address in 10.7.0.0/16: is the VPN on?")
}

func dialTCP(ctx context.Context, addr string, ifIndex int) (net.Conn, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	if ifIndex != 0 {
		d.Control = func(network, address string, rc syscall.RawConn) error {
			var serr error
			if err := rc.Control(func(fd uintptr) { serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, ifIndex) }); err != nil {
				return err
			}
			return serr
		}
	}
	return d.DialContext(ctx, "tcp", addr)
}

func connect(id rppair.Identity) (*ios.Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host, _, _ := net.SplitHostPort(cfg.Addr)
	dial := func(ctx context.Context, addr string) (net.Conn, error) { return dialTCP(ctx, addr, bound) }
	if cfg.Interface != "" && bound == 0 {
		idx, err := tunnelInterface()
		if err != nil {
			return nil, err
		}
		bound = idx
	}
	conn, err := dial(ctx, cfg.Addr)
	if err != nil && bound == 0 {
		idx, ierr := tunnelInterface()
		if ierr != nil {
			return nil, fmt.Errorf("remotepairingd: %w (%v)", err, ierr)
		}
		c, err2 := dialTCP(ctx, cfg.Addr, idx)
		if err2 != nil {
			return nil, fmt.Errorf("remotepairingd: %w; bound to the tunnel: %v", err, err2)
		}
		logLine(fmt.Sprintf("pinky dial: routed failed (%v); bound to interface %d works", err, idx))
		bound, conn, err = idx, c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("remotepairingd: %w", err)
	}
	s, err := rppair.Verify(ctx, conn, id)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("pair-verify: %w", err)
	}
	t, err := s.Tunnel(ctx, func(ctx context.Context, port int) (net.Conn, error) {
		return dial(ctx, net.JoinHostPort(host, fmt.Sprint(port)))
	})
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("tunnel: %w", err)
	}
	model, _ := s.Peer["model"].(string)
	info := driver.Info{ID: cfg.UDID, Platform: driver.IOS, Name: "this iPhone", Model: model, Connection: "loopback"}
	log := slog.New(slog.NewTextHandler(osLog{}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d, err := ios.OpenOverTunnel(ctx, info, t, log)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("driver: %w", err)
	}
	// The pairing session must stay open while its tunnel lives.
	go func() { <-d.Done(); s.Close() }()
	return d, nil
}

func heartbeat(every time.Duration) {
	var ms runtime.MemStats
	for range time.Tick(every) {
		runtime.ReadMemStats(&ms)
		var b bytes.Buffer
		fmt.Fprintf(&b, "%s host=%s footprint=%dMB heap=%dMB goroutines=%d",
			eng.Heartbeat(), cfg.Host, uint64(C.pinky_footprint())>>20, ms.HeapInuse>>20, runtime.NumGoroutine())
		logLine(b.String())
	}
}
