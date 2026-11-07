package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/DIYgod/pinky/internal/api"
)

// runMCP is the stdio shim agents launch. It forwards messages between
// stdio and the gateway's socket, where the MCP server runs, starts the
// gateway first when none is running, and waits for it when the app
// restarts for an update.
func runMCP(args []string) error {
	fs := flag.NewFlagSet("pinky mcp", flag.ContinueOnError)
	fake := fs.Bool("fake", false, "when no gateway runs, start one with fake phones")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dirs, err := api.DefaultDirs()
	if err != nil {
		return err
	}
	conn, err := api.Dial(dirs)
	if err != nil {
		if err := startDaemon(dirs, *fake); err != nil {
			return fmt.Errorf("start the gateway: %w", err)
		}
		if conn, err = waitForGateway(dirs, 20*time.Second); err != nil {
			return fmt.Errorf("the gateway did not start (see %s): %w", dirs.DaemonLog(), err)
		}
	}
	defer conn.Close()
	tok, err := api.EnsureToken(dirs.TokenFile())
	if err != nil {
		return err
	}
	br, err := greetMCP(conn, tok)
	if err != nil {
		return err
	}
	// The session survives the app restarting for an update (shimconn.go).
	return newShimSession(dirs, tok, os.Stdout).run(conn, br, os.Stdin)
}

// startDaemon starts the gateway in the background, detached from this
// process, logging to the data folder. On macOS that is the desktop app,
// since only the app can answer approvals for real phones; elsewhere, and
// with fake phones, it is `pinky daemon`.
func startDaemon(dirs api.Dirs, fake bool) error {
	if err := dirs.Ensure(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(dirs.DaemonLog(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	args := []string{"daemon"}
	if fake {
		args = append(args, "--fake")
	} else if runtime.GOOS == "darwin" {
		args = []string{"app", "--background"}
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func waitForGateway(dirs api.Dirs, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := api.Dial(dirs)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}
