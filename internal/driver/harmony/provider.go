package harmony

import (
	"context"
	"fmt"
	"strings"

	"github.com/DIYgod/pinky/internal/driver"
)

// Provider discovers native HarmonyOS devices without passing them to adb.
// Discovery never installs an app, changes a setting, or authorizes a phone.
type Provider struct {
	find func() (string, error)
	run  func(context.Context, string, ...string) ([]byte, error)
	usb  func(context.Context) ([]string, error)
}

func NewProvider() *Provider {
	return &Provider{find: findHDC, run: runHDC, usb: usbTargets}
}

func (*Provider) Platform() driver.Platform { return driver.HarmonyOS }

// List includes USB devices when the SDK is missing, so plugging in a
// native HarmonyOS phone no longer appears to do nothing.
func (p *Provider) List(ctx context.Context) ([]driver.Info, error) {
	path, err := p.find()
	if err != nil {
		ids, err := p.usb(ctx)
		if err != nil {
			return nil, err
		}
		var infos []driver.Info
		for _, id := range ids {
			info := baseInfo(id, "usb")
			info.State, info.Setup = driver.StateNeedsSetup, "hdc"
			info.Detail = "This phone uses native HarmonyOS, which Pinky cannot control yet. This computer also has no HDC tool for connection diagnostics; Android USB debugging and Pinky Companion do not apply."
			infos = append(infos, info)
		}
		return infos, nil
	}
	out, err := p.run(ctx, path, "list", "targets", "-v")
	if err != nil {
		return nil, err
	}
	return parseTargets(string(out)), nil
}

func baseInfo(id, connection string) driver.Info {
	return driver.Info{ID: id, Platform: driver.HarmonyOS, Name: "HarmonyOS phone", Connection: connection, Backend: "hdc"}
}

func parseTargets(out string) []driver.Info {
	var infos []driver.Info
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || (fields[1] != "USB" && fields[1] != "TCP") || seen[fields[0]] {
			continue
		}
		seen[fields[0]] = true
		connection := "usb"
		if fields[1] == "TCP" {
			connection = "wifi"
		}
		info := baseInfo(fields[0], connection)
		switch strings.ToLower(fields[2]) {
		case "unauthorized":
			info.State, info.Setup = driver.StateUnauthorized, driver.FixAuthorize
			info.Detail = "Unlock the HarmonyOS phone and allow USB debugging from this computer."
		case "connected":
			// Do not advertise an Android/scrcpy backend for an HDC
			// device: it needs a native HarmonyOS driver.
			info.State, info.Setup = driver.StateNeedsSetup, "harmony_backend"
			info.Detail = "The HarmonyOS phone is connected through HDC. Native HarmonyOS screen control is not available in this build."
		default:
			info.State = driver.StateOffline
			info.Detail = "The HarmonyOS phone is not connected through HDC."
		}
		infos = append(infos, info)
	}
	return infos
}

func (p *Provider) Open(ctx context.Context, id string) (driver.Device, error) {
	infos, err := p.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		if info.ID == id {
			if info.State == driver.StateUnauthorized {
				return nil, fmt.Errorf("HarmonyOS phone needs debugging authorization: %w", driver.ErrOffline)
			}
			return nil, fmt.Errorf("native HarmonyOS screen control: %w", driver.ErrUnsupported)
		}
	}
	return nil, driver.ErrNotFound
}

var _ driver.Provider = (*Provider)(nil)
