package harmony

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/DIYgod/pinky/internal/driver"
)

// Provider discovers native HarmonyOS devices without passing them to adb.
// Discovery never installs an app, changes a setting, or authorizes a phone.
type Provider struct {
	find  func() (string, error)
	run   func(context.Context, string, ...string) ([]byte, error)
	usb   func(context.Context) ([]string, error)
	mu    sync.Mutex
	facts map[string]driver.Info
}

func NewProvider() *Provider {
	return &Provider{find: findHDC, run: runHDC, usb: usbTargets, facts: map[string]driver.Info{}}
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
			info.SetupCheck = driver.CheckHost
			info.Detail = "This native HarmonyOS phone needs the HarmonyOS SDK's hdc tool. Set PINKY_HDC to its executable; Android ADB and Pinky Companion do not apply."
			infos = append(infos, info)
		}
		return infos, nil
	}
	out, err := p.run(ctx, path, "list", "targets", "-v")
	if err != nil {
		return nil, err
	}
	infos := parseTargets(string(out))
	for i := range infos {
		if infos[i].State != driver.StateOnline {
			continue
		}
		p.mu.Lock()
		fact, ok := p.facts[infos[i].ID]
		p.mu.Unlock()
		if !ok {
			fact = infos[i]
			props, err := p.run(ctx, path, "-t", fact.ID, "shell", "param get const.product.model; param get const.ohos.fullname; uitest --version")
			if err != nil {
				return nil, err
			}
			lines := strings.FieldsFunc(strings.TrimSpace(string(props)), func(r rune) bool { return r == '\n' || r == '\r' })
			if len(lines) < 3 || containsFailure(string(props)) {
				infos[i].State, infos[i].Setup = driver.StateNeedsSetup, "harmony_backend"
				infos[i].SetupCheck = "harmony_control"
				infos[i].Detail = "The phone's native UITest control tools are not available."
				continue
			}
			fact.Model, fact.OSVersion = strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
			fact.Name = "Huawei " + fact.Model
			p.mu.Lock()
			p.facts[fact.ID] = fact
			p.mu.Unlock()
		}
		infos[i].Name, infos[i].Model, infos[i].OSVersion = fact.Name, fact.Model, fact.OSVersion
	}
	return infos, nil
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
			info.State = driver.StateOnline
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
			if info.State != driver.StateOnline {
				return nil, fmt.Errorf("native HarmonyOS control tools are unavailable: %w", driver.ErrUnsupported)
			}
			path, err := p.find()
			if err != nil {
				return nil, err
			}
			return &Device{info: info, path: path, run: p.run, labels: map[string]string{}}, nil
		}
	}
	return nil, driver.ErrNotFound
}

var _ driver.Provider = (*Provider)(nil)
