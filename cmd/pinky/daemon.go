package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/mcp"
	"github.com/DIYgod/pinky/internal/policy"
)

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("pinky daemon", flag.ContinueOnError)
	fake := fs.Bool("fake", false, "use scripted fake phones; their history goes to a separate folder")
	approvalTimeout := fs.Duration("approval-timeout", 60*time.Second, "how long the person has to answer an approval")
	idle := fs.Duration("idle-timeout", 10*time.Minute, "end a lease after this long without calls")
	noShots := fs.Bool("no-screenshots", false, "do not store before and after screenshots of each step")
	verbose := fs.Bool("v", false, "log debug messages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	dirs, err := api.DefaultDirs()
	if err != nil {
		return err
	}
	ln, err := api.Listen(dirs)
	if err != nil {
		var running *api.RunningError
		if errors.As(err, &running) {
			return err
		}
		return fmt.Errorf("listen on %s: %w", dirs.Socket(), err)
	}
	defer ln.Close()

	pol, err := policy.Load(dirs.Rules(), dirs.Apps())
	if err != nil {
		return fmt.Errorf("rules: %w", err)
	}
	var providers []driver.Provider
	dataDir := dirs.Data
	if *fake {
		providers, _ = demo.World()
		for cat, e := range demo.PolicyApps() {
			pol.AddApps(cat, e)
		}
		dataDir = filepath.Join(dirs.Data, "fake")
	} else {
		providers = realProviders(log)
		if len(providers) == 0 {
			log.Warn("no device provider could start (adb server, usbmuxd); use --fake to try Pinky with fake phones")
		}
	}

	g, err := gateway.New(gateway.Options{
		Providers: providers, DataDir: dataDir, Policy: pol, Logger: log,
		ApprovalTimeout: *approvalTimeout, IdleTimeout: *idle, NoScreenshots: *noShots,
		// Fake phones cannot hurt anything, so agents need no first approval
		// and socket clients may answer approvals (for scripted tests).
		TrustNewAgents: *fake,
	})
	if err != nil {
		return err
	}
	defer g.Close()

	tok, err := api.EnsureToken(dirs.TokenFile())
	if err != nil {
		return fmt.Errorf("agent token: %w", err)
	}
	srv := &api.Server{Gateway: g, MCP: mcp.New(g, version), Token: tok, Trusted: *fake, Version: version, Log: log}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("gateway ready", "socket", dirs.Socket(), "data", dataDir, "devices", len(g.Devices()), "fake", *fake, "pid", os.Getpid())
	err = srv.Serve(ctx, ln)
	log.Info("gateway stopping")
	return err
}
