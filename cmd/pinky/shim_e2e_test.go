// These tests stop daemons with SIGTERM, which Windows lacks.

//go:build !windows

package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/update"
)

// startFakeDaemon runs `pinky daemon --fake` until stopped or the test
// ends.
func startFakeDaemon(t *testing.T, bin, home string) (stop func()) {
	t.Helper()
	cmd := exec.Command(bin, "daemon", "--fake", "--no-update-check")
	cmd.Env = env(home)
	var logs bytes.Buffer
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			<-exited
		}
		if t.Failed() {
			t.Logf("daemon log:\n%s", logs.String())
		}
	}
	t.Cleanup(stop)
	waitSocket(t, dirsFor(t, home))
	return stop
}

// TestShimReconnectsAfterAnUpdate: when the gateway goes away because Pinky
// restarts for an update, `pinky mcp` waits for the new one and the agent's
// session goes on; when it goes away otherwise, the session ends as
// before.
func TestShimReconnectsAfterAnUpdate(t *testing.T) {
	bin := buildPinky(t)
	home := pinkyHome(t)
	d := dirsFor(t, home)
	stop := startFakeDaemon(t, bin, home)

	agent := startMCP(t, bin, home, "claude-code")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := agent.cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	text, isErr := agent.call(t, "phone_connect", map[string]any{"device": "Pixel 9 (fake)", "task": "Before the update"})
	if isErr {
		t.Fatalf("connect: %s", text)
	}

	// The app marks its restart before it quits for an update.
	(&update.Updater{StatePath: filepath.Join(d.Config, "update.json")}).MarkRestart()
	stop()
	restarted := make(chan func())
	go func() {
		time.Sleep(2 * time.Second) // the new version takes a moment
		restarted <- startFakeDaemon(t, bin, home)
	}()
	tools, err := agent.cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("the session did not survive the restart: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools after the restart")
	}
	stop2 := <-restarted
	text, isErr = agent.call(t, "phone_connect", map[string]any{"device": "Pixel 9 (fake)", "task": "After the update"})
	if isErr || !strings.Contains(text, "Pixel 9 (fake)") {
		t.Fatalf("connect after the restart: %s", text)
	}
	if update.Restarting(d.Config) {
		t.Error("the new gateway left the restart marker")
	}

	// Without the marker the session ends with the gateway.
	stop2()
	done := make(chan error, 1)
	go func() {
		_, err := agent.cs.ListTools(context.Background(), nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("the session went on without a gateway")
		}
	case <-time.After(20 * time.Second):
		t.Error("the shim waited for a gateway that quit for good")
	}
}
