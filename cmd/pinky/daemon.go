package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
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
	var c gatewayConfig
	c.flags(fs)
	verbose := fs.Bool("v", false, "log debug messages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c.Log = newLogger(os.Stderr, *verbose)

	rg, err := startGateway(c)
	if err != nil {
		return err
	}
	defer rg.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = rg.Serve(ctx)
	c.Log.Info("gateway stopping")
	return err
}

// gatewayConfig is what the daemon and the app share.
type gatewayConfig struct {
	Fake            bool
	ApprovalTimeout time.Duration
	IdleTimeout     time.Duration
	NoScreenshots   bool
	Log             *slog.Logger
}

func (c *gatewayConfig) flags(fs *flag.FlagSet) {
	fs.BoolVar(&c.Fake, "fake", false, "use scripted fake phones; their history goes to a separate folder")
	fs.DurationVar(&c.ApprovalTimeout, "approval-timeout", 60*time.Second, "how long the person has to answer an approval")
	fs.DurationVar(&c.IdleTimeout, "idle-timeout", 10*time.Minute, "end a lease after this long without calls")
	fs.BoolVar(&c.NoScreenshots, "no-screenshots", false, "do not store before and after screenshots of each step")
}

func newLogger(w *os.File, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// runningGateway is a gateway with its socket, ready to serve agents and
// clients.
type runningGateway struct {
	Dirs    api.Dirs
	DataDir string
	Policy  *policy.Engine
	G       *gateway.Gateway

	ln  net.Listener
	srv *api.Server
	log *slog.Logger
}

// startGateway takes the per-user socket, loads the rules and starts the
// gateway with real or fake phones.
func startGateway(c gatewayConfig) (*runningGateway, error) {
	log := c.Log
	dirs, err := api.DefaultDirs()
	if err != nil {
		return nil, err
	}
	ln, err := api.Listen(dirs)
	if err != nil {
		var running *api.RunningError
		if errors.As(err, &running) {
			return nil, err
		}
		return nil, fmt.Errorf("listen on %s: %w", dirs.Socket(), err)
	}
	pol, err := policy.Load(dirs.Rules(), dirs.Apps())
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("rules: %w", err)
	}
	var providers []driver.Provider
	dataDir := dirs.Data
	if c.Fake {
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
		ApprovalTimeout: c.ApprovalTimeout, IdleTimeout: c.IdleTimeout, NoScreenshots: c.NoScreenshots,
		// Fake phones cannot hurt anything, so agents need no first approval
		// and socket clients may answer approvals (for scripted tests).
		TrustNewAgents: c.Fake,
	})
	if err != nil {
		ln.Close()
		return nil, err
	}
	tok, err := api.EnsureToken(dirs.TokenFile())
	if err != nil {
		g.Close()
		ln.Close()
		return nil, fmt.Errorf("agent token: %w", err)
	}
	srv := &api.Server{Gateway: g, MCP: mcp.New(g, version), Token: tok, Trusted: c.Fake, Version: version, Log: log}
	log.Info("gateway ready", "socket", dirs.Socket(), "data", dataDir, "devices", len(g.Devices()), "fake", c.Fake, "pid", os.Getpid())
	return &runningGateway{Dirs: dirs, DataDir: dataDir, Policy: pol, G: g, ln: ln, srv: srv, log: log}, nil
}

// Serve answers agents and clients on the socket until ctx ends.
func (r *runningGateway) Serve(ctx context.Context) error {
	return r.srv.Serve(ctx, r.ln)
}

// Close stops the gateway and releases the socket.
func (r *runningGateway) Close() {
	r.G.Close()
	r.ln.Close()
}
