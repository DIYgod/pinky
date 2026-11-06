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

	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/ui"
)

// runApp runs the desktop app: the gateway, the socket agents connect to,
// and the client, in one process. Approvals are answered only here.
func runApp(args []string) error {
	fs := flag.NewFlagSet("pinky", flag.ContinueOnError)
	var c gatewayConfig
	c.flags(fs)
	theme := fs.String("theme", "", `"light" or "dark"; empty follows the system`)
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
	settings := loadSettings(settingsPath, rg.Policy, c.Log)
	if settings.ReadOnly {
		rg.G.SetReadOnly("", true)
	}
	return ui.Run(rg.G, ui.Options{
		Version:  version,
		Theme:    *theme,
		Settings: &settings,
		OnSettings: func(s ui.Settings) {
			rg.G.SetReadOnly("", s.ReadOnly)
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
// Blocked apps always come from the rules, which decide what is blocked.
func loadSettings(path string, pol *policy.Engine, log *slog.Logger) ui.Settings {
	s := ui.DefaultSettings()
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &s); err != nil {
			log.Warn("settings file unreadable; using the defaults", "path", path, "err", err)
			s = ui.DefaultSettings()
		}
	}
	s.BlockedApps = blockedApps(pol)
	return s
}

func saveSettings(path string, s ui.Settings) error {
	s.BlockedApps = nil // kept in the rules
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

// blockedApps lists the rules that block whole apps or categories, which is
// what Settings shows as blocked apps.
func blockedApps(pol *policy.Engine) []ui.BlockedApp {
	var out []ui.BlockedApp
	for _, r := range pol.Rules() {
		if r.Decision != policy.Deny || len(r.Match.Action) > 0 || len(r.Match.Label) > 0 {
			continue
		}
		for _, cat := range r.Match.Category {
			out = append(out, ui.BlockedApp{Name: r.Name, Kind: categoryTitle(cat)})
		}
		for _, app := range r.Match.App {
			out = append(out, ui.BlockedApp{Name: app, Kind: r.Name})
		}
	}
	return out
}

func categoryTitle(cat string) string {
	switch cat {
	case "finance":
		return "Banking, payments and wallets"
	case "passwords":
		return "Password managers"
	case "messaging":
		return "Messaging"
	case "social":
		return "Social"
	case "stores":
		return "App stores"
	case "browsers":
		return "Browsers"
	}
	return cat
}
