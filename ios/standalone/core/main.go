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

static void pinky_log(const char *s) { os_log(OS_LOG_DEFAULT, "%{public}s", s); }

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
	Interface string   `json:"interface"` // bind to this interface ("utun4"), or "" for routing
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
		logLine(fmt.Sprintf("pinky connected in %v: %d services", time.Since(start).Round(time.Millisecond), len(d.Services())))
		eng.SetPhone(d)
		<-d.Done()
		eng.SetState("tunnel closed")
	}
}

// loopNet holds the tunnel interface's own address: 10.7.1.1 in our
// reflector, 10.7.0.0 in LocalDevVPN's.
var loopNet = &net.IPNet{IP: net.IPv4(10, 7, 0, 0), Mask: net.CIDRMask(16, 32)}

// bound is the interface index dials bind to, 0 for none. Dials first follow
// the routes; if that fails they bind to the tunnel interface, in case iOS
// keeps the extension's own sockets out of its tunnel.
var bound int

func tunnelInterface() (int, error) {
	if cfg.Interface != "" {
		ifi, err := net.InterfaceByName(cfg.Interface)
		if err != nil {
			return 0, err
		}
		return ifi.Index, nil
	}
	ifs, err := net.Interfaces()
	if err != nil {
		return 0, err
	}
	for _, ifi := range ifs {
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && loopNet.Contains(ipn.IP) {
				return ifi.Index, nil
			}
		}
	}
	return 0, errors.New("no interface has an address in " + loopNet.String() + ": is the VPN on?")
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
