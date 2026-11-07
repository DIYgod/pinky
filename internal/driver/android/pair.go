package android

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/driver/android/mdns"
)

// Pairing phones over Wi-Fi (wireless debugging, Android 11 and later).
// The adb server pairs (host:pair) and connects (host:connect); the driver
// finds the phone's pairing and connect services on the network, both in
// the server's own mDNS discovery and with a search of its own (Browser),
// because the server's discovery stops working after the computer's
// network changes and then never sees the phone: both sides would wait.

const (
	pairPoll = 500 * time.Millisecond
	// serverGrace is how long the adb server gets to connect by itself to
	// a phone that paired while its discovery lists the phone.
	serverGrace = time.Second
)

// connectTimeout is how long a phone that paired, or that ConnectWiFi
// connected to, gets to come online.
var connectTimeout = 30 * time.Second

// StartPairing implements driver.Pairer: the person scans QR() under
// Developer options > Wireless debugging > Pair device with QR code, the
// phone then advertises a pairing service named in the code, and Wait
// pairs with it, connects, and returns the phone once it is online.
func (p *Provider) StartPairing(ctx context.Context) (driver.Pairing, error) {
	// The server pairs and connects, so it must be there; its own
	// discovery may be missing or broken, since the driver searches too.
	serverMDNS := true
	if _, err := p.cfg.Client.MDNSCheck(ctx); err != nil {
		var ae *adb.Error
		if !errors.As(err, &ae) {
			return nil, fmt.Errorf("android: no adb server to pair with (%v): %w", err, driver.ErrUnsupported)
		}
		serverMDNS = false
	}
	own, ownErr := p.browse(adb.ServicePairing, adb.ServiceConnect)
	if !serverMDNS && own == nil {
		return nil, fmt.Errorf("android: neither the adb server nor Pinky can look for phones on the network (%v): %w", ownErr, driver.ErrUnsupported)
	}
	name, err := randomText(10)
	if err != nil {
		closeBrowse(own)
		return nil, err
	}
	pass, err := randomText(12)
	if err != nil {
		closeBrowse(own)
		return nil, err
	}
	q := &qrPairing{p: p, name: "pinky-" + name, pass: pass, serverMDNS: serverMDNS, own: own, done: make(chan struct{})}
	if ownErr != nil {
		q.progress.SearchErr = ownErr.Error()
	}
	return q, nil
}

// PairWithCode implements driver.Pairer with the address and six-digit
// code the phone shows under Wireless debugging > Pair device with pairing
// code. It then connects to the phone's connect service, which it finds
// with mDNS, and returns the phone once it is online. When that service
// does not show up, the error wraps driver.ErrPairedNotFound: ConnectWiFi
// then takes the address the phone shows.
func (p *Provider) PairWithCode(ctx context.Context, addr, code string) (driver.Info, error) {
	addr = strings.TrimSpace(addr)
	code = strings.TrimSpace(code)
	if !sixDigits(code) {
		return driver.Info{}, errors.New("android: a pairing code has six digits")
	}
	host, err := splitAddr(addr)
	if err != nil {
		return driver.Info{}, err
	}
	// Search while the phone pairs: its connect service is there already.
	own, _ := p.browse(adb.ServiceConnect)
	defer closeBrowse(own)
	guid, err := p.cfg.Client.Pair(ctx, addr, code)
	if err != nil {
		return driver.Info{}, pairError(err)
	}
	return p.connectPaired(ctx, guid, host, own)
}

// ConnectWiFi implements driver.WiFiConnector: it connects to a phone
// paired with this computer at the address Wireless debugging shows (IP
// address & Port), and returns it once it is online.
func (p *Provider) ConnectWiFi(ctx context.Context, addr string) (driver.Info, error) {
	addr = strings.TrimSpace(addr)
	if _, err := splitAddr(addr); err != nil {
		return driver.Info{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := p.cfg.Client.Connect(ctx, addr); err != nil {
		return driver.Info{}, wrapErr(err)
	}
	for {
		entries, err := p.cfg.Client.Devices(ctx)
		if err != nil {
			return driver.Info{}, wrapErr(err)
		}
		for _, e := range entries {
			if e.Serial == addr && e.State == adb.StateDevice {
				return p.infoFor(ctx, e.Serial)
			}
		}
		if err := sleepCtx(ctx, pairPoll); err != nil {
			return driver.Info{}, fmt.Errorf("android: adb connected to %s, but the phone did not come online: %w", addr, driver.ErrOffline)
		}
	}
}

// splitAddr checks an address and port the person typed and returns the
// host.
func splitAddr(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if n, perr := strconv.Atoi(port); err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("android: %q is not an address and port such as 192.168.1.42:37123", addr)
	}
	return host, nil
}

// qrPairing is a QR pairing in progress.
type qrPairing struct {
	p          *Provider
	name, pass string
	serverMDNS bool   // the adb server has mDNS discovery
	own        Browse // the driver's own search; nil when it could not start
	done       chan struct{}
	once       sync.Once

	mu       sync.Mutex
	progress driver.PairingProgress
}

// QR implements driver.Pairing: the text Android's QR scanner expects.
func (q *qrPairing) QR() string {
	return "WIFI:T:ADB;S:" + q.name + ";P:" + q.pass + ";;"
}

// Progress implements driver.PairingReporter.
func (q *qrPairing) Progress() driver.PairingProgress {
	q.mu.Lock()
	pr := q.progress
	q.mu.Unlock()
	if q.own != nil && !pr.Found {
		if err := q.own.Err(); err != nil {
			pr.SearchErr = err.Error()
			pr.NotAllowed = errors.Is(err, mdns.ErrNotAllowed)
		}
	}
	return pr
}

func (q *qrPairing) update(fn func(*driver.PairingProgress)) {
	q.mu.Lock()
	fn(&q.progress)
	q.mu.Unlock()
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
	addr, err := q.find(ctx)
	if err != nil {
		return driver.Info{}, q.closedErr(ctx, err)
	}
	q.update(func(pr *driver.PairingProgress) { pr.Found = true })
	q.p.cfg.logf("android: pairing with the phone at %s", addr)
	guid, err := q.p.cfg.Client.Pair(ctx, addr, q.pass)
	if err != nil {
		return driver.Info{}, q.closedErr(ctx, pairError(err))
	}
	q.update(func(pr *driver.PairingProgress) { pr.Paired = true })
	host, _, _ := net.SplitHostPort(addr)
	info, err := q.p.connectPaired(ctx, guid, host, q.own)
	return info, q.closedErr(ctx, err)
}

// find waits for the phone's pairing service: listed by the adb server,
// or found by the driver's own search when the server's discovery misses
// it.
func (q *qrPairing) find(ctx context.Context) (string, error) {
	for {
		if q.serverMDNS {
			listed, err := q.p.cfg.Client.MDNSServices(ctx)
			var ae *adb.Error
			if err != nil && !errors.As(err, &ae) {
				return "", wrapErr(err) // the server went away
			}
			if addr := pairingAddr(listed, q.name); addr != "" {
				return addr, nil
			}
		}
		if q.own != nil {
			if addr := pairingAddr(q.own.Services(), q.name); addr != "" {
				if q.serverMDNS {
					q.p.cfg.logf("android: the adb server's discovery does not list %s; Pinky found it at %s", q.name, addr)
				}
				return addr, nil
			}
		}
		if err := sleepCtx(ctx, pairPoll); err != nil {
			return "", err
		}
	}
}

// pairingAddr returns the address of the pairing service with this name.
func pairingAddr(svcs []adb.MDNSService, name string) string {
	for _, s := range svcs {
		if s.Type == adb.ServicePairing && s.Instance == name {
			return s.Addr
		}
	}
	return ""
}

// Close implements driver.Pairing; a Wait in progress returns.
func (q *qrPairing) Close() error {
	q.once.Do(func() {
		close(q.done)
		closeBrowse(q.own)
	})
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

func closeBrowse(b Browse) {
	if b != nil {
		b.Close()
	}
}

// connectPaired waits for a phone that just paired to come online: the
// adb server connects by itself to a phone its discovery lists (its serial
// is then the mDNS name, "adb-<serial>-<suffix>._adb-tls-connect._tcp");
// otherwise the driver connects to the phone's connect service, which the
// server lists or the driver's own search found, by the phone's guid, or
// by the address it paired from when the server reported no guid.
func (p *Provider) connectPaired(ctx context.Context, guid, host string, own Browse) (driver.Info, error) {
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
			if pairedSerial(e.Serial, guid, connected) && e.State == adb.StateDevice {
				return p.infoFor(ctx, e.Serial)
			}
		}
		if connected == "" {
			listed, _ := c.MDNSServices(ctx) // a server without discovery lists nothing
			addr := connectAddr(listed, guid, host)
			byServer := addr != ""
			if addr == "" && own != nil {
				addr = connectAddr(own.Services(), guid, host)
			}
			// A server that lists the phone connects to it by itself; give it
			// a moment first. One that does not will never connect.
			if addr != "" && (!byServer || time.Since(start) > serverGrace) {
				if !byServer {
					p.cfg.logf("android: the adb server's discovery does not list the paired phone; connecting to %s, which Pinky found", addr)
				}
				if err := c.Connect(ctx, addr); err != nil {
					p.cfg.logf("android: connecting to %s: %v", addr, err)
				} else {
					connected = addr
				}
			}
		}
		if err := sleepCtx(ctx, pairPoll); err != nil {
			if connected == "" {
				return driver.Info{}, fmt.Errorf("android: the phone paired, but its wireless debugging service did not show up; check that Wireless debugging is still on and the phone is on the same network: %w", driver.ErrPairedNotFound)
			}
			return driver.Info{}, fmt.Errorf("android: the phone paired and adb connected to %s, but it did not come online: %w", connected, driver.ErrOffline)
		}
	}
}

// infoFor returns the listed phone that has the connection with this adb
// serial, with its properties. Over Wi-Fi a phone is listed by its
// hardware serial, not by the connection's.
func (p *Provider) infoFor(ctx context.Context, serial string) (driver.Info, error) {
	infos, err := p.List(ctx)
	if err != nil {
		return driver.Info{}, err
	}
	id := serial
	if entries, err := p.cfg.Client.Devices(ctx); err == nil {
		for _, e := range entries {
			if e.Serial != serial || e.State != adb.StateDevice {
				continue
			}
			if pr, err := p.deviceProps(ctx, e); err == nil {
				id = phoneID(e, pr) // read once per connection: List did
			}
		}
	}
	for _, in := range infos {
		if in.ID == id {
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

var (
	_ driver.Pairer          = (*Provider)(nil)
	_ driver.WiFiConnector   = (*Provider)(nil)
	_ driver.PairingReporter = (*qrPairing)(nil)
)
