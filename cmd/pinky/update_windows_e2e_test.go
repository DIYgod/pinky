//go:build e2e && windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sys/windows/registry"

	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/update"
)

// TestWindowsUpdate updates a Pinky that its installer installed, as on a
// person's computer. It installs the oldest of three installers of e2e
// builds quietly, into a temporary folder (which Settings › Apps then
// names), serves the next one from a local feed and leaves Pinky alone in
// the tray. Pinky installs the update by itself: its helper runs the
// installer quietly, starts the new version, which shows What's new, and
// removes the old files. Then the feed offers the newest, which exits as it
// starts (PINKY_E2E_CRASH_VERSION): the helper puts the version before it
// back, files and Settings › Apps entry, and that version does not try the
// broken one again by itself.
//
// It installs Pinky for the current user and uninstalls it at the end, so
// it is for CI's Windows runner (.github/workflows/update-windows.yml):
//
//	PINKY_E2E_SETUPS=<a folder with three pinky-setup-<version>-amd64.exe> \
//	  go test -tags e2e -run TestWindowsUpdate -v -timeout 30m ./cmd/pinky/
func TestWindowsUpdate(t *testing.T) {
	setups := os.Getenv("PINKY_E2E_SETUPS")
	if setups == "" {
		t.Skip("PINKY_E2E_SETUPS (installers of three e2e builds) not set; this test installs Pinky for the current user")
	}
	names, _ := filepath.Glob(filepath.Join(setups, "pinky-setup-*-amd64.exe"))
	setup := map[string]string{}
	var versions []string
	for _, n := range names {
		v := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(n), "pinky-setup-"), "-amd64.exe")
		setup[v] = n
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return update.Compare(versions[i], versions[j]) < 0 })
	if len(versions) != 3 {
		t.Fatalf("want three installers in %s, found %v", setups, versions)
	}
	oldV, newV, brokenV := versions[0], versions[1], versions[2]

	tmp, err := os.MkdirTemp("", "pku-")
	if err != nil {
		t.Fatal(err)
	}
	programs := filepath.Join(tmp, "Programs")
	dir := filepath.Join(programs, "Pinky")
	exe := filepath.Join(dir, "Pinky.exe")
	home := filepath.Join(tmp, "h")
	sock := filepath.Join(tmp, "ui.sock")
	t.Setenv(api.HomeEnv, home)
	dirs, err := api.DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	feed := newTestFeed(t)
	// Pinky's environment, and the one of commands that must not take
	// the app's automation socket (every e2e build of Pinky.exe would).
	base := append(os.Environ(), api.HomeEnv+"="+home, update.FeedEnv+"="+feed.srv.URL+"/latest.json",
		"PINKY_E2E_CRASH_VERSION="+brokenV, "DO_NOT_TRACK=1")
	appEnv := append(append([]string(nil), base...), "MYGO_AUTOMATION="+sock)

	var app *exec.Cmd
	t.Cleanup(func() {
		quitPinky(dirs)
		if app != nil && app.Process != nil {
			app.Process.Kill()
		}
		if t.Failed() {
			for _, name := range []string{filepath.Join("h", "data", "pinky.log"), filepath.Join("h", "data", "update.log"), "app.log"} {
				if b, err := os.ReadFile(filepath.Join(tmp, name)); err == nil {
					t.Logf("%s:\n%s", name, tail(string(b), 6000))
				}
			}
		}
		uninstall(t, dir)
		os.RemoveAll(tmp)
	})

	// The older version, installed quietly with its installer.
	installSetup(t, setup[oldV], dir)
	if got := uninstallValue(t, "InstallLocation"); !strings.EqualFold(filepath.Clean(got), filepath.Clean(dir)) {
		t.Fatalf("Settings › Apps says Pinky is in %q, not %s", got, dir)
	}
	if got := uninstallValue(t, "DisplayVersion"); got != oldV {
		t.Fatalf("Settings › Apps says version %q", got)
	}

	// Left alone in the tray, it finds the next version and installs it.
	feed.offer(t, newV, setup[newV])
	start := func() <-chan error {
		app = exec.Command(exe, "--fake", "--background")
		app.Env = appEnv
		logf, _ := os.OpenFile(filepath.Join(tmp, "app.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		app.Stdout, app.Stderr = logf, logf
		if err := app.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan error, 1)
		go func() { exited <- app.Wait(); logf.Close() }()
		return exited
	}
	exited := start()
	waitFor(t, "the old version's gateway", 60*time.Second, func() bool { return gatewayVersion(dirs) == oldV })

	// An agent connected through `pinky mcp`, which runs from the
	// Pinky.exe that the update replaces.
	agent := connectAgent(t, exe, base)

	select {
	case <-exited:
	case <-time.After(4 * time.Minute):
		t.Fatal("the old version did not update itself within four minutes")
	}
	waitFor(t, "the new version's gateway", 3*time.Minute, func() bool { return gatewayVersion(dirs) == newV })
	if got := exeVersion(t, exe, base); got != newV {
		t.Errorf("Pinky.exe says %q", got)
	}
	if got := uninstallValue(t, "DisplayVersion"); got != newV {
		t.Errorf("Settings › Apps says version %q", got)
	}
	st := update.LoadState(filepath.Join(dirs.Config, "update.json"))
	if st.LastRun != newV || st.NotesFrom != oldV {
		t.Errorf("update.json: %+v", st)
	}
	waitFor(t, "the work folder to go", 90*time.Second, func() bool { return len(workFolders(programs)) == 0 })

	c := dialAutomation(t, sock)
	var labels []string
	waitFor(t, "What's new in the new version's window", 60*time.Second, func() bool {
		labels = c.labels(t)
		return containsAll(labels, "Pinky was updated", "Now "+newV)
	})
	c.conn.Close()
	t.Logf("the new version shows: %s", strings.Join(labels[:min(len(labels), 12)], " · "))

	// What became of the agent's session (the installer stops what runs
	// from the install folder).
	agent.check(t)

	// The next version exits as it starts: the helper puts this one back.
	quitPinky(dirs)
	waitFor(t, "the new version to quit", 30*time.Second, func() bool { return gatewayVersion(dirs) == "" })
	feed.offer(t, brokenV, setup[brokenV])
	exited = start()
	waitFor(t, "the new version's gateway", 60*time.Second, func() bool { return gatewayVersion(dirs) == newV })
	select {
	case <-exited:
	case <-time.After(4 * time.Minute):
		t.Fatal("the new version did not try the broken one within four minutes")
	}
	waitFor(t, "the update helper to bring the new version back", 4*time.Minute, func() bool {
		st := update.LoadState(filepath.Join(dirs.Config, "update.json"))
		return gatewayVersion(dirs) == newV && st.Failed == brokenV
	})
	st = update.LoadState(filepath.Join(dirs.Config, "update.json"))
	if !strings.Contains(st.FailedWhy, "didn't start") {
		t.Errorf("why the update failed: %q", st.FailedWhy)
	}
	if got := exeVersion(t, exe, base); got != newV {
		t.Errorf("after the rollback, Pinky.exe says %q", got)
	}
	if got := uninstallValue(t, "DisplayVersion"); got != newV {
		t.Errorf("after the rollback, Settings › Apps says version %q", got)
	}
	waitFor(t, "the work folder to go after the rollback", 90*time.Second, func() bool { return len(workFolders(programs)) == 0 })
	// It does not try the broken version again by itself: a minute's tick
	// later nothing is staged.
	time.Sleep(75 * time.Second)
	if w := workFolders(programs); len(w) != 0 || gatewayVersion(dirs) != newV {
		t.Errorf("after a tick: work folders %v, gateway %q", w, gatewayVersion(dirs))
	}
	logs, _ := os.ReadFile(filepath.Join(dirs.Data, "update.log"))
	t.Logf("update.log:\n%s", tail(string(logs), 4000))
}

// testFeed serves latest.json for the version it offers, and its installer.
type testFeed struct {
	srv  *httptest.Server
	mu   sync.Mutex
	body string
	file []byte
}

func newTestFeed(t *testing.T) *testFeed {
	f := &testFeed{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		body, file := f.body, f.file
		f.mu.Unlock()
		switch r.URL.Path {
		case "/latest.json":
			w.Write([]byte(body))
		case "/setup.exe":
			w.Write(file)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *testFeed) offer(t *testing.T, version, setup string) {
	t.Helper()
	data, err := os.ReadFile(setup)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	body := fmt.Sprintf(`{"version": %q, "tag": "v%s", "date": %q, "notes": "- Updates itself on Windows", "assets": {"windows-amd64": {"url": %q, "sha256": %q, "size": %d}}}`,
		version, version, time.Now().UTC().Format(time.RFC3339), f.srv.URL+"/setup.exe", hex.EncodeToString(sum[:]), len(data))
	f.mu.Lock()
	f.body, f.file = body, data
	f.mu.Unlock()
}

// installSetup runs an installer quietly into dir, as the helper does.
func installSetup(t *testing.T, setup, dir string) {
	t.Helper()
	cmd := exec.Command(setup)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(setup) + " /S /D=" + dir}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install %s: %v %s", filepath.Base(setup), err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "Pinky.exe")); err != nil {
		t.Fatalf("the installer left no Pinky.exe: %v", err)
	}
}

// uninstall removes Pinky quietly, in place (_?=), so that the test waits
// for it, and checks that its entry in Settings › Apps went.
func uninstall(t *testing.T, dir string) {
	un := filepath.Join(dir, "Uninstall.exe")
	if _, err := os.Stat(un); err != nil {
		return
	}
	cmd := exec.Command(un)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(un) + " /S _?=" + dir}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("uninstall: %v %s", err, out)
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\`+update.BundleID, registry.QUERY_VALUE); err == nil {
		k.Close()
		t.Error("the uninstaller left Pinky's entry in Settings › Apps")
	}
}

func uninstallValue(t *testing.T, name string) string {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\`+update.BundleID, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("Pinky's entry in Settings › Apps: %v", err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}

// exeVersion is what `Pinky.exe version` says.
func exeVersion(t *testing.T, exe string, env []string) string {
	t.Helper()
	cmd := exec.Command(exe, "version")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s version: %v", exe, err)
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "pinky"))
}

func workFolders(programs string) []string {
	m, _ := filepath.Glob(filepath.Join(programs, ".Pinky.update-*"))
	return m
}

// testAgent is an MCP client on `pinky mcp`, connected before the update.
type testAgent struct {
	cs     *sdk.ClientSession
	stderr *syncBuf
}

func connectAgent(t *testing.T, exe string, env []string) *testAgent {
	t.Helper()
	cmd := exec.Command(exe, "mcp")
	cmd.Env = env
	a := &testAgent{stderr: &syncBuf{}}
	cmd.Stderr = a.stderr
	cl := sdk.NewClient(&sdk.Implementation{Name: "claude-code", Version: "e2e"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cs, err := cl.Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to pinky mcp: %v\n%s", err, a.stderr)
	}
	t.Cleanup(func() { cs.Close() })
	a.cs = cs
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatalf("tools before the update: %v", err)
	}
	return a
}

// check reports whether the agent's session survived the update.
func (a *testAgent) check(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tools, err := a.cs.ListTools(ctx, nil)
	if err != nil {
		t.Logf("the agent's session through `pinky mcp` did not survive the update: %v\n%s", err, a.stderr)
		return
	}
	t.Logf("the agent's session through `pinky mcp` survived the update: %d tools", len(tools.Tools))
}

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
