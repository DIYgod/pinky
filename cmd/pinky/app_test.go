package main

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/policy"
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
	if s.Preset != 1 || !s.HideCodes || s.ReadOnly {
		t.Fatalf("defaults from the gateway: %+v", s)
	}
	for _, b := range s.BlockedApps {
		if b.Name == "Northbank" {
			t.Fatalf("the client's sample apps leaked into real settings: %+v", s.BlockedApps)
		}
	}

	next := s
	next.Preset, next.HideCodes, next.ReadOnly = 2, false, true
	next.BlockedApps = append(append([]ui.BlockedApp(nil), s.BlockedApps...), ui.BlockedApp{Name: "Chatter"})
	applySettings(rg, s, next, log)
	if pol.Preset() != policy.PresetPayments || pol.HideCodes() || !pol.ReadOnly("") {
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
