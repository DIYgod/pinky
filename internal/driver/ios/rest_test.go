package ios

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// restLink is a link on a tunnel with nothing behind it, which Close stops.
func restLink() *link {
	return &link{tunnel: &Tunnel{done: make(chan struct{})}}
}

// tunnelClosed reports whether l's tunnel was closed.
func tunnelClosed(l *link) bool {
	select {
	case <-l.tunnel.Done():
		return true
	default:
		return false
	}
}

// restDevice is a Device on a fake link whose wakes dial through dial.
func restDevice(t *testing.T, dial func(context.Context) (*link, error)) *Device {
	t.Helper()
	d := &Device{info: driver.Info{ID: "phone"}, l: restLink(), done: make(chan struct{}), openedAt: time.Now()}
	d.redial = dial
	t.Cleanup(func() { close(d.done) })
	return d
}

func TestDeviceRestsAndWakes(t *testing.T) {
	var dials atomic.Int32
	d := restDevice(t, func(context.Context) (*link, error) {
		dials.Add(1)
		return restLink(), nil
	})
	first := d.cur()
	later := time.Now().Add(2 * restIdle)

	// Just opened, used, or with an agent working: no rest.
	if d.restIfIdle(time.Now()) {
		t.Fatal("rested as it opened")
	}
	d.AgentWorking(true)
	if d.restIfIdle(later) {
		t.Fatal("rested while an agent works")
	}
	d.AgentWorking(false)
	d.mu.Lock()
	d.watchers = 1
	d.mu.Unlock()
	if d.restIfIdle(later) {
		t.Fatal("rested while someone watches")
	}
	d.mu.Lock()
	d.watchers = 0
	d.mu.Unlock()

	if !d.restIfIdle(later) || !d.Resting() {
		t.Fatal("an idle phone did not rest")
	}
	if !tunnelClosed(first) {
		t.Fatal("the tunnel stayed open")
	}
	// Resting is not gone: lockdown reads go on, and errors are not offline.
	if d.linkGone() || errors.Is(d.gone(errors.New("x")), driver.ErrOffline) {
		t.Fatal("a resting phone counts as gone")
	}
	if d.cur() != first || dials.Load() != 0 {
		t.Fatal("cur woke the phone")
	}
	if d.restIfIdle(later) {
		t.Fatal("rested twice")
	}

	// The next use wakes it, once, with a new tunnel.
	var wg sync.WaitGroup
	links := make([]*link, 4)
	for i := range links {
		wg.Add(1)
		go func() { defer wg.Done(); links[i] = d.link() }()
	}
	wg.Wait()
	if d.Resting() || dials.Load() != 1 {
		t.Fatalf("resting %v after %d dials", d.Resting(), dials.Load())
	}
	for _, l := range links {
		if l == first || l != d.cur() {
			t.Fatal("a caller got the old tunnel")
		}
	}
	// Used just now: no rest until restIdle passes again.
	if d.restIfIdle(time.Now()) {
		t.Fatal("rested right after a wake")
	}
}

func TestDeviceWakeFails(t *testing.T) {
	d := restDevice(t, func(context.Context) (*link, error) {
		return nil, errors.New("no route to the phone")
	})
	if !d.restIfIdle(time.Now().Add(2 * restIdle)) {
		t.Fatal("did not rest")
	}
	d.link()
	if !d.linkGone() {
		t.Fatal("a failed wake does not count as gone")
	}
	if err := d.gone(errors.New("read")); !errors.Is(err, driver.ErrOffline) {
		t.Fatalf("after a failed wake: %v", err)
	}
	if _, err := d.wakeUp(context.Background()); !errors.Is(err, driver.ErrOffline) || !strings.Contains(err.Error(), "no route") {
		t.Fatalf("a second wake: %v", err)
	}
}

// fakeRun is a runner Pinky started, which stop ends at once.
func fakeRun() (*wdaRun, *atomic.Bool) {
	stopped := new(atomic.Bool)
	done := make(chan struct{})
	return &wdaRun{done: done, cancel: func() {
		if stopped.CompareAndSwap(false, true) {
			close(done)
		}
	}}, stopped
}

// dialTo routes a WDA client to a test server.
func dialTo(srv *httptest.Server) *wdaClient {
	addr := srv.Listener.Addr().String()
	return newWDADial(func(ctx context.Context, _ int) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	})
}

func TestWDADeviceRestsAndWakes(t *testing.T) {
	f1, f2 := &fakeWDA{}, &fakeWDA{}
	s1, s2 := httptest.NewServer(f1), httptest.NewServer(f2)
	defer s1.Close()
	defer s2.Close()
	run, stopped := fakeRun()
	d := &WDADevice{info: driver.Info{ID: "phone"}, c: dialTo(s1), run: run, done: make(chan struct{}), openedAt: time.Now()}
	defer close(d.done)
	d.c.enter = d.enter
	var reopens atomic.Int32
	var reopenErr error
	run2, stopped2 := fakeRun()
	d.reopen = func(context.Context) (*WDADevice, error) {
		reopens.Add(1)
		if reopenErr != nil {
			return nil, reopenErr
		}
		return &WDADevice{c: dialTo(s2), run: run2}, nil
	}
	ctx := context.Background()
	later := func() time.Time { return time.Now().Add(2 * restIdle) }

	if err := d.c.keys(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if d.restIfIdle(time.Now()) {
		t.Fatal("rested right after a request")
	}
	d.AgentWorking(true)
	if d.restIfIdle(later()) {
		t.Fatal("rested while an agent works")
	}
	d.AgentWorking(false)
	streamDone := make(chan struct{})
	d.watchStream(streamDone)
	if d.restIfIdle(later()) {
		t.Fatal("rested while the picture is watched")
	}
	close(streamDone)
	for deadline := time.Now().Add(time.Second); ; time.Sleep(time.Millisecond) {
		d.rmu.Lock()
		w := d.watchers
		d.rmu.Unlock()
		if w == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the view was not let go")
		}
	}

	if !d.restIfIdle(later()) || !d.Resting() || !stopped.Load() {
		t.Fatalf("resting %v, runner stopped %v", d.Resting(), stopped.Load())
	}
	if d.ended() {
		t.Fatal("a resting helper counts as ended")
	}
	if reopens.Load() != 0 {
		t.Fatal("resting started the runner")
	}

	// The next request starts the runner again and goes to it.
	if err := d.c.keys(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if d.Resting() || reopens.Load() != 1 {
		t.Fatalf("resting %v after %d reopens", d.Resting(), reopens.Load())
	}
	f2.mu.Lock()
	keys := f2.bodies["POST /wda/keys"]
	f2.mu.Unlock()
	if len(keys) != 1 || !strings.Contains(keys[0], `"b"`) {
		t.Fatalf("the new runner got %v", keys)
	}

	// A wake that fails reports the phone offline.
	if !d.restIfIdle(later()) || !stopped2.Load() {
		t.Fatal("did not rest again")
	}
	reopenErr = errors.New("passcode needed")
	if err := d.c.keys(ctx, "c"); !errors.Is(err, driver.ErrOffline) {
		t.Fatalf("a failed wake: %v", err)
	}
	if !d.ended() {
		t.Fatal("a failed wake does not end the device")
	}
}

// A runner Pinky did not start (Xcode's) never rests.
func TestWDADeviceTheirsNeverRests(t *testing.T) {
	f := &fakeWDA{}
	s := httptest.NewServer(f)
	defer s.Close()
	d := &WDADevice{info: driver.Info{ID: "phone"}, c: dialTo(s), done: make(chan struct{})}
	defer close(d.done)
	d.reopen = func(context.Context) (*WDADevice, error) { t.Fatal("reopened"); return nil, nil }
	if d.restIfIdle(time.Now().Add(2 * restIdle)) {
		t.Fatal("rested a runner that is not Pinky's")
	}
}
