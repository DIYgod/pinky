package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/ui"
)

// runApp runs the desktop app: the gateway, the socket agents connect to,
// and the client, in one process. Approvals are answered only here.
func runApp(args []string) error {
	fs := flag.NewFlagSet("pinky", flag.ContinueOnError)
	var c gatewayConfig
	c.flags(fs)
	theme := fs.String("theme", "", `"light" or "dark"; empty follows the system`)
	background := fs.Bool("background", false, "start in the menu bar without a window; it opens when something needs you")
	verbose := fs.Bool("verbose", false, "log debug messages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dirs, err := api.DefaultDirs()
	if err != nil {
		return err
	}
	if err := dirs.Ensure(); err != nil {
		return err
	}
	logFile, err := openLog(filepath.Join(dirs.Data, "pinky.log"))
	if err != nil {
		return err
	}
	defer logFile.Close()
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	c.Log = slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, logFile), &slog.HandlerOptions{Level: level}))

	rg, err := startGateway(c)
	if err != nil {
		var running *api.RunningError
		if errors.As(err, &running) {
			return fmt.Errorf("%w; quit the other Pinky (or `pinky daemon`) first", err)
		}
		return err
	}
	defer rg.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := rg.Serve(ctx); err != nil && ctx.Err() == nil {
			c.Log.Error("agent socket stopped", "err", err)
		}
	}()

	settingsPath := filepath.Join(dirs.Config, "settings.json")
	settings := loadSettings(settingsPath, c.Log)
	gatewaySettings(&settings, rg)
	current := settings
	return ui.Run(rg.G, ui.Options{
		Version:  version,
		Theme:    *theme,
		Hidden:   *background,
		Settings: &settings,
		OnSettings: func(s ui.Settings) {
			applySettings(rg, current, s, c.Log)
			current = s
			if err := saveSettings(settingsPath, s); err != nil {
				c.Log.Warn("save settings", "err", err)
			}
		},
		RulesFile: dirs.Rules(),
	})
}

// openLog opens the app's log for appending, starting over once it passes
// 10 MB.
func openLog(path string) (*os.File, error) {
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > 10<<20 {
		flags |= os.O_TRUNC
	}
	return os.OpenFile(path, flags, 0o600)
}

// loadSettings reads the client's settings, or starts from the defaults.
// What the gateway keeps (the preset, hiding codes, read-only mode and
// blocked apps) comes from the gateway instead; see gatewaySettings.
func loadSettings(path string, log *slog.Logger) ui.Settings {
	s := ui.DefaultSettings()
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &s); err != nil {
			log.Warn("settings file unreadable; using the defaults", "path", path, "err", err)
			s = ui.DefaultSettings()
		}
	}
	return s
}

func saveSettings(path string, s ui.Settings) error {
	s.BlockedApps = nil // kept in the rules and apps files
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// gatewaySettings fills in what the policy keeps in rules.yaml and
// apps.yaml, which are the source of truth for it.
func gatewaySettings(s *ui.Settings, rg *runningGateway) {
	if p := rg.Policy.Preset(); p >= policy.PresetCommits && p <= policy.PresetEvery {
		s.Preset = int(p)
	}
	s.HideCodes = rg.Policy.HideCodes()
	s.ReadOnly = rg.Policy.ReadOnly("")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.BlockedApps = nil
	for _, b := range rg.G.BlockedApps(ctx) {
		name := b.Name
		if name == "" {
			name = b.ID
		}
		s.BlockedApps = append(s.BlockedApps, ui.BlockedApp{Name: name, Kind: policy.CategoryLabel(b.Category)})
	}
}

// applySettings passes what changed in Settings to the gateway.
func applySettings(rg *runningGateway, old, s ui.Settings, log *slog.Logger) {
	warn := func(what string, err error) {
		if err != nil {
			log.Warn("settings: "+what, "err", err)
		}
	}
	if s.Preset != old.Preset && s.Preset >= int(policy.PresetCommits) && s.Preset <= int(policy.PresetEvery) {
		warn("preset", rg.Policy.SetPreset(policy.Preset(s.Preset)))
	}
	if s.HideCodes != old.HideCodes {
		warn("hide codes", rg.Policy.SetHideCodes(s.HideCodes))
	}
	if s.ReadOnly != old.ReadOnly {
		warn("read-only", rg.G.SetReadOnly("", s.ReadOnly))
	}
	was := map[string]bool{}
	for _, b := range old.BlockedApps {
		was[b.Name] = true
	}
	now := map[string]bool{}
	for _, b := range s.BlockedApps {
		now[b.Name] = true
		if !was[b.Name] {
			warn("block "+b.Name, rg.G.SetAppBlocked(appFromText(b.Name), true))
		}
	}
	for name := range was {
		if !now[name] {
			warn("unblock "+name, rg.G.SetAppBlocked(appFromText(name), false))
		}
	}
}

// appFromText reads what the person typed as an app: a package or bundle
// id when it looks like one, else a display name.
func appFromText(text string) screen.App {
	text = strings.TrimSpace(text)
	if strings.Contains(text, ".") && !strings.ContainsAny(text, " \t") {
		return screen.App{ID: text}
	}
	return screen.App{Name: text}
}
