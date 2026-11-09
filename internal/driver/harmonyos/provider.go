package harmonyos

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

const idPrefix = "harmonyos:"

var serialPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Provider discovers authorized USB devices through an explicitly configured HDC.
type Provider struct {
	hdc   string
	mu    sync.Mutex
	known map[string]driver.Info
}

func NewProvider(hdc string) *Provider {
	return &Provider{hdc: hdc, known: make(map[string]driver.Info)}
}

func (p *Provider) Platform() driver.Platform { return driver.HarmonyOS }

func (p *Provider) List(ctx context.Context) ([]driver.Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out, err := p.command(ctx, "list", "targets", "-v")
	if err != nil {
		return nil, err
	}
	var devices []driver.Info
	present := make(map[string]driver.Info)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "USB" || !serialPattern.MatchString(fields[0]) {
			continue
		}
		serial := fields[0]
		id := idPrefix + serial
		switch fields[2] {
		case "Connected":
			info, ok := p.known[id]
			// USB enumeration can succeed before the HDC handshake.
			if len(fields) > 3 && fields[3] == "unknown..." {
				if ok {
					present[id] = info
				}
				continue
			}
			if !ok {
				info, err = p.readInfo(ctx, serial)
				if err != nil {
					continue
				}
			}
			present[id] = info
			devices = append(devices, info)
		case "Unauthorized":
			devices = append(devices, driver.Info{ID: id, Name: "HarmonyOS", Platform: driver.HarmonyOS,
				Connection: "usb", Backend: "uitest", State: driver.StateUnauthorized, Setup: "authorize"})
		}
	}
	p.known = present
	return devices, nil
}

func (p *Provider) readInfo(ctx context.Context, serial string) (driver.Info, error) {
	out, err := p.shell(ctx, serial, "param get const.product.name; param get const.product.model; param get const.ohos.fullname")
	if err != nil {
		return driver.Info{}, err
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(fields) != 3 {
		return driver.Info{}, fmt.Errorf("HDC did not report the device name, model and OS version")
	}
	name, model, version := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1]), strings.TrimSpace(fields[2])
	if name == "" || model == "" || version == "" {
		return driver.Info{}, fmt.Errorf("HDC returned incomplete device details")
	}
	return driver.Info{ID: idPrefix + serial, Name: name, Model: model, OSVersion: version, Connection: "usb",
		Platform: driver.HarmonyOS, Backend: "uitest", State: driver.StateOnline}, nil
}

func (p *Provider) Open(ctx context.Context, id string) (driver.Device, error) {
	p.mu.Lock()
	info, ok := p.known[id]
	p.mu.Unlock()
	if !ok {
		return nil, driver.ErrOffline
	}
	return &device{provider: p, serial: strings.TrimPrefix(id, idPrefix), info: info}, nil
}

func (p *Provider) shell(ctx context.Context, serial, command string) ([]byte, error) {
	return p.command(ctx, "-t", serial, "shell", command)
}

func (p *Provider) command(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.hdc, args...)
	cmd.Dir = filepath.Dir(p.hdc)
	hideConsole(cmd)
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	for line := range bytes.SplitSeq(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("[Fail]")) {
			if bytes.Contains(line, []byte("[E000004]")) || bytes.Contains(line, []byte("[E001003]")) ||
				bytes.Contains(line, []byte("[E001005]")) {
				return nil, fmt.Errorf("the HarmonyOS USB debugging session is unavailable: %w", driver.ErrOffline)
			}
			return nil, fmt.Errorf("HDC reported a command failure")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("HDC command failed: %w", err)
	}
	return out, nil
}
