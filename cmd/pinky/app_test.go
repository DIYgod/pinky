package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/mcp"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/secret"
	"github.com/DIYgod/pinky/ui"
)

func TestSettingsReachTheGateway(t *testing.T) {
	dir := t.TempDir()
	pol, err := policy.Load(filepath.Join(dir, "rules.yaml"), filepath.Join(dir, "apps.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	providers, _ := demo.World()
	for cat, e := range demo.PolicyApps() {
		pol.AddApps(cat, e)
	}
	g, err := gateway.New(gateway.Options{Providers: providers, Policy: pol})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	rg := &runningGateway{Policy: pol, G: g}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	s := ui.DefaultSettings()
	gatewaySettings(&s, rg)
	if s.Preset != int(policy.PresetPayments) || !s.HideCodes || s.ReadOnly {
		t.Fatalf("defaults from the gateway: %+v", s)
	}
	for _, b := range s.BlockedApps {
		if b.Name == "Northbank" {
			t.Fatalf("the client's sample apps leaked into real settings: %+v", s.BlockedApps)
		}
	}

	next := s
	next.Preset, next.HideCodes, next.ReadOnly = int(policy.PresetCommits), false, true
	next.BlockedApps = append(append([]ui.BlockedApp(nil), s.BlockedApps...), ui.BlockedApp{Name: "Chatter"})
	applySettings(rg, s, next, log)
	if pol.Preset() != policy.PresetCommits || pol.HideCodes() || !pol.ReadOnly("") {
		t.Fatalf("after applying: preset %d, hide codes %v, read-only %v", pol.Preset(), pol.HideCodes(), pol.ReadOnly(""))
	}
	var again ui.Settings
	gatewaySettings(&again, rg)
	found := false
	for _, b := range again.BlockedApps {
		found = found || b.Name == "Chatter"
	}
	if !found {
		t.Fatalf("Chatter not blocked: %+v", again.BlockedApps)
	}

	// Unblocking it again.
	applySettings(rg, again, s, log)
	var third ui.Settings
	gatewaySettings(&third, rg)
	for _, b := range third.BlockedApps {
		if b.Name == "Chatter" {
			t.Fatalf("Chatter still blocked: %+v", third.BlockedApps)
		}
	}
}

func TestAppFromText(t *testing.T) {
	if a := appFromText(" com.dinely.app "); a.ID != "com.dinely.app" || a.Name != "" {
		t.Errorf("package: %+v", a)
	}
	if a := appFromText("Google Wallet"); a.Name != "Google Wallet" || a.ID != "" {
		t.Errorf("name: %+v", a)
	}
}

func TestRemoteAgentsAndRecordingSettings(t *testing.T) {
	dir := t.TempDir()
	providers, _ := demo.World()
	g, err := gateway.New(gateway.Options{Providers: providers, DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rg := &runningGateway{G: g, Policy: g.Policy(), Remote: newRemoteAgents(mcp.New(g, "test"), "tok", log)}
	defer rg.Remote.Close()

	s := ui.DefaultSettings()
	startSettings(rg, s, log)
	if g.Retention() != 30 || rg.Remote.URL() != "" {
		t.Fatalf("start: retention %d, remote %q", g.Retention(), rg.Remote.URL())
	}
	next := s
	next.KeepDays, next.RemoteAgents, next.RemoteAddr = 7, true, "127.0.0.1:0"
	applySettings(rg, s, next, log)
	if g.Retention() != 7 {
		t.Fatalf("retention %d", g.Retention())
	}
	url := rg.Remote.URL()
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/mcp") {
		t.Fatalf("remote url %q", url)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	off := next
	off.RemoteAgents = false
	applySettings(rg, next, off, log)
	if rg.Remote.URL() != "" {
		t.Fatal("remote agents still on")
	}
	if _, err := http.Get(url); err == nil {
		t.Fatal("still listening")
	}
}

func TestPINNeedsTheSetting(t *testing.T) {
	ctx := context.Background()
	var reasons []string
	store := &secret.Memory{Confirm: func(reason string) error { reasons = append(reasons, reason); return nil }}
	p := pinSource{store: store}
	store.Save(secret.PINAccount("pixel"), "4711")
	if _, err := p.pin(ctx, "pixel"); err == nil || !strings.Contains(err.Error(), "off in Settings") {
		t.Fatalf("PIN with the setting off: %v", err)
	}
	p.on.Store(true)
	if pin, err := p.pin(ctx, "pixel"); err != nil || pin != "4711" {
		t.Fatalf("PIN %q, %v", pin, err)
	}
	if len(reasons) != 1 || reasons[0] != "unlock pixel" {
		t.Fatalf("confirmed for %q", reasons)
	}
	// Nothing saved: no prompt at all.
	if _, err := p.pin(ctx, "ipad"); !errors.Is(err, secret.ErrNotFound) || len(reasons) != 1 {
		t.Fatalf("no saved PIN: %v, %d prompts", err, len(reasons))
	}
	store.Confirm = func(string) error { return secret.ErrCancelled }
	if _, err := p.pin(ctx, "pixel"); !errors.Is(err, secret.ErrCancelled) {
		t.Fatalf("cancelled: %v", err)
	}
}
