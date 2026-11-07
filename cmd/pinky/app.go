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
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/internal/secret"
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
	crashes := captureCrashes(dirs, "app", c.Log)
	defer crashes.Stop()
	c.Crashes = crashes

	settingsPath := filepath.Join(dirs.Config, "settings.json")
	settings := loadSettings(settingsPath, c.Log)
	var unlock pinSource
	unlock.on.Store(settings.UnlockFromComputer)
	c.PIN = unlock.pin
	rg, err := startGateway(c)
	if err != nil {
		var running *api.RunningError
		if errors.As(err, &running) {
			return fmt.Errorf("%w; quit the other Pinky (or `pinky daemon`) first", err)
		}
		return err
	}
	defer rg.Close()
	unlock.names = rg.G
	startSettings(rg, settings, c.Log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tel := startTelemetry(ctx, rg, crashes, settings, "app", c.Fake, c.Log)
	defer tel.Close()
	// `pinky quit` quits the app as its menu would.
	rg.srv.Quit = func() { mygo.App.Quit() }
	go func() {
		if err := rg.Serve(ctx); err != nil && ctx.Err() == nil {
			c.Log.Error("agent socket stopped", "err", err)
		}
	}()

	gatewaySettings(&settings, rg)
	current := settings
	return ui.Run(clientAPI(rg, tel, c.Fake), ui.Options{
		Version:  version,
		Theme:    *theme,
		Hidden:   *background,
		Settings: &settings,
		OnSettings: func(s ui.Settings) {
			applySettings(rg, current, s, c.Log)
			applyTelemetry(tel, s, c.Fake)
			unlock.on.Store(s.UnlockFromComputer)
			current = s
			if err := saveSettings(settingsPath, s); err != nil {
				c.Log.Warn("save settings", "err", err)
			}
		},
		RulesFile:  dirs.Rules(),
		AgentToken: rg.srv.Token,
		Telemetry:  tel,
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

// startSettings applies what the gateway takes from Settings at start:
// recording, retention and remote agents.
func startSettings(rg *runningGateway, s ui.Settings, log *slog.Logger) {
	rg.G.SetRecording(s.Record)
	rg.G.SetRetention(s.KeepDays)
	if s.RemoteAgents {
		if err := rg.Remote.Set(true, remoteAddr(s)); err != nil {
			log.Warn("settings: remote agents", "err", err)
		}
	}
}

func remoteAddr(s ui.Settings) string {
	if a := strings.TrimSpace(s.RemoteAddr); a != "" {
		return a
	}
	return ui.DefaultRemoteAddr
}

// pinSource hands out saved PINs from the system keychain, which asks the
// person to confirm (Touch ID), and only while Settings › Unlock from this
// computer is on.
type pinSource struct {
	on    atomic.Bool
	names interface{ Devices() []gateway.DeviceView }
	store secret.Store // nil: the system keychain
}

func (p *pinSource) pin(ctx context.Context, deviceID string) (string, error) {
	if !p.on.Load() {
		return "", errors.New("unlocking from this computer is off in Settings; unlock on the phone")
	}
	name := deviceID
	if p.names != nil {
		for _, d := range p.names.Devices() {
			if d.Info.ID == deviceID && d.Info.Name != "" {
				name = d.Info.Name
			}
		}
	}
	store := p.store
	if store == nil {
		store = secret.System()
	}
	account := secret.PINAccount(deviceID)
	// Without a saved PIN there is nothing to confirm: say so without a
	// Touch ID prompt.
	if !store.Has(account) {
		return "", fmt.Errorf("no PIN is saved for %s; save it in the phone's settings in Pinky: %w", name, secret.ErrNotFound)
	}
	return store.Reveal(ctx, account, "unlock "+name)
}

// applySettings passes what changed in Settings to the gateway.
func applySettings(rg *runningGateway, old, s ui.Settings, log *slog.Logger) {
	warn := func(what string, err error) {
		if err != nil {
			log.Warn("settings: "+what, "err", err)
		}
	}
	if s.Record != old.Record {
		rg.G.SetRecording(s.Record)
	}
	if s.KeepDays != old.KeepDays {
		rg.G.SetRetention(s.KeepDays)
	}
	if rg.Remote != nil && (s.RemoteAgents != old.RemoteAgents || remoteAddr(s) != remoteAddr(old)) {
		warn("remote agents", rg.Remote.Set(s.RemoteAgents, remoteAddr(s)))
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
