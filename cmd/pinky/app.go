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

	"github.com/DIYgod/pinky"
	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/internal/secret"
	"github.com/DIYgod/pinky/internal/update"
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
	// Opened from its icon, Pinky has no terminal for its errors: say in a
	// dialog why it could not start. Started in the background, an agent's
	// `pinky mcp` reads them from the log.
	var logPath string
	failed := func(err error) error {
		if !*background {
			showStartError(err, logPath)
		}
		return err
	}
	dirs, err := api.DefaultDirs()
	if err != nil {
		return failed(err)
	}
	if err := dirs.Ensure(); err != nil {
		return failed(err)
	}
	logPath = filepath.Join(dirs.Data, "pinky.log")
	logFile, err := openLog(logPath)
	if err != nil {
		return failed(err)
	}
	defer logFile.Close()
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	c.Log = slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, logFile), &slog.HandlerOptions{Level: level}))
	secret.SetLogger(c.Log)
	crashes := captureCrashes(dirs, "app", c.Log)
	defer crashes.Stop()
	c.Crashes = crashes

	settingsPath := filepath.Join(dirs.Config, "settings.json")
	settings := loadSettings(settingsPath, c.Log)
	var unlock pinSource
	unlock.on.Store(settings.UnlockFromComputer)
	c.PIN = unlock.pin
	// Set when `pinky daemon` takes this app's place (below); it starts
	// once the gateway here has let go of the socket.
	var daemon func()
	defer func() {
		if daemon != nil {
			daemon()
		}
	}()
	rg, err := startGateway(c)
	if running := (*api.RunningError)(nil); errors.As(err, &running) {
		// An agent's `pinky daemon`, or another Pinky window.
		rg, err = takeOver(c, dirs, *background, startGateway)
		if errors.Is(err, errShownOther) {
			c.Log.Info("Pinky is already open: showed its window")
			return nil
		}
	}
	if err != nil {
		return failed(err)
	}
	// Once the gateway has closed (deferred calls run last to first).
	var stopADB atomic.Bool
	stopADB.Store(settings.StopADBOnQuit)
	var restarting bool
	defer func() { stopADBAtQuit(stopADB.Load(), c.Fake, restarting, c.Log) }()
	defer rg.Close()
	rg.srv.Kind = api.KindApp
	// Opening Pinky again shows this window (takeOver), once the client
	// is there to show it.
	var show atomic.Pointer[func()]
	rg.srv.Show = func() {
		if f := show.Load(); f != nil {
			(*f)()
		}
	}
	unlock.names = rg.G
	startSettings(rg, settings, c.Log)
	upd := newUpdater(dirs, c.Log)
	swapped := startedUpdate(upd, c.Log)
	go upd.Where() // codesign takes a moment; look while the window opens

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
	ready := false
	err = ui.Run(clientAPI(rg, tel, c.Fake), ui.Options{
		BindShow: func(f func()) { show.Store(&f) },
		Version:  version,
		Theme:    *theme,
		Hidden:   *background,
		Settings: &settings,
		OnSettings: func(old, s ui.Settings) {
			applySettings(rg, old, s, c.Log)
			applyTelemetry(tel, s)
			unlock.on.Store(s.UnlockFromComputer)
			stopADB.Store(s.StopADBOnQuit)
			if err := saveSettings(settingsPath, s); err != nil {
				c.Log.Warn("save settings", "err", err)
			}
		},
		GatewaySettings: func(since int) (ui.Settings, int, bool) { return policySettings(rg, since) },
		RulesFile:       dirs.Rules(),
		AgentToken:      rg.srv.Token,
		Telemetry:       tel,
		Updates:         upd,
		Changelog:       pinky.Changelog,
		AgentSetup:      agentSetup(dirs),
		// For phones that refuse installs over USB.
		CompanionAPK:  bundledCompanion,
		RemoteTailnet: remoteTailnet,
		// What's new in the app's language.
		ChangelogTranslations: pinky.ChangelogTranslations,
		// The window is up: an update that just swapped this version in
		// can drop the old one.
		OnReady: func(*mygo.Window) {
			ready = true
			if err := swapped.Confirm(); err != nil {
				c.Log.Warn("update: confirm", "err", err)
			}
		},
	})
	if err != nil && *background && !ready {
		// No display after all, or the app's libraries are missing. The
		// gateway was up for a moment, though, and answered the agent whose
		// `pinky mcp` started this app: too late for the shim to start the
		// daemon instead (launchGateway). So the app leaves the daemon
		// behind, and the restart marker keeps the agent's session, as it
		// does when the app takes over from a daemon (takeOver).
		c.Log.Warn("Pinky cannot show itself here; pinky daemon takes its place", "err", err)
		upd.MarkRestart()
		daemon = func() {
			mode := daemonMode
			if c.Fake {
				mode = fakeMode
			}
			if _, err := startDaemon(dirs, mode); err != nil {
				c.Log.Error("start pinky daemon", "err", err)
				update.RestartDone(filepath.Dir(upd.StatePath))
			}
		}
	}
	// Read after MarkRestart above, so a handover keeps adb for the daemon.
	restarting = update.Restarting(dirs.Config)
	return err
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
// apps.yaml, which are the source of truth for it. A file without a preset
// shows none of the three.
func gatewaySettings(s *ui.Settings, rg *runningGateway) {
	if p := rg.Policy.Preset(); p >= policy.PresetNone && p <= policy.PresetEvery {
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
		s.BlockedApps = append(s.BlockedApps, ui.BlockedApp{Name: name, ID: b.ID, Kind: policy.CategoryLabel(b.Category)})
	}
}

// policySettings is gatewaySettings for Settings to read again after hand
// edits of the rules files: it reads the gateway only when the policy's
// version is no longer since, which saves listing the phones' apps every
// few seconds.
func policySettings(rg *runningGateway, since int) (ui.Settings, int, bool) {
	v := rg.Policy.Version()
	if v == since {
		return ui.Settings{}, v, false
	}
	var s ui.Settings
	gatewaySettings(&s, rg)
	return s, v, true
}

// startSettings applies what the gateway takes from Settings at start:
// recording, retention, iPhones over Wi-Fi and remote agents.
func startSettings(rg *runningGateway, s ui.Settings, log *slog.Logger) {
	rg.G.SetRecording(s.Record)
	rg.G.SetRetention(s.KeepDays)
	rg.G.SetWiFi(s.IOSWiFi)
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
	if s.IOSWiFi != old.IOSWiFi {
		rg.G.SetWiFi(s.IOSWiFi)
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
	was := map[string]ui.BlockedApp{}
	for _, b := range old.BlockedApps {
		was[b.Name] = b
	}
	now := map[string]bool{}
	for _, b := range s.BlockedApps {
		now[b.Name] = true
		if _, ok := was[b.Name]; !ok {
			warn("block "+b.Name, rg.G.SetAppBlocked(blockedApp(b), true))
		}
	}
	for name, b := range was {
		if !now[name] {
			warn("unblock "+name, rg.G.SetAppBlocked(blockedApp(b), false))
		}
	}
}

// blockedApp is the app a row of Settings › Safety stands for: by its id
// and name when the gateway listed it (a banking app the bundled table
// blocks by bundle id, such as Wise, stays blocked when only its name is
// unblocked), else by what the person typed.
func blockedApp(b ui.BlockedApp) screen.App {
	if b.ID == "" {
		return appFromText(b.Name)
	}
	a := screen.App{ID: b.ID}
	if b.Name != b.ID {
		a.Name = b.Name
	}
	return a
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
