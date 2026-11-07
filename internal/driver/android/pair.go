package android

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
)

// Pairing phones over Wi-Fi (wireless debugging, Android 11 and later).
// The adb server does the work: it discovers the phone's pairing and
// connect services with mDNS, runs the pairing protocol and connects over
// TLS. The driver tells it what to pair with and waits for the phone.

const (
	pairPoll       = 500 * time.Millisecond
	connectTimeout = 30 * time.Second // after pairing, for the phone to come online
)

// StartPairing implements driver.Pairer: the person scans QR() under
// Developer options › Wireless debugging › Pair device with QR code, the
// phone then advertises a pairing service named in the code, and Wait
// pairs with it, connects, and returns the phone once it is online.
func (p *Provider) StartPairing(ctx context.Context) (driver.Pairing, error) {
	if _, err := p.cfg.Client.MDNSCheck(ctx); err != nil {
		return nil, fmt.Errorf("android: the adb server cannot find phones on the network (mDNS: %v): %w", err, driver.ErrUnsupported)
	}
	name, err := randomText(10)
	if err != nil {
		return nil, err
	}
	pass, err := randomText(12)
	if err != nil {
		return nil, err
	}
	return &qrPairing{p: p, name: "pinky-" + name, pass: pass, done: make(chan struct{})}, nil
}

// PairWithCode implements driver.Pairer with the address and six-digit
// code the phone shows under Wireless debugging › Pair device with pairing
// code. It then connects to the phone's connect service, which it finds
// with mDNS, and returns the phone once it is online.
func (p *Provider) PairWithCode(ctx context.Context, addr, code string) (driver.Info, error) {
	addr = strings.TrimSpace(addr)
	code = strings.TrimSpace(code)
	if !sixDigits(code) {
		return driver.Info{}, errors.New("android: a pairing code has six digits")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return driver.Info{}, fmt.Errorf("android: %q is not an address and port such as 192.168.1.42:37123", addr)
	}
	guid, err := p.cfg.Client.Pair(ctx, addr, code)
	if err != nil {
		return driver.Info{}, pairError(err)
	}
	return p.connectPaired(ctx, guid, host)
}

// qrPairing is a QR pairing in progress.
type qrPairing struct {
	p          *Provider
	name, pass string
	done       chan struct{}
	once       sync.Once
}

// QR implements driver.Pairing: the text Android's QR scanner expects.
func (q *qrPairing) QR() string {
	return "WIFI:T:ADB;S:" + q.name + ";P:" + q.pass + ";;"
}

// Wait implements driver.Pairing.
func (q *qrPairing) Wait(ctx context.Context) (driver.Info, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-q.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	var addr string
	for addr == "" {
		svcs, err := q.p.cfg.Client.MDNSServices(ctx)
		if err != nil {
			return driver.Info{}, q.closedErr(ctx, wrapErr(err))
		}
		for _, s := range svcs {
			if s.Type == adb.ServicePairing && s.Instance == q.name {
				addr = s.Addr
			}
		}
		if addr == "" {
			if err := sleepCtx(ctx, pairPoll); err != nil {
				return driver.Info{}, q.closedErr(ctx, err)
			}
		}
	}
	q.p.cfg.logf("android: pairing with the phone at %s", addr)
	guid, err := q.p.cfg.Client.Pair(ctx, addr, q.pass)
	if err != nil {
		return driver.Info{}, q.closedErr(ctx, pairError(err))
	}
	host, _, _ := net.SplitHostPort(addr)
	info, err := q.p.connectPaired(ctx, guid, host)
	return info, q.closedErr(ctx, err)
}

// Close implements driver.Pairing; a Wait in progress returns.
func (q *qrPairing) Close() error {
	q.once.Do(func() { close(q.done) })
	return nil
}

func (q *qrPairing) closedErr(ctx context.Context, err error) error {
	select {
	case <-q.done:
		if err != nil {
			return errors.New("android: pairing closed")
		}
	default:
	}
	return err
}

// connectPaired waits for a phone that just paired to come online: the
// adb server may connect to it by itself (its serial is then the mDNS
// name, "adb-<serial>-<suffix>._adb-tls-connect._tcp"); otherwise the
// driver connects to the phone's connect service, found by its guid, or
// by its address when the server reported none.
func (p *Provider) connectPaired(ctx context.Context, guid, host string) (driver.Info, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	c := p.cfg.Client
	var connected string // the address the driver connected to
	start := time.Now()
	for {
		entries, err := c.Devices(ctx)
		if err != nil {
			return driver.Info{}, wrapErr(err)
		}
		for _, e := range entries {
			if !pairedSerial(e.Serial, guid, connected) {
				continue
			}
			if e.State == adb.StateDevice {
				return p.infoFor(ctx, e.Serial)
			}
		}
		// Give the server a moment to connect by itself first.
		if connected == "" && time.Since(start) > time.Second {
			svcs, err := c.MDNSServices(ctx)
			if err != nil {
				return driver.Info{}, wrapErr(err)
			}
			if addr := connectAddr(svcs, guid, host); addr != "" {
				if err := c.Connect(ctx, addr); err != nil {
					p.cfg.logf("android: connecting to %s: %v", addr, err)
				} else {
					connected = addr
				}
			}
		}
		if err := sleepCtx(ctx, pairPoll); err != nil {
			if connected == "" {
				return driver.Info{}, errors.New("android: the phone paired, but its wireless debugging service did not show up; check that Wireless debugging is still on and the phone is on the same network")
			}
			return driver.Info{}, fmt.Errorf("android: the phone paired and adb connected to %s, but it did not come online: %w", connected, driver.ErrOffline)
		}
	}
}

// infoFor returns the listed phone with this serial, with its properties.
func (p *Provider) infoFor(ctx context.Context, serial string) (driver.Info, error) {
	infos, err := p.List(ctx)
	if err != nil {
		return driver.Info{}, err
	}
	for _, in := range infos {
		if in.ID == serial {
			return in, nil
		}
	}
	return driver.Info{}, fmt.Errorf("android: %s: %w", serial, driver.ErrNotFound)
}

// pairedSerial reports whether a device serial is the phone that paired:
// the address the driver connected to, or the server's own connection
// named after the phone's guid.
func pairedSerial(serial, guid, connected string) bool {
	if connected != "" && serial == connected {
		return true
	}
	return guid != "" && (serial == guid || strings.HasPrefix(serial, guid+"."))
}

// connectAddr picks the paired phone's connect service: the one named
// after its guid, or else the only one at the address it paired from.
func connectAddr(svcs []adb.MDNSService, guid, host string) string {
	var atHost []string
	for _, s := range svcs {
		if s.Type != adb.ServiceConnect {
			continue
		}
		if guid != "" && s.Instance == guid {
			return s.Addr
		}
		if h, _, err := net.SplitHostPort(s.Addr); err == nil && h == host {
			atHost = append(atHost, s.Addr)
		}
	}
	if guid == "" && len(atHost) == 1 {
		return atHost[0]
	}
	return ""
}

func pairError(err error) error {
	if errors.Is(err, adb.ErrPairing) {
		return fmt.Errorf("android: pairing failed (a wrong code, or the phone closed the pairing dialog): %w", err)
	}
	return wrapErr(err)
}

func sixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// randomText returns n random letters and digits: safe in a QR code's
// WIFI: fields, an mDNS name and the adb server's pair request.
func randomText(n int) (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	for i := range b {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[v.Int64()]
	}
	return string(b), nil
}

var _ driver.Pairer = (*Provider)(nil)
