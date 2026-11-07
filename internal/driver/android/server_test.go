package android

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
)

func TestServerWatch(t *testing.T) {
	var w serverWatch
	logf := func(string, ...any) {}
	t0 := time.Unix(1_000_000, 0)
	// No server at the start: start one at once.
	if !w.claim(t0, logf) {
		t.Fatal("no start at first")
	}
	// One start at a time.
	if w.claim(t0, logf) {
		t.Fatal("a second start while one is under way")
	}
	w.done(t0, errors.New("failed"))
	// After a failure: serverRetry, then twice that.
	if w.claim(t0.Add(serverRetry-time.Second), logf) {
		t.Fatal("a start before serverRetry")
	}
	if !w.claim(t0.Add(serverRetry), logf) {
		t.Fatal("no start after serverRetry")
	}
	w.done(t0.Add(serverRetry), errors.New("failed again"))
	if w.claim(t0.Add(serverRetry+2*serverRetry-time.Second), logf) {
		t.Fatal("the wait did not double")
	}
	if !w.claim(t0.Add(3*serverRetry), logf) {
		t.Fatal("no start after the doubled wait")
	}
	w.done(t0.Add(3*serverRetry), nil)
	w.answered()

	// A server that ran goes away: another tool gets serverAway to bring
	// one back first.
	t1 := t0.Add(time.Hour)
	if w.claim(t1, logf) || w.claim(t1.Add(serverAway-time.Millisecond), logf) {
		t.Fatal("started one within serverAway of the server going")
	}
	if !w.claim(t1.Add(serverAway), logf) {
		t.Fatal("no start after serverAway")
	}
	w.done(t1.Add(serverAway), nil)
	// It came back: the next time it goes, the wait starts again.
	w.answered()
	t2 := t1.Add(time.Hour)
	if w.claim(t2, logf) {
		t.Fatal("started one at once after it went again")
	}
}

// TestListStartsServer: List starts a server when none answers, and
// lists what it then finds.
func TestListStartsServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now
	var starts atomic.Int32
	f := &fakeADB{t: t}
	p := NewProvider(Config{
		Client: &adb.Client{Addr: addr},
		StartServer: func(ctx context.Context) error {
			starts.Add(1)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			f.ln = ln
			t.Cleanup(func() { ln.Close() })
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					go f.handle(c)
				}
			}()
			return nil
		},
		Logf: t.Logf,
	})
	p.usbNodes = nil
	ctx := context.Background()
	infos, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 || starts.Load() != 1 {
		t.Errorf("listed %v after %d starts", infos, starts.Load())
	}
	if _, err := p.List(ctx); err != nil || starts.Load() != 1 {
		t.Errorf("again: %v, %d starts", err, starts.Load())
	}
	// The server goes: List reports the phones gone, and leaves the
	// server to whoever restarts it, for a while.
	f.ln.Close()
	if _, err := p.List(ctx); !errors.Is(err, driver.ErrOffline) || starts.Load() != 1 {
		t.Errorf("without the server: %v, %d starts", err, starts.Load())
	}
}

// TestListDriverless: phones without a driver come after adb's.
func TestListDriverless(t *testing.T) {
	f, p := newFakeADB(t)
	f.set(func(f *fakeADB) {
		f.devices = []string{"emulator-5560\tdevice product:sdk model:sdk_gphone64 device:emu64a transport_id:1"}
		f.props = map[string]string{"emulator-5560": "sdk_gphone64\n17\n37\nnull\nEMULATOR\n"}
	})
	p.usbNodes = func() ([]usbNode, error) {
		return []usbNode{{ID: `USB\VID_18D1&PID_4EE7\SERIAL1`, Compatible: []string{adbInterface}, Driverless: true}}, nil
	}
	infos, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].ID != "emulator-5560" || infos[1].ID != driverlessPrefix+`USB\VID_18D1&PID_4EE7\SERIAL1` {
		t.Errorf("listed %+v", infos)
	}
	p.usbNodes = func() ([]usbNode, error) { return nil, errors.New("no SetupAPI") }
	if infos, err := p.List(context.Background()); err != nil || len(infos) != 1 {
		t.Errorf("when Windows does not answer: %+v %v", infos, err)
	}
}
