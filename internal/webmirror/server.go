// Package webmirror shows a phone's live screen in a web browser, on this
// computer or, through `tailscale serve`, on the person's other devices.
//
// It only shows: the page sends no input. It is off by default, the person
// turns it on in Settings, and it listens on this computer only (Addr).
// Other devices reach it through Tailscale, which the person points at it
// with `tailscale serve`; Pinky never runs that itself. Every viewer needs
// the mirror's own token, which can only watch, never the agent token:
// the page's link carries it after the "#", which browsers do not send to
// the server, and the page sends it as its first WebSocket message.
//
// The phone's video goes to the browser as it comes from the phone,
// without decoding or encoding it again: a WebSocket carries the decoder
// configuration (codec.go) and each picture, and the page decodes them
// with WebCodecs. It shows the phone's screen as Pinky's own mirror does,
// blocked apps and one-time codes included, so the token is the key to
// all of it.
package webmirror

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/screen"
)

// Addr is where the web mirror listens: this computer only, on the port
// after remote agents' (127.0.0.1:7465).
const Addr = "127.0.0.1:7466"

// WebSocketPath is where the page opens its WebSocket.
const WebSocketPath = "/ws"

// Source gives the phones and their live video, and takes the person's
// input: the gateway. Touch and Key are the window mirror's own, so input
// from the page pauses an agent's task as input on the mirror does; the
// page reaches nothing else of the gateway (approvals, unlocking).
type Source interface {
	Devices() []gateway.DeviceView
	Stream(ctx context.Context, deviceID string, opt driver.StreamOptions) (driver.Stream, error)
	Touch(deviceID string, a driver.TouchAction, p screen.Point) error
	Key(deviceID string, k driver.Key) error
}

// Options configure a Server.
type Options struct {
	Source Source
	// TokenFile keeps the mirror's token, which can only watch, and
	// ControlTokenFile the one that can also use the phone; mode 0600.
	TokenFile        string
	ControlTokenFile string
	Log              *slog.Logger
	// OwnNames returns this computer's own DNS names that `tailscale
	// serve` forwards requests under, such as "mac.tail1234.ts.net".
	// Requests under any other name than those and localhost's are
	// refused (DNS rebinding).
	OwnNames func(ctx context.Context) []string
	// Text returns the page's words, by key, in the language that best
	// suits the browser's Accept-Language, and that language's tag.
	Text func(acceptLanguage string) (lang string, text map[string]string)
}

// Server is the web mirror.
type Server struct {
	opt Options
	log *slog.Logger

	mu           sync.Mutex
	ln           net.Listener
	srv          *http.Server
	token        string
	controlToken string
	control      bool // the person lets the page use the phone
	conns        map[*websocket.Conn]role
	pressing     map[string]*session // the one session holding a finger down, by phone
	err          error               // why it could not turn on
	// stopped is set once the mirror is turned off, so a viewer whose
	// WebSocket opened while it was stopping is not kept.
	stopped bool
}

// New returns the web mirror, off.
func New(opt Options) *Server {
	log := opt.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Server{opt: opt, log: log, conns: map[*websocket.Conn]role{}, pressing: map[string]*session{}}
}

// Set turns the web mirror on or off. Turning it off ends every viewer's
// connection.
func (s *Server) Set(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on == (s.srv != nil) && s.err == nil {
		return nil
	}
	s.err = nil
	if !on {
		s.stopLocked()
		return nil
	}
	if err := s.tokensLocked(); err != nil {
		s.err = fmt.Errorf("web mirror token: %w", err)
		return s.err
	}
	ln, err := net.Listen("tcp", Addr)
	if err != nil {
		s.err = fmt.Errorf("listen on %s: %w", Addr, err)
		return s.err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	s.ln, s.srv, s.stopped = ln, srv, false
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Warn("web mirror stopped", "err", err)
		}
	}()
	s.log.Info("web mirror on", "addr", Addr)
	return nil
}

// On reports whether the web mirror is on.
func (s *Server) On() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srv != nil
}

// Err says why the web mirror could not turn on, nil when it is on or off
// as asked.
func (s *Server) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// URL is the page's address on this computer.
func URL() string { return "http://" + Addr + "/" }

// Close turns the web mirror off.
func (s *Server) Close() { s.Set(false) }

func (s *Server) stopLocked() {
	if s.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.srv.Shutdown(ctx)
	s.stopped = true
	s.closeConnsLocked()
	s.ln, s.srv = nil, nil
	s.log.Info("web mirror off")
}

// closeConnsLocked ends the WebSockets of the role given, or of every
// role, which Shutdown leaves alone.
func (s *Server) closeConnsLocked(only ...role) {
	for c, r := range s.conns {
		if len(only) == 0 || only[0] == r {
			c.Close()
		}
	}
}

// SetControl lets the page use the phone, or stops it: the browsers that
// do are cut off.
func (s *Server) SetControl(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.control == on {
		return
	}
	s.control = on
	if !on {
		s.closeConnsLocked(roleControl)
	}
	s.log.Info("web mirror: control from the browser", "on", on)
}

func (s *Server) controlAllowed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.control
}

// Viewers is how many browsers watch now, Controllers how many of the
// others can use the phone.
func (s *Server) Viewers() int     { return s.count(roleView) }
func (s *Server) Controllers() int { return s.count(roleControl) }

func (s *Server) count(r role) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, cr := range s.conns {
		if cr == r {
			n++
		}
	}
	return n
}

// Token returns the token that can only watch, ControlToken the one that
// can also use the phone, each made the first time.
func (s *Server) Token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.tokensLocked()
	return s.token, err
}

func (s *Server) ControlToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.tokensLocked()
	return s.controlToken, err
}

func (s *Server) tokensLocked() error {
	if err := loadToken(s.opt.TokenFile, &s.token); err != nil {
		return err
	}
	if s.opt.ControlTokenFile == "" {
		return nil
	}
	return loadToken(s.opt.ControlTokenFile, &s.controlToken)
}

// loadToken reads the token in path into *tok, making it when there is
// none.
func loadToken(path string, tok *string) error {
	if *tok != "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if t := strings.TrimSpace(string(b)); err == nil && t != "" {
		*tok = t
		return nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return newToken(path, tok)
}

// NewToken replaces both tokens: every link stops working, and the
// browsers that watch or use the phone with them are cut off.
func (s *Server) NewToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := newToken(s.opt.TokenFile, &s.token); err != nil {
		return "", err
	}
	if s.opt.ControlTokenFile != "" {
		if err := newToken(s.opt.ControlTokenFile, &s.controlToken); err != nil {
			return "", err
		}
	}
	s.closeConnsLocked()
	return s.token, nil
}

func newToken(path string, tok *string) error {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	t := hex.EncodeToString(b[:])
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(t+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	*tok = t
	return nil
}

// roleOf says what a hello's token lets the page do. Both tokens are
// compared in full, in constant time.
func (s *Server) roleOf(tok string) (r role, ok bool) {
	s.mu.Lock()
	view, ctl := s.token, s.controlToken
	s.mu.Unlock()
	isView := view != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(view)) == 1
	isCtl := ctl != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(ctl)) == 1
	switch {
	case isCtl:
		return roleControl, true
	case isView:
		return roleView, true
	}
	return roleView, false
}

// Handler serves the page and its WebSocket.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.servePage)
	mux.Handle("GET "+WebSocketPath, websocket.Server{Handshake: s.handshake, Handler: s.serveViewer})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.ownHost(r) {
			http.Error(w, "Pinky's web mirror answers only at this computer's own names.", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ownHost reports whether r names this computer: localhost, a loopback
// address, or one of its tailnet names, which `tailscale serve` forwards
// requests under. A page whose DNS name was rebound to 127.0.0.1 names
// another host, and is refused.
func (s *Server) ownHost(r *http.Request) bool {
	name := r.Host
	if h, _, err := net.SplitHostPort(name); err == nil {
		name = h
	}
	name = strings.TrimSuffix(name, ".")
	if loopbackName(name) {
		return true
	}
	if s.opt.OwnNames == nil || name == "" {
		return false
	}
	for _, own := range s.opt.OwnNames(r.Context()) {
		if own = strings.TrimSuffix(own, "."); own != "" && strings.EqualFold(name, own) {
			return true
		}
	}
	return false
}

func loopbackName(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// handshake takes WebSockets from the page itself only: the Origin must
// name the host the request does.
func (s *Server) handshake(cfg *websocket.Config, r *http.Request) error {
	o, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || o.Host == "" || !strings.EqualFold(o.Host, r.Host) {
		return errors.New("cross-origin WebSocket")
	}
	cfg.Origin = o
	return nil
}

//go:embed page.html
var pageHTML string

var page = template.Must(template.New("page").Parse(pageHTML))

func (s *Server) servePage(w http.ResponseWriter, r *http.Request) {
	lang, text := "en", map[string]string{}
	if s.opt.Text != nil {
		lang, text = s.opt.Text(r.Header.Get("Accept-Language"))
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self' ws: wss:; img-src data:; frame-ancestors 'none'")
	page.Execute(w, struct {
		Lang string
		Text map[string]string
	}{lang, text})
}

// Messages between the page and the server. The page's first one is a
// hello with the token; text messages from the server are JSON, binary
// ones pictures (frameMessage).
type hello struct {
	Token  string `json:"token"`
	Device string `json:"device"`
}

type clientMsg struct {
	Type string `json:"type"` // "keyframe", "touch", "key"
	// A touch: its phase ("down", "move", "up"), where it is from 0 to 1
	// across the upright picture the page shows, and the quarter turns the
	// page turned the stream's picture by to show it.
	A    string  `json:"a,omitempty"`
	X    float64 `json:"x,omitempty"`
	Y    float64 `json:"y,omitempty"`
	Turn int     `json:"turn,omitempty"`
	Key  string  `json:"key,omitempty"` // "back", "home"
}

type deviceInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Model    string `json:"model,omitempty"` // for the bezel: an iPad's corners differ
}

type serverMsg struct {
	Type    string       `json:"type"` // "devices", "config", "state", "error", "ended"
	Devices []deviceInfo `json:"devices,omitempty"`
	Device  string       `json:"device,omitempty"`
	Control bool         `json:"control,omitempty"` // the page may use the phone
	Status  string       `json:"status,omitempty"`  // the phone's gateway.DeviceStatus

	Codec       string `json:"codec,omitempty"`
	Description string `json:"description,omitempty"` // base64
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Error       string `json:"error,omitempty"` // "token", "control_off", "tailnet_user", "no_phone", "stream"
	Detail      string `json:"detail,omitempty"`
}

// Viewer limits.
const (
	helloTimeout = 10 * time.Second
	writeTimeout = 10 * time.Second
	// maxLag is how far the pictures sent may fall behind the phone before
	// the viewer skips to the next key frame.
	maxLag = 1500 * time.Millisecond
	// statusEvery is how often a page that can use the phone hears of its
	// state.
	statusEvery = time.Second
)

// status is the phone's gateway.DeviceStatus, "" when it is gone.
func (s *Server) status(device string) string {
	for _, d := range s.opt.Source.Devices() {
		if d.Info.ID == device {
			return string(d.Status)
		}
	}
	return ""
}

func (s *Server) serveViewer(ws *websocket.Conn) {
	defer ws.Close()
	ws.MaxPayloadBytes = 4 << 10
	r := ws.Request()
	ws.SetReadDeadline(time.Now().Add(helloTimeout))
	var h hello
	if err := websocket.JSON.Receive(ws, &h); err != nil {
		return
	}
	ws.SetReadDeadline(time.Time{})
	rl, ok := s.roleOf(h.Token)
	if !ok {
		send(ws, serverMsg{Type: "error", Error: "token"})
		return
	}
	who := r.Header.Get("Tailscale-User-Login") // set by `tailscale serve` for the tailnet's signed-in users
	if rl == roleControl {
		if !s.controlAllowed() {
			send(ws, serverMsg{Type: "error", Error: "control_off"})
			return
		}
		// Through Tailscale, only a person signed in to the tailnet: a
		// tagged device or a public Funnel link carries no user, and
		// `tailscale serve` drops the header from the request it got.
		if !loopbackName(hostName(r.Host)) && who == "" {
			send(ws, serverMsg{Type: "error", Error: "tailnet_user"})
			return
		}
	}
	if !s.addConn(ws, rl) {
		return
	}
	defer s.removeConn(ws)
	s.log.Info("web mirror: a browser joined", "role", rl, "from", r.RemoteAddr, "tailscale_user", who)
	defer s.log.Info("web mirror: a browser left", "role", rl, "from", r.RemoteAddr, "tailscale_user", who)

	devs, id := s.devices(h.Device)
	if err := send(ws, serverMsg{Type: "devices", Devices: devs, Device: id, Control: rl == roleControl}); err != nil {
		return
	}
	if id == "" {
		send(ws, serverMsg{Type: "error", Error: "no_phone"})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := s.opt.Source.Stream(ctx, id, driver.StreamOptions{})
	if err != nil {
		send(ws, serverMsg{Type: "error", Error: "stream", Detail: err.Error()})
		return
	}
	defer st.Close()
	var ss *session
	if rl == roleControl {
		ss = newSession(s, id)
		defer ss.close()
	}
	keyFrames := make(chan struct{}, 1)
	go func() {
		defer cancel()
		for {
			var m clientMsg
			if err := websocket.JSON.Receive(ws, &m); err != nil {
				return
			}
			switch m.Type {
			case "keyframe":
				select {
				case keyFrames <- struct{}{}:
				default:
				}
			case "touch", "key":
				if ss == nil || !s.controlAllowed() {
					return // a page that may only watch, or control was turned off
				}
				if m.Type == "key" {
					ss.key(m.Key)
				} else {
					ss.touch(driver.TouchAction(m.A), m.X, m.Y, m.Turn)
				}
			}
		}
	}()
	err = s.pump(ctx, ws, st, keyFrames, id, ss)
	if err == nil {
		send(ws, serverMsg{Type: "ended"})
	}
}

func hostName(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}

// pump sends the stream's pictures to the page until either ends. It
// returns nil when the stream ended.
func (s *Server) pump(ctx context.Context, ws *websocket.Conn, st driver.Stream, keyFrames <-chan struct{}, device string, ss *session) error {
	conv := &converter{codec: st.Codec()}
	needKey := true
	var lag lagMeter
	// The phone's state, for a page that can use it: it shows when the
	// person's input paused an agent's task.
	var status string
	var tick <-chan time.Time
	if ss != nil {
		t := time.NewTicker(statusEvery)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick:
			if now := s.status(device); now != status {
				status = now
				if err := send(ws, serverMsg{Type: "state", Status: now}); err != nil {
					return err
				}
			}
		case <-keyFrames:
			// The page could not decode a picture: start again at a key
			// frame.
			needKey = true
			requestKeyFrame(st)
		case p, ok := <-st.Packets():
			if !ok {
				return nil
			}
			cfg, chunk, err := conv.convert(p)
			if err != nil {
				if !errors.Is(err, errNoConfig) {
					s.log.Warn("web mirror: cannot pass a picture on", "err", err)
				}
				continue
			}
			if cfg != nil {
				if ss != nil {
					ss.setSize(cfg.Width, cfg.Height)
				}
				if err := send(ws, serverMsg{Type: "config", Codec: cfg.Codec, Width: cfg.Width, Height: cfg.Height,
					Description: base64.StdEncoding.EncodeToString(cfg.Description)}); err != nil {
					return err
				}
				needKey = true
			}
			if chunk == nil {
				continue
			}
			behind := lag.behind(p.PTS, time.Now()) > maxLag
			if needKey || behind {
				if !p.KeyFrame || behind {
					if !needKey {
						requestKeyFrame(st)
					}
					needKey = true
					continue
				}
				needKey = false
			}
			ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := websocket.Message.Send(ws, frameMessage(p, chunk)); err != nil {
				return err
			}
		}
	}
}

// lagMeter tells how far behind the phone the pictures are: by how much a
// picture's arrival here, less its time on the phone, exceeds the least
// that difference has been. Pictures queued for a slow browser arrive here
// late; the pictures since the last key frame that a viewer gets at once
// when it joins are older still, but come before any newer one and so
// never look late.
type lagMeter struct {
	base time.Time
	min  time.Duration
	set  bool
}

func (l *lagMeter) behind(pts time.Duration, now time.Time) time.Duration {
	if !l.set {
		l.base, l.set = now, true
		l.min = now.Sub(l.base) - pts
		return 0
	}
	d := now.Sub(l.base) - pts
	if d < l.min {
		l.min = d
	}
	return d - l.min
}

// requestKeyFrame asks the phone for a key frame, when its stream can.
func requestKeyFrame(st driver.Stream) {
	if kr, ok := st.(interface{ RequestKeyFrame() }); ok {
		kr.RequestKeyFrame()
	}
}

// devices lists the phones online and picks the one to show: want when it
// is online, else the first.
func (s *Server) devices(want string) ([]deviceInfo, string) {
	var out []deviceInfo
	pick := ""
	for _, d := range s.opt.Source.Devices() {
		if d.Info.State != driver.StateOnline {
			continue
		}
		out = append(out, deviceInfo{ID: d.Info.ID, Name: d.Info.Name, Platform: string(d.Info.Platform), Model: d.Info.Model})
		if pick == "" || d.Info.ID == want {
			pick = d.Info.ID
		}
	}
	return out, pick
}

func (s *Server) addConn(ws *websocket.Conn, r role) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || (r == roleControl && !s.control) {
		return false
	}
	s.conns[ws] = r
	return true
}

func (s *Server) removeConn(ws *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, ws)
}

func send(ws *websocket.Conn, m serverMsg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return websocket.Message.Send(ws, string(b))
}
