package ios

import (
	"context"
	"fmt"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"
)

// link is a tunnel plus the device's service list: everything needed to open
// service connections to one device.
type link struct {
	udid   string
	base   goios.DeviceEntry // the usbmuxd entry
	tunnel *Tunnel
	rsd    goios.RsdHandshakeResponse
	entry  goios.DeviceEntry // routes go-ios connections through the tunnel
}

// dialLink opens a tunnel to the device and reads its service list. The
// first RSD handshake on a tunnel also announces the host: without it the
// device treats us as a legacy peer and its services crash when they send
// large replies such as screenshots. iOS 27.2 accepts that announcement only
// once per tunnel, so later handshakes must not repeat it.
func dialLink(ctx context.Context, udid string) (*link, error) {
	base, err := goios.GetDevice(udid)
	if err != nil {
		return nil, fmt.Errorf("find %s on usbmuxd: %w", udid, err)
	}
	t, err := OpenTunnel(ctx, base)
	if err != nil {
		return nil, err
	}
	l := &link{udid: udid, base: base, tunnel: t}
	l.entry = t.Entry(base, nil)
	rsd, err := l.handshake(ctx, true)
	if err != nil {
		t.Close()
		return nil, err
	}
	l.rsd = rsd
	l.entry = t.Entry(base, rsd)
	return l, nil
}

func (l *link) handshake(ctx context.Context, asHost bool) (goios.RsdHandshakeResponse, error) {
	type result struct {
		h   goios.RsdHandshakeResponse
		err error
	}
	ch := make(chan result, 1)
	go func() {
		s, err := goios.NewWithAddrPortDevice(l.entry.Address, l.tunnel.RSDPort(), l.entry)
		if err != nil {
			ch <- result{err: err}
			return
		}
		defer s.Close()
		var h goios.RsdHandshakeResponse
		if asHost {
			h, err = s.HandshakeAsHost()
		} else {
			h, err = s.Handshake()
		}
		ch <- result{h, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return r.h, fmt.Errorf("RSD handshake: %w", r.err)
		}
		return r.h, nil
	case <-ctx.Done():
		return goios.RsdHandshakeResponse{}, fmt.Errorf("RSD handshake: %w", ctx.Err())
	case <-time.After(15 * time.Second):
		return goios.RsdHandshakeResponse{}, fmt.Errorf("RSD handshake: timed out")
	}
}

// has reports whether the device offers a service.
func (l *link) has(service string) bool { return l.rsd.GetPort(service) != 0 }

func (l *link) Close() error { return l.tunnel.Close() }
