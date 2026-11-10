package android

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
)

// An adb server cut off from the local network. On macOS (15 and later) a
// process may use the local network only while the app responsible for it
// may (Privacy & Security › Local Network), and the adb server Pinky
// starts counts as Pinky. It keeps the Pinky that started it as its app
// after that one quit, and once an update replaced it, the server lost the
// network: on 2026-11-08 a server that Pinky 2026.1109.8 had started got
// "No route to host" at once for every phone on the network after Pinky
// updated itself to 2026.1109.10, while Pinky, a shell and a server
// started anew reached them. Its mDNS discovery listed nothing, pairing
// failed with "Unable to start pairing client", and the phones on Wi-Fi
// never came back; the ones on a cable worked.
//
// So when the server gets no route to an address that Pinky's own process
// reaches, the driver stops the server, if Pinky started it (in this run
// or an earlier one: adb.Client.OwnServer), and starts another one, which
// counts as the Pinky that runs. A server another program started it
// leaves running, as always, and CheckHost says what to do
// (adb_no_network).

// offNetRetry is the shortest time between two restarts of a server cut
// off from the network: one that cannot reach the network either after a
// restart is left for CheckHost to report.
const offNetRetry = 10 * time.Minute

// dialWait is how long Pinky's own connection to an address may take.
const dialWait = 3 * time.Second

// dialOwn connects to addr from Pinky's own process, and closes the
// connection at once.
func dialOwn(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: dialWait}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

// serverCutOff reports whether the adb server failed to reach addr because
// it cannot use the local network: its words say that there was no route
// to addr (connecting) or that it could not start its pairing client
// (pairing; adb says that for no route and for a refused connection
// alike), while Pinky's own connection to addr goes through.
func (p *Provider) serverCutOff(ctx context.Context, addr string, err error) bool {
	if err == nil || addr == "" {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "no route to host") && !strings.Contains(msg, "unable to start pairing client") {
		return false
	}
	return p.dial(ctx, addr) == nil
}

// serverOffNetwork deals with an adb server that could not reach addr
// while Pinky could (serverCutOff): it stops one Pinky started and starts
// another (Config.StopServer, Config.StartServer), at most once per
// offNetRetry, and leaves any other running for CheckHost to report. It
// reports whether a new server runs.
func (p *Provider) serverOffNetwork(ctx context.Context, addr string) bool {
	if p.cfg.StartServer == nil || !p.server.tryRestart(time.Now()) {
		p.server.cutOff(addr)
		return false
	}
	err := p.cfg.StopServer(ctx)
	if err != nil {
		own := !errors.Is(err, adb.ErrNotOwnServer)
		p.server.restarted(time.Now(), own, false, nil)
		p.server.cutOff(addr)
		if own {
			p.cfg.logf("android: the adb server Pinky started cannot reach %s on the local network, which Pinky reaches; stopping it: %v", addr, err)
		} else {
			p.cfg.logf("android: the adb server cannot reach %s on the local network, which Pinky reaches; another program started it, so Pinky leaves it running", addr)
		}
		return false
	}
	p.cfg.logf("android: the adb server Pinky started could not reach %s on the local network, which Pinky reaches (it lost Local Network access); Pinky stopped it and starts another", addr)
	serr := p.cfg.StartServer(ctx)
	p.server.restarted(time.Now(), true, true, serr)
	if serr != nil {
		p.cfg.logf("android: starting an adb server after stopping the one cut off from the network: %v", serr)
		return false
	}
	return true
}

// connectAddr has the adb server connect at addr, as Client.Connect. A
// server that cannot reach the network while Pinky can is restarted when
// Pinky started it, and asked again; otherwise the error wraps
// driver.ErrPairNoNetwork.
func (p *Provider) connectAddr(ctx context.Context, addr string) error {
	err := p.cfg.Client.Connect(ctx, addr)
	if err != nil && p.serverCutOff(ctx, addr, err) {
		if !p.serverOffNetwork(ctx, addr) {
			return fmt.Errorf("android: connecting to %s failed: %w (%w)", addr, driver.ErrPairNoNetwork, err)
		}
		err = p.cfg.Client.Connect(ctx, addr)
	}
	if err == nil {
		p.server.reached()
	}
	return err
}
