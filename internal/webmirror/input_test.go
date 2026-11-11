package webmirror

import (
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

func TestToPicture(t *testing.T) {
	// A 100×200 stream picture (portrait as the phone sends it).
	for _, tc := range []struct {
		x, y float64
		turn int
		want screen.Point
	}{
		{0, 0, 0, screen.Point{X: 0, Y: 0}},
		{0.5, 0.25, 0, screen.Point{X: 50, Y: 50}},
		{1, 1, 0, screen.Point{X: 99, Y: 199}}, // clamped inside
		// Turned once clockwise for the page: the page's top-left corner
		// is the picture's bottom-left.
		{0, 0, 1, screen.Point{X: 0, Y: 199}},
		{1, 0, 1, screen.Point{X: 0, Y: 0}},
		{0, 1, 1, screen.Point{X: 99, Y: 199}},
		{0, 0, 2, screen.Point{X: 99, Y: 199}},
		{0, 0, 3, screen.Point{X: 99, Y: 0}},
		{1, 1, 3, screen.Point{X: 0, Y: 199}},
		{-3, 7, 0, screen.Point{X: 0, Y: 199}}, // outside the picture
	} {
		if got := toPicture(tc.x, tc.y, 100, 200, tc.turn); got != tc.want {
			t.Errorf("toPicture(%v, %v, turn %d) = %v, want %v", tc.x, tc.y, tc.turn, got, tc.want)
		}
	}
}

// controlConn connects with the control token and reads up to the phone's
// configuration, so touches have a picture size.
func controlConn(t *testing.T, s *Server, st *fakeStream, dial func() *websocket.Conn) *websocket.Conn {
	t.Helper()
	tok, err := s.ControlToken()
	if err != nil {
		t.Fatal(err)
	}
	ws := dial()
	websocket.JSON.Send(ws, hello{Token: tok})
	if m := receive(t, ws); m.Type != "devices" || !m.Control {
		t.Fatalf("got %+v, want devices with control", m)
	}
	st.ch <- driver.Packet{Data: annexB(unhex(t, h264SPSHex), unhex(t, h264PPSHex)), Config: true}
	if m := receive(t, ws); m.Type != "config" {
		t.Fatalf("got %+v, want config", m)
	}
	return ws
}

func touchMsg(a string, x, y float64) clientMsg {
	return clientMsg{Type: "touch", A: a, X: x, Y: y}
}

func TestControlTouches(t *testing.T) {
	in := newInputLog()
	st := &fakeStream{ch: make(chan driver.Packet, 8), keys: make(chan struct{}, 8)}
	s, hs := newTestServer(t, fakeSource{st: st, in: in})
	s.SetControl(true)
	ws := controlConn(t, s, st, func() *websocket.Conn { return dial(t, hs, hs.URL) })

	// A move or an up without a down is dropped; a drag goes through in
	// order, its moves maybe merged; then Back.
	websocket.JSON.Send(ws, touchMsg("move", 0.1, 0.1))
	websocket.JSON.Send(ws, touchMsg("up", 0.1, 0.1))
	websocket.JSON.Send(ws, touchMsg("down", 0.5, 0.5))
	for i := 1; i <= 20; i++ {
		websocket.JSON.Send(ws, touchMsg("move", 0.5, 0.5+float64(i)/100))
	}
	websocket.JSON.Send(ws, touchMsg("up", 0.5, 0.7))
	websocket.JSON.Send(ws, clientMsg{Type: "key", Key: "back"})
	websocket.JSON.Send(ws, clientMsg{Type: "key", Key: "power"}) // not a page key

	calls := in.wait(t, 3)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && calls[len(calls)-1] != "p1 key back"; {
		calls = in.wait(t, len(calls)+1)
	}
	if calls[0] != "p1 down 320,180" {
		t.Errorf("first call %q, want the down at the middle of 640×360", calls[0])
	}
	if got := calls[len(calls)-2:]; got[0] != "p1 up 320,251" || got[1] != "p1 key back" {
		t.Errorf("last calls %q, want the up then Back", got)
	}
	for _, c := range calls[1 : len(calls)-2] {
		if !strings.HasPrefix(c, "p1 move ") {
			t.Errorf("between down and up: %q", c)
		}
	}
}

func TestDisconnectLiftsFinger(t *testing.T) {
	in := newInputLog()
	st := &fakeStream{ch: make(chan driver.Packet, 8), keys: make(chan struct{}, 8)}
	s, hs := newTestServer(t, fakeSource{st: st, in: in})
	s.SetControl(true)
	ws := controlConn(t, s, st, func() *websocket.Conn { return dial(t, hs, hs.URL) })
	websocket.JSON.Send(ws, touchMsg("down", 0.25, 0.5))
	websocket.JSON.Send(ws, touchMsg("move", 0.5, 0.5))
	in.wait(t, 2)
	ws.Close()
	calls := in.wait(t, 3)
	if len(calls) != 3 || calls[2] != "p1 up 320,180" {
		t.Fatalf("calls %q, want the finger lifted where it was", calls)
	}

	// The phone's finger is free again for the next page.
	ws2 := controlConn(t, s, st, func() *websocket.Conn { return dial(t, hs, hs.URL) })
	websocket.JSON.Send(ws2, touchMsg("down", 0, 0))
	if calls := in.wait(t, 4); len(calls) != 4 || calls[3] != "p1 down 0,0" {
		t.Fatalf("calls %q, want the second page's down", calls)
	}
}

func TestViewTokenCannotTouch(t *testing.T) {
	in := newInputLog()
	st := &fakeStream{ch: make(chan driver.Packet, 8), keys: make(chan struct{}, 8)}
	s, hs := newTestServer(t, fakeSource{st: st, in: in})
	s.SetControl(true)
	tok, _ := s.Token()
	ws := dial(t, hs, hs.URL)
	websocket.JSON.Send(ws, hello{Token: tok})
	if m := receive(t, ws); m.Type != "devices" || m.Control {
		t.Fatalf("got %+v, want devices without control", m)
	}
	websocket.JSON.Send(ws, touchMsg("down", 0.5, 0.5))
	var b []byte
	if err := websocket.Message.Receive(ws, &b); err == nil {
		t.Fatal("a page that may only watch stayed connected after a touch")
	}
	time.Sleep(200 * time.Millisecond)
	in.mu.Lock()
	calls := in.calls
	in.mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("calls %q from a page that may only watch", calls)
	}
}

func TestControlOff(t *testing.T) {
	st := &fakeStream{ch: make(chan driver.Packet, 8), keys: make(chan struct{}, 8)}
	s, hs := newTestServer(t, fakeSource{st: st})
	tok, _ := s.ControlToken()
	ws := dial(t, hs, hs.URL)
	websocket.JSON.Send(ws, hello{Token: tok})
	if m := receive(t, ws); m.Type != "error" || m.Error != "control_off" {
		t.Fatalf("got %+v, want control_off", m)
	}

	// Turning control off cuts off the pages that use the phone.
	s.SetControl(true)
	ws = controlConn(t, s, st, func() *websocket.Conn { return dial(t, hs, hs.URL) })
	if n := s.Controllers(); n != 1 {
		t.Errorf("Controllers() = %d, want 1", n)
	}
	s.SetControl(false)
	var b []byte
	if err := websocket.Message.Receive(ws, &b); err == nil {
		t.Fatal("a page kept control after control was turned off")
	}
}

// Through Tailscale, control needs a person signed in to the tailnet,
// which `tailscale serve` names in Tailscale-User-Login: a public Funnel
// link or a tagged device has none.
func TestControlThroughTailnetNeedsUser(t *testing.T) {
	st := &fakeStream{ch: make(chan driver.Packet, 8), keys: make(chan struct{}, 8)}
	s, hs := newTestServer(t, fakeSource{st: st})
	s.SetControl(true)
	tok, _ := s.ControlToken()
	for _, tc := range []struct {
		user string
		want string
	}{{"", "error"}, {"someone@example.com", "devices"}} {
		cfg, err := websocket.NewConfig("ws://mac.tail1234.ts.net:8443"+WebSocketPath, "https://mac.tail1234.ts.net:8443")
		if err != nil {
			t.Fatal(err)
		}
		if tc.user != "" {
			cfg.Header.Set("Tailscale-User-Login", tc.user)
		}
		conn, err := net.Dial("tcp", strings.TrimPrefix(hs.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		ws, err := websocket.NewClient(cfg, conn)
		if err != nil {
			t.Fatal(err)
		}
		ws.SetDeadline(time.Now().Add(5 * time.Second))
		websocket.JSON.Send(ws, hello{Token: tok})
		if m := receive(t, ws); m.Type != tc.want || (m.Type == "error" && m.Error != "tailnet_user") {
			t.Errorf("user %q: got %+v, want %s", tc.user, m, tc.want)
		}
		ws.Close()
	}
}
