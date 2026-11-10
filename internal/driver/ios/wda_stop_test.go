package ios

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/appservice"
	"github.com/danielpaulus/go-ios/ios/installationproxy"
)

const testRunnerApp = "/private/var/containers/Bundle/Application/0B5E/WebDriverAgentRunner-Runner.app"

// fakeProcTable is a phone's process table behind appservice.
type fakeProcTable struct {
	mu      sync.Mutex
	procs   map[int]string
	killed  []int
	sticky  int // kills that fail to end the process before one works
	refuse  map[string]bool
	opened  []string // the entries' addresses opened, in order
	closed  int
	linked  int // tunnels opened from the base entry
	unlinks int
}

func (f *fakeProcTable) open(e goios.DeviceEntry) (runnerProcs, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, e.Address)
	if f.refuse[e.Address] {
		return nil, errors.New("connection refused")
	}
	return f, nil
}

func (f *fakeProcTable) link(ctx context.Context, base goios.DeviceEntry) (goios.DeviceEntry, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.linked++
	return goios.DeviceEntry{Address: "new-tunnel"}, func() {
		f.mu.Lock()
		f.unlinks++
		f.mu.Unlock()
	}, nil
}

func (f *fakeProcTable) ListProcesses() ([]appservice.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var list []appservice.Process
	for pid, p := range f.procs {
		list = append(list, appservice.Process{Pid: pid, Path: p})
	}
	return list, nil
}

func (f *fakeProcTable) KillProcess(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, pid)
	if f.sticky > 0 {
		f.sticky--
		return nil
	}
	delete(f.procs, pid)
	return nil
}

func (f *fakeProcTable) Close() error {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
	return nil
}

func (f *fakeProcTable) running(pid int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.procs[pid]
	return ok
}

func usePhone(t *testing.T, f *fakeProcTable) {
	t.Helper()
	open, link, recheck := openRunnerProcs, dialRunnerLink, runnerRecheck
	openRunnerProcs, dialRunnerLink, runnerRecheck = f.open, f.link, time.Millisecond
	t.Cleanup(func() { openRunnerProcs, dialRunnerLink, runnerRecheck = open, link, recheck })
}

func newFakePhone() *fakeProcTable {
	return &fakeProcTable{procs: map[int]string{
		1:   "file:///usr/libexec/backboardd",
		300: "file:///private/var/containers/Bundle/Application/0B5E/WebDriverAgentRunner-Runner.app/WebDriverAgentRunner-Runner",
		301: "file:///var/containers/Bundle/Application/0B5E/WebDriverAgentRunner-Runner.app/PlugIns/X.appex/X",
		400: "file:///private/var/containers/Bundle/Application/0B5E/WebDriverAgentRunner-Runner.app.other/App",
		500: "file:///private/var/containers/Bundle/Application/77AA/MobileSafari.app/MobileSafari",
	}}
}

func TestRunnerPids(t *testing.T) {
	list, _ := newFakePhone().ListProcesses()
	got := map[int]bool{}
	for _, pid := range runnerPids(list, testRunnerApp) {
		got[pid] = true
	}
	if len(got) != 2 || !got[300] || !got[301] {
		t.Fatalf("runner pids %v, want 300 and 301", got)
	}
	if pids := runnerPids(list, ""); len(pids) != 0 {
		t.Fatalf("no app path matched %v", pids)
	}
}

func TestEndRunnerKillsUntilGone(t *testing.T) {
	f := newFakePhone()
	f.sticky = 1 // the first kill does not take
	usePhone(t, f)
	killed, err := endRunner(context.Background(), goios.DeviceEntry{Address: "tunnel"}, goios.DeviceEntry{}, testRunnerApp)
	if err != nil {
		t.Fatal(err)
	}
	if f.running(300) || f.running(301) {
		t.Fatal("the runner still runs")
	}
	if !f.running(1) || !f.running(400) || !f.running(500) {
		t.Fatal("killed a process that is not the runner's")
	}
	if killed < 2 || f.closed != 1 || f.linked != 0 {
		t.Fatalf("killed %d, closed %d, linked %d", killed, f.closed, f.linked)
	}
}

func TestEndRunnerNothingToKill(t *testing.T) {
	f := newFakePhone()
	delete(f.procs, 300)
	delete(f.procs, 301)
	usePhone(t, f)
	killed, err := endRunner(context.Background(), goios.DeviceEntry{Address: "tunnel"}, goios.DeviceEntry{}, testRunnerApp)
	if err != nil || killed != 0 || len(f.killed) != 0 {
		t.Fatalf("killed %d (%v), err %v", killed, f.killed, err)
	}
}

func TestEndRunnerThroughNewTunnel(t *testing.T) {
	f := newFakePhone()
	f.refuse = map[string]bool{"tunnel": true} // the run's tunnel is gone
	usePhone(t, f)
	base := goios.DeviceEntry{Properties: goios.DeviceProperties{SerialNumber: "phone"}}
	if _, err := endRunner(context.Background(), goios.DeviceEntry{Address: "tunnel"}, base, testRunnerApp); err != nil {
		t.Fatal(err)
	}
	if f.running(300) || f.linked != 1 || f.unlinks != 1 {
		t.Fatalf("running %v, linked %d, unlinked %d", f.running(300), f.linked, f.unlinks)
	}
	if len(f.opened) != 2 || f.opened[1] != "new-tunnel" {
		t.Fatalf("opened %v", f.opened)
	}
}

func TestEndRunnerGivesUp(t *testing.T) {
	f := newFakePhone()
	f.sticky = 1 << 30 // never dies
	usePhone(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := endRunner(ctx, goios.DeviceEntry{Address: "tunnel"}, goios.DeviceEntry{}, testRunnerApp); err == nil {
		t.Fatal("no error for a runner that never ended")
	}
}

// TestRunStopEndsRunner: stopping the run kills the runner even when
// go-ios's run has not ended it (it ignores the cancel here), once.
func TestRunStopEndsRunner(t *testing.T) {
	f := newFakePhone()
	usePhone(t, f)
	cancelled := make(chan struct{})
	r := &wdaRun{
		cancel: func() { close(cancelled) },
		done:   make(chan struct{}),
		entry:  goios.DeviceEntry{Address: "tunnel"},
		app:    wdaRunner{BundleID: "cc.example.WebDriverAgentRunner.xctrunner", Path: testRunnerApp},
	}
	close(r.done) // the run ended at once, the runner did not
	r.stop()
	r.stop()
	select {
	case <-cancelled:
	default:
		t.Fatal("the run was not cancelled")
	}
	if f.running(300) || f.running(301) {
		t.Fatal("the runner still runs after stop")
	}
	if len(f.opened) != 1 {
		t.Fatalf("stop looked %d times, want once", len(f.opened))
	}
}

// TestRunStopWithoutPath: a runner whose folder is unknown is left to
// go-ios, as before.
func TestRunStopWithoutPath(t *testing.T) {
	f := newFakePhone()
	usePhone(t, f)
	r := &wdaRun{cancel: func() {}, done: make(chan struct{})}
	close(r.done)
	r.stop()
	if len(f.opened) != 0 {
		t.Fatal("looked for a runner without its path")
	}
}

// TestWDACloseEndsRunnerBeforeTunnel: the device's Close kills the runner
// while the run's tunnel still reaches the phone.
func TestWDACloseEndsRunnerBeforeTunnel(t *testing.T) {
	f := newFakePhone()
	usePhone(t, f)
	srv := httptest.NewServer(&fakeWDA{})
	defer srv.Close()
	d := &WDADevice{c: newWDAURL(srv.URL, "")}
	d.run = &wdaRun{
		cancel: func() {},
		done:   make(chan struct{}),
		entry:  goios.DeviceEntry{Address: "tunnel"},
		app:    wdaRunner{Path: testRunnerApp},
	}
	close(d.run.done)
	d.Close()
	if f.running(300) || f.linked != 0 {
		t.Fatalf("running %v, linked %d", f.running(300), f.linked)
	}
}

func TestPickRunnerAppPath(t *testing.T) {
	got := pickRunnerApp([]installationproxy.AppInfo{{
		"CFBundleIdentifier": "cc.example.WebDriverAgentRunner.xctrunner",
		"CFBundleName":       "WebDriverAgentRunner-Runner",
		"Path":               testRunnerApp,
	}})
	if got.Path != testRunnerApp {
		t.Fatalf("path %q", got.Path)
	}
}
