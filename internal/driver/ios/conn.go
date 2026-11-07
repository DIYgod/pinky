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
func dialLink(ctx context.Context, udid string, wifi bool) (*link, error) {
	base, err := findEntry(udid, wifi)
	if err != nil {
		return nil, err
	}
	return dialLinkEntry(ctx, base)
}

// dialLinkEntry opens a tunnel through one usbmuxd entry.
func dialLinkEntry(ctx context.Context, base goios.DeviceEntry) (*link, error) {
	udid := base.Properties.SerialNumber
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

// findEntry returns the usbmuxd entry for a device. A phone that is on USB
// and Wi-Fi at once is listed twice; USB wins unless wifi asks for the
// network entry, which then must exist.
func findEntry(udid string, wifi bool) (goios.DeviceEntry, error) {
	list, err := goios.ListDevices()
	if err != nil {
		return goios.DeviceEntry{}, fmt.Errorf("usbmuxd: %w", err)
	}
	var found *goios.DeviceEntry
	for i, e := range list.DeviceList {
		if e.Properties.SerialNumber != udid {
			continue
		}
		network := e.Properties.ConnectionType == "Network"
		if wifi && !network {
			continue
		}
		if found == nil || !network {
			found = &list.DeviceList[i]
		}
	}
	if found == nil {
		if wifi {
			return goios.DeviceEntry{}, fmt.Errorf("iPhone %s is not reachable over Wi-Fi", udid)
		}
		return goios.DeviceEntry{}, fmt.Errorf("iPhone %s is not connected", udid)
	}
	return *found, nil
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
