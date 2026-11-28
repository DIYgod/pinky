package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/DIYgod/pinky/internal/pilot"
	"github.com/DIYgod/pinky/internal/secret"
)

func TestFirstService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	withKey := func(name string) secret.Store {
		s := &secret.Memory{}
		if err := s.Save(secret.ModelKeyAccount(name), "k"); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, c := range []struct {
		name    string
		file    string // the settings file; "-" for none
		store   secret.Store
		service string
	}{
		{"new computer", "-", &secret.Memory{}, pilot.FirstService},
		{"no keychain", "-", nil, pilot.FirstService},
		// A key saved for Anthropic before a setting ever changed.
		{"Anthropic key, no settings", "-", withKey(pilot.Anthropic), pilot.Anthropic},
		// Another service's key without settings leaves no trace of it,
		// since choosing that service saved the settings.
		{"other key, no settings", "-", withKey("deepseek"), pilot.FirstService},
		{"settings of today", `{"PilotService": "", "PilotProvider": "anthropic"}`, &secret.Memory{}, pilot.Anthropic},
		{"settings of before there were services", `{"PilotProvider": "anthropic", "PilotAnthropicModel": "claude-sonnet-5-5"}`, &secret.Memory{}, pilot.Anthropic},
		{"settings without Ask Pinky", `{"KeepDays": 7}`, &secret.Memory{}, pilot.Anthropic},
		{"OpenAI of before", `{"PilotProvider": "openai"}`, &secret.Memory{}, pilot.OpenAI},
		{"a service chosen", `{"PilotService": "deepseek"}`, &secret.Memory{}, "deepseek"},
		{"unreadable settings", `{`, &secret.Memory{}, pilot.Anthropic},
	} {
		path := filepath.Join(t.TempDir(), "settings.json")
		if c.file != "-" {
			if err := os.WriteFile(path, []byte(c.file), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		s := firstService(loadSettings(path, log), path, c.store)
		cfg := s.PilotConfig()
		if cfg.Service != c.service {
			t.Errorf("%s: service %q, want %q", c.name, cfg.Service, c.service)
		}
		if c.service == pilot.FirstService {
			svc, _ := pilot.ServiceByID(pilot.FirstService)
			if cfg.Provider != pilot.OpenAI || cfg.BaseURL != svc.BaseURL || cfg.Model != svc.Models[0].ID || cfg.KeyName() != pilot.FirstService {
				t.Errorf("%s: %+v", c.name, cfg)
			}
		}
		if _, err := os.Stat(path); c.file == "-" && err == nil {
			t.Errorf("%s: a settings file was written", c.name)
		}
	}
	// What the person chooses afterwards is saved and stays.
	path := filepath.Join(t.TempDir(), "settings.json")
	s := firstService(loadSettings(path, log), path, &secret.Memory{})
	s.PilotService = pilot.Anthropic
	if err := saveSettings(path, s); err != nil {
		t.Fatal(err)
	}
	if got := firstService(loadSettings(path, log), path, &secret.Memory{}).PilotConfig().Service; got != pilot.Anthropic {
		t.Errorf("after choosing Anthropic: %s", got)
	}
}
