// Package webapproval lets the person answer Pinky's approvals in a web
// browser on their phone, away from the computer: a page served next to
// the web mirror (internal/webmirror), reached through `tailscale serve`.
// docs/notes/remote-approvals.md has the design and the threat model.
//
// It is off by default. Every request must come through Tailscale from
// the person who owns this computer, and still proves nothing by that
// alone: a process on this computer can send any header to 127.0.0.1.
// What lets a browser answer is a passkey made on the person's phone,
// which the person adds by clicking Allow in Pinky's window after
// comparing a code the phone shows; every answer is a fresh challenge
// that names the approval and the decision, signed by that passkey with
// the person verified (Face ID, a fingerprint, the phone's passcode). An
// agent on this computer has no such passkey, and an agent that drives
// the phone cannot pass the phone's own check.
//
// Everything lives in memory only: the links that add a phone, the
// requests waiting for the person, the phones added and their session
// tokens. Quitting Pinky or turning the feature off forgets them.
//
// It reaches the gateway through Source, which has Needs and Decide only,
// and answers only what docs/notes/remote-approvals.md lists, with Allow
// once or Deny; the rest stays in Pinky's window.
package webapproval

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image/jpeg"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/screen"
)

// MaxPhones is how many phones may answer approvals at once.
const MaxPhones = 8

// Path is where the page is, under the web mirror's address.
const Path = "/approvals"

// Source is the gateway, as far as the page reaches it.
type Source interface {
	Needs() []gateway.NeedView
	Decide(needID string, d gateway.Decision, note string) error
}

// Options configure a Server.
type Options struct {
	Source Source
	Log    *slog.Logger
	// Owner returns the tailnet login of the person who owns this
	// computer, "" when it has none (a tagged computer): only they may use
	// the page.
	Owner func(ctx context.Context) string
	// Proxied reports whether a request came through `tailscale serve`
	// (webmirror.Proxied).
	Proxied func(r *http.Request) bool
	// Text returns the page's words, by key, in the language that best
	// suits the browser's Accept-Language, and that language's tag.
	Text func(acceptLanguage string) (lang string, text map[string]string)
	// OnChange runs, without locks held, when a request to add a phone
	// comes or goes, or a phone is added or removed, so Settings shows it.
	OnChange func()
}

// Limits.
const (
	linkLife      = 5 * time.Minute // an add-a-phone link, and the request it starts
	challengeLife = 2 * time.Minute // an answer's challenge, at most; never past the approval's deadline
	maxPhones     = 8               // phones added at once
	maxChallenges = 32              // challenges open per phone
	maxBody       = 64 << 10        // a request's body
)

// Errors of Allow.
var (
	ErrGone    = errors.New("this request is gone")
	ErrTooMany = fmt.Errorf("at most %d phones can answer approvals; remove one first", MaxPhones)
)

// Request is a browser that asks to answer approvals, waiting for the
// person to compare its code and allow it in Pinky's window.
type Request struct {
	ID      string
	Login   string // the tailnet login `tailscale serve` named
	Browser string // what the page says it is, as "iPhone · Safari"
	Code    string // six digits the page shows too
	Created time.Time
}

// Phone is a browser that may answer approvals.
type Phone struct {
	ID       string
	Login    string
	Browser  string
	Added    time.Time
	LastSeen time.Time
}

// Server is the approvals page.
type Server struct {
	opt Options
	log *slog.Logger

	mu         sync.Mutex
	on         bool
	link       string // the one add-a-phone link's token, "" when none
	linkExp    time.Time
	pairs      map[string]*pairing   // by id
	phones     map[[32]byte]*phone   // by the session token's hash
	challenges map[string]*challenge // by the challenge, base64url
}

type pairState int

const (
	pairBegun   pairState = iota // the page is making a passkey
	pairWaiting                  // the person compares the code
	pairAllowed                  // added; the page picks up its session
	pairDenied
)

type pairing struct {
	Request
	state     pairState
	challenge []byte
	expires   time.Time
	key       *ecdsa.PublicKey
	credID    []byte
	session   string // set once allowed, until the page picks it up
}

type phone struct {
	Phone
	key    *ecdsa.PublicKey
	credID []byte
}

type challenge struct {
	phone    [32]byte
	need     string
	decision gateway.Decision
	value    []byte
	expires  time.Time
	deadline time.Time // the approval's
}

// New returns the approvals page, off.
func New(opt Options) *Server {
	log := opt.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Server{opt: opt, log: log, pairs: map[string]*pairing{}, phones: map[[32]byte]*phone{},
		challenges: map[string]*challenge{}}
}

// Set turns the page on or off. Off forgets every phone, link and
// request.
func (s *Server) Set(on bool) {
	s.mu.Lock()
	changed := on != s.on
	s.on = on
	if !on {
		s.link, s.linkExp = "", time.Time{}
		clear(s.pairs)
		clear(s.phones)
		clear(s.challenges)
	}
	s.mu.Unlock()
	if changed {
		s.log.Info("approvals in the browser", "on", on)
		s.changed()
	}
}

// On reports whether the page is on.
func (s *Server) On() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on
}

func (s *Server) changed() {
	if s.opt.OnChange != nil {
		s.opt.OnChange()
	}
}

// NewLink makes the token of a link that adds a phone, good once and for
// a few minutes; it replaces an earlier one not yet used. "" while off.
func (s *Server) NewLink() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.on {
		return ""
	}
	s.link, s.linkExp = rand.Text(), time.Now().Add(linkLife)
	return s.link
}

// Requests are the browsers waiting for the person to allow them, oldest
// first.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(time.Now())
	var out []Request
	for _, p := range s.pairs {
		if p.state == pairWaiting {
			out = append(out, p.Request)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// Allow adds the browser of a request; Pinky's window calls it when the
// person clicks Allow. It is the only way a phone is added.
func (s *Server) Allow(id string) error {
	s.mu.Lock()
	s.sweepLocked(time.Now())
	p, ok := s.pairs[id]
	if !ok || p.state != pairWaiting {
		s.mu.Unlock()
		return ErrGone
	}
	if len(s.phones) >= MaxPhones {
		s.mu.Unlock()
		return ErrTooMany
	}
	tok := rand.Text()
	now := time.Now()
	ph := &phone{Phone: Phone{ID: p.ID, Login: p.Login, Browser: p.Browser, Added: now, LastSeen: now}, key: p.key, credID: p.credID}
	s.phones[sha256.Sum256([]byte(tok))] = ph
	p.state, p.session = pairAllowed, tok
	s.mu.Unlock()
	s.log.Info("approvals in the browser: phone added", "login", p.Login, "browser", p.Browser)
	s.changed()
	return nil
}

// Deny turns a request down.
func (s *Server) Deny(id string) {
	s.mu.Lock()
	p, ok := s.pairs[id]
	if ok && p.state == pairWaiting {
		p.state = pairDenied
	}
	s.mu.Unlock()
	if ok {
		s.changed()
	}
}

// Phones are the browsers that may answer approvals, oldest first.
func (s *Server) Phones() []Phone {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Phone
	for _, p := range s.phones {
		out = append(out, p.Phone)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Added.Before(out[j].Added) })
	return out
}

// Remove takes a phone's right to answer away.
func (s *Server) Remove(id string) {
	s.mu.Lock()
	found := false
	for h, p := range s.phones {
		if p.ID == id {
			delete(s.phones, h)
			for k, c := range s.challenges {
				if c.phone == h {
					delete(s.challenges, k)
				}
			}
			found = true
		}
	}
	s.mu.Unlock()
	if found {
		s.changed()
	}
}

// sweepLocked forgets expired links, requests and challenges.
func (s *Server) sweepLocked(now time.Time) {
	if s.link != "" && now.After(s.linkExp) {
		s.link = ""
	}
	for id, p := range s.pairs {
		if now.After(p.expires) {
			delete(s.pairs, id)
		}
	}
	for k, c := range s.challenges {
		if now.After(c.expires) {
			delete(s.challenges, k)
		}
	}
}

// Answerable reports whether the page may answer a need: an action's
// approval or an agent's own question, on a phone. Not an agent's first
// connection (it needs a scope), opening a blocked app, handoffs,
// unlocking or setup.
func Answerable(n gateway.NeedView) bool {
	return n.Kind == gateway.NeedApproval && n.DeviceID != "" && n.Rule != "New agent" && !n.Unblock
}

// Handler serves the page and its API under Path. The web mirror serves
// it behind its own checks (Tailscale Funnel, this computer's names).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+Path, s.servePage)
	mux.HandleFunc("POST "+Path+"/api/pair/begin", s.pairBegin)
	mux.HandleFunc("POST "+Path+"/api/pair/finish", s.pairFinish)
	mux.HandleFunc("POST "+Path+"/api/pair/status", s.pairStatus)
	mux.HandleFunc("POST "+Path+"/api/needs", s.authed(s.needs))
	mux.HandleFunc("POST "+Path+"/api/shot", s.authed(s.shot))
	mux.HandleFunc("POST "+Path+"/api/challenge", s.authed(s.challenge))
	mux.HandleFunc("POST "+Path+"/api/decide", s.authed(s.decide))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		if !s.On() {
			http.NotFound(w, r)
			return
		}
		// Through Tailscale, from the person who owns this computer. A
		// local process can fake both, so this is never enough alone.
		if s.opt.Proxied == nil || !s.opt.Proxied(r) || !s.ownerLogin(r) {
			if r.Method == http.MethodGet {
				s.writePage(w, r, http.StatusForbidden, "tailnet")
				return
			}
			apiError(w, http.StatusForbidden, "tailnet")
			return
		}
		if r.Method == http.MethodPost && !sameOrigin(r) {
			apiError(w, http.StatusForbidden, "origin")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) ownerLogin(r *http.Request) bool {
	login := r.Header.Get("Tailscale-User-Login")
	if login == "" || s.opt.Owner == nil {
		return false
	}
	owner := s.opt.Owner(r.Context())
	return owner != "" && strings.EqualFold(owner, login)
}

// origin is the page's origin, which passkeys are bound to: HTTPS at the
// name `tailscale serve` answers under.
func origin(r *http.Request) string { return "https://" + r.Host }

// rpID is the passkeys' relying party: the computer's tailnet name.
func rpID(r *http.Request) string {
	h := r.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.TrimSuffix(h, "."))
}

func sameOrigin(r *http.Request) bool {
	o, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && o.Host != "" && strings.EqualFold(o.Host, r.Host)
}

//go:embed page.html
var pageHTML string

var page = template.Must(template.New("page").Parse(pageHTML))

func (s *Server) servePage(w http.ResponseWriter, r *http.Request) {
	s.writePage(w, r, http.StatusOK, "")
}

// writePage writes the page; fail names an error it shows at once.
func (s *Server) writePage(w http.ResponseWriter, r *http.Request, status int, fail string) {
	lang, text := "en", map[string]string{}
	if s.opt.Text != nil {
		lang, text = s.opt.Text(r.Header.Get("Accept-Language"))
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src blob:; frame-ancestors 'none'")
	w.WriteHeader(status)
	page.Execute(w, struct {
		Lang string
		Text map[string]string
		Fail string
	}{lang, text, fail})
}

type apiErr struct {
	Error string `json:"error"`
}

func apiError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, apiErr{code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody)).Decode(v)
}

// The pairing: the page sends the link's token (begin), makes a passkey
// and sends its public key (finish), then asks until the person allowed or
// denied it in Pinky's window (status).

type pairBeginReq struct {
	Link string `json:"link"`
}

type pairBeginResp struct {
	ID        string `json:"id"`
	Challenge string `json:"challenge"`
	RPID      string `json:"rpId"`
	User      string `json:"user"`
}

func (s *Server) pairBegin(w http.ResponseWriter, r *http.Request) {
	var req pairBeginReq
	if readJSON(r, &req) != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	now := time.Now()
	s.mu.Lock()
	s.sweepLocked(now)
	if s.link == "" || req.Link == "" || !constEq(req.Link, s.link) {
		s.mu.Unlock()
		apiError(w, http.StatusForbidden, "link")
		return
	}
	s.link = "" // once
	ch := random(32)
	p := &pairing{Request: Request{ID: rand.Text(), Login: r.Header.Get("Tailscale-User-Login"), Created: now},
		challenge: ch, expires: now.Add(linkLife)}
	s.pairs[p.ID] = p
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, pairBeginResp{ID: p.ID, Challenge: b64.EncodeToString(ch), RPID: rpID(r),
		User: b64.EncodeToString(random(16))})
}

type pairFinishReq struct {
	ID           string `json:"id"`
	CredentialID string `json:"credentialId"`
	PublicKey    string `json:"publicKey"` // SPKI, base64url
	Alg          int    `json:"alg"`
	ClientData   string `json:"clientData"`
	AuthData     string `json:"authData"`
	Browser      string `json:"browser"`
}

type pairFinishResp struct {
	Code string `json:"code"`
}

func (s *Server) pairFinish(w http.ResponseWriter, r *http.Request) {
	var req pairFinishReq
	if readJSON(r, &req) != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	cred, e1 := b64.DecodeString(req.CredentialID)
	spki, e2 := b64.DecodeString(req.PublicKey)
	cd, e3 := b64.DecodeString(req.ClientData)
	ad, e4 := b64.DecodeString(req.AuthData)
	if errors.Join(e1, e2, e3, e4) != nil || len(cred) == 0 || len(cred) > 1024 {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	s.mu.Lock()
	s.sweepLocked(time.Now())
	p, ok := s.pairs[req.ID]
	if !ok || p.state != pairBegun || !strings.EqualFold(p.Login, r.Header.Get("Tailscale-User-Login")) {
		s.mu.Unlock()
		apiError(w, http.StatusForbidden, "gone")
		return
	}
	ch := p.challenge
	s.mu.Unlock()

	key, err := parseKey(spki)
	if err == nil && req.Alg != algES256 {
		err = errKey
	}
	if err == nil {
		err = checkClientData(cd, "webauthn.create", ch, origin(r))
	}
	if err == nil {
		err = checkAuthData(ad, rpID(r))
	}
	if err != nil {
		s.log.Warn("approvals in the browser: a passkey was refused", "err", err, "login", r.Header.Get("Tailscale-User-Login"))
		s.mu.Lock()
		delete(s.pairs, req.ID)
		s.mu.Unlock()
		apiError(w, http.StatusForbidden, "passkey")
		return
	}
	s.mu.Lock()
	if p.state != pairBegun {
		s.mu.Unlock()
		apiError(w, http.StatusForbidden, "gone")
		return
	}
	p.state, p.key, p.credID = pairWaiting, key, cred
	p.Browser = clip(req.Browser, 60)
	p.Code = code()
	c := p.Code
	s.mu.Unlock()
	s.log.Info("approvals in the browser: a browser asks to be added", "login", p.Login, "browser", p.Browser)
	s.changed()
	writeJSON(w, http.StatusOK, pairFinishResp{Code: c})
}

type pairStatusReq struct {
	ID string `json:"id"`
}

type pairStatusResp struct {
	State   string `json:"state"` // "waiting", "allowed", "denied", "gone"
	Session string `json:"session,omitempty"`
}

func (s *Server) pairStatus(w http.ResponseWriter, r *http.Request) {
	var req pairStatusReq
	if readJSON(r, &req) != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(time.Now())
	p, ok := s.pairs[req.ID]
	if !ok || !strings.EqualFold(p.Login, r.Header.Get("Tailscale-User-Login")) {
		writeJSON(w, http.StatusOK, pairStatusResp{State: "gone"})
		return
	}
	switch p.state {
	case pairAllowed:
		// The session token goes to the page once.
		delete(s.pairs, req.ID)
		writeJSON(w, http.StatusOK, pairStatusResp{State: "allowed", Session: p.session})
	case pairDenied:
		delete(s.pairs, req.ID)
		writeJSON(w, http.StatusOK, pairStatusResp{State: "denied"})
	default:
		writeJSON(w, http.StatusOK, pairStatusResp{State: "waiting"})
	}
}

// authed runs h for a request with an added phone's session token.
func (s *Server) authed(h func(http.ResponseWriter, *http.Request, [32]byte, *phone)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" {
			apiError(w, http.StatusUnauthorized, "session")
			return
		}
		hash := sha256.Sum256([]byte(tok))
		s.mu.Lock()
		p, ok := s.phones[hash]
		if ok {
			p.LastSeen = time.Now()
		}
		s.mu.Unlock()
		if !ok {
			apiError(w, http.StatusUnauthorized, "session")
			return
		}
		h(w, r, hash, p)
	}
}

// needView is a card on the page: what the panel shows.
type needView struct {
	ID         string         `json:"id"`
	Phone      string         `json:"phone"`
	Agent      string         `json:"agent"`
	Title      string         `json:"title"`
	Detail     string         `json:"detail,omitempty"`
	Rule       string         `json:"rule,omitempty"`
	App        string         `json:"app,omitempty"`
	Created    int64          `json:"created"`  // ms since the epoch
	Deadline   int64          `json:"deadline"` // ms since the epoch
	Answerable bool           `json:"answerable"`
	Shot       *shotView      `json:"shot,omitempty"`
	Target     *screen.Rect   `json:"target,omitempty"`
	Points     []screen.Point `json:"points,omitempty"` // the finger's: Point, the path, To
	Points2    []screen.Point `json:"points2,omitempty"`
}

type shotView struct {
	W int `json:"w"`
	H int `json:"h"`
}

type needsResp struct {
	Now   int64      `json:"now"`
	Needs []needView `json:"needs"`
}

func (s *Server) needs(w http.ResponseWriter, r *http.Request, _ [32]byte, _ *phone) {
	out := needsResp{Now: time.Now().UnixMilli(), Needs: []needView{}}
	for _, n := range s.opt.Source.Needs() {
		if n.Kind != gateway.NeedApproval {
			continue
		}
		v := needView{ID: n.ID, Phone: n.DeviceName, Agent: n.Agent, Title: n.Title, Detail: n.Detail, Rule: n.Rule, App: n.App,
			Created: n.Created.UnixMilli(), Deadline: n.Deadline.UnixMilli(), Answerable: Answerable(n)}
		if n.Screenshot != nil {
			b := n.Screenshot.Bounds()
			v.Shot = &shotView{W: b.Dx(), H: b.Dy()}
			if n.Target.W > 0 && n.Target.H > 0 {
				t := n.Target
				v.Target = &t
			}
			v.Points = fingerPoints(n.Point, n.Path, n.To)
			v.Points2 = fingerPoints(n.Point2, nil, n.To2)
		}
		out.Needs = append(out.Needs, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// fingerPoints lists where a finger goes: none when the action has no
// position.
func fingerPoints(p screen.Point, path []screen.Point, to screen.Point) []screen.Point {
	if len(path) > 0 {
		return path
	}
	if p == (screen.Point{}) {
		return nil
	}
	if to == (screen.Point{}) || to == p {
		return []screen.Point{p}
	}
	return []screen.Point{p, to}
}

type idReq struct {
	ID string `json:"id"`
}

// shot sends an approval's screenshot as JPEG.
func (s *Server) shot(w http.ResponseWriter, r *http.Request, _ [32]byte, _ *phone) {
	var req idReq
	if readJSON(r, &req) != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	n, ok := s.need(req.ID)
	if !ok || n.Screenshot == nil {
		apiError(w, http.StatusNotFound, "gone")
		return
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, n.Screenshot, &jpeg.Options{Quality: 80}); err != nil {
		apiError(w, http.StatusInternalServerError, "shot")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(buf.Bytes())
}

func (s *Server) need(id string) (gateway.NeedView, bool) {
	for _, n := range s.opt.Source.Needs() {
		if n.ID == id && n.Kind == gateway.NeedApproval {
			return n, true
		}
	}
	return gateway.NeedView{}, false
}

type challengeReq struct {
	ID       string `json:"id"`
	Decision string `json:"decision"` // "allow" or "deny"
}

type challengeResp struct {
	Challenge    string `json:"challenge"`
	CredentialID string `json:"credentialId"`
	RPID         string `json:"rpId"`
	Expires      int64  `json:"expires"` // ms since the epoch
}

// decisionOf maps the page's two answers to the gateway's.
func decisionOf(s string) (gateway.Decision, bool) {
	switch s {
	case "allow":
		return gateway.AllowOnce, true
	case "deny":
		return gateway.Deny, true
	}
	return "", false
}

// challenge issues the challenge that the phone's passkey signs to give
// one answer to one approval.
func (s *Server) challenge(w http.ResponseWriter, r *http.Request, hash [32]byte, p *phone) {
	var req challengeReq
	if readJSON(r, &req) != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	d, ok := decisionOf(req.Decision)
	if !ok {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	n, ok := s.need(req.ID)
	if !ok {
		apiError(w, http.StatusNotFound, "gone")
		return
	}
	if !Answerable(n) {
		apiError(w, http.StatusForbidden, "computer")
		return
	}
	now := time.Now()
	exp := now.Add(challengeLife)
	if n.Deadline.Before(exp) {
		exp = n.Deadline
	}
	v := random(32)
	k := b64.EncodeToString(v)
	s.mu.Lock()
	s.sweepLocked(now)
	if _, still := s.phones[hash]; !still {
		s.mu.Unlock()
		apiError(w, http.StatusUnauthorized, "session")
		return
	}
	mine := 0
	var oldest string
	for ck, c := range s.challenges {
		if c.phone == hash {
			mine++
			if oldest == "" || c.expires.Before(s.challenges[oldest].expires) {
				oldest = ck
			}
		}
	}
	if mine >= maxChallenges {
		delete(s.challenges, oldest)
	}
	s.challenges[k] = &challenge{phone: hash, need: n.ID, decision: d, value: v, expires: exp, deadline: n.Deadline}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, challengeResp{Challenge: k, CredentialID: b64.EncodeToString(p.credID), RPID: rpID(r),
		Expires: exp.UnixMilli()})
}

type decideReq struct {
	Challenge    string `json:"challenge"`
	CredentialID string `json:"credentialId"`
	ClientData   string `json:"clientData"`
	AuthData     string `json:"authData"`
	Signature    string `json:"signature"`
}

type decideResp struct {
	// Result is "done", or "gone": already answered elsewhere, or the time
	// ran out (Late says which).
	Result string `json:"result"`
	Late   bool   `json:"late,omitempty"`
}

// decide checks the passkey's signature over the challenge and gives the
// answer to the gateway, as the panel does.
func (s *Server) decide(w http.ResponseWriter, r *http.Request, hash [32]byte, p *phone) {
	var req decideReq
	if readJSON(r, &req) != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	cred, e1 := b64.DecodeString(req.CredentialID)
	cd, e2 := b64.DecodeString(req.ClientData)
	ad, e3 := b64.DecodeString(req.AuthData)
	sig, e4 := b64.DecodeString(req.Signature)
	if errors.Join(e1, e2, e3, e4) != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	now := time.Now()
	s.mu.Lock()
	c, ok := s.challenges[req.Challenge]
	if ok {
		delete(s.challenges, req.Challenge) // once, whatever comes of it
	}
	s.mu.Unlock()
	if !ok || c.phone != hash {
		apiError(w, http.StatusForbidden, "challenge")
		return
	}
	if now.After(c.expires) {
		writeJSON(w, http.StatusOK, decideResp{Result: "gone", Late: !now.Before(c.deadline)})
		return
	}
	if !bytes.Equal(cred, p.credID) {
		apiError(w, http.StatusForbidden, "passkey")
		return
	}
	if err := (assertion{ClientData: cd, AuthData: ad, Signature: sig}).verify(p.key, c.value, origin(r), rpID(r)); err != nil {
		s.log.Warn("approvals in the browser: an answer was refused", "err", err, "login", p.Login, "browser", p.Browser)
		apiError(w, http.StatusForbidden, "passkey")
		return
	}
	// Check again what the approval is: the same id stays the same
	// approval, but answer only what the page may.
	n, ok := s.need(c.need)
	if !ok {
		writeJSON(w, http.StatusOK, decideResp{Result: "gone", Late: !time.Now().Before(c.deadline)})
		return
	}
	if !Answerable(n) {
		apiError(w, http.StatusForbidden, "computer")
		return
	}
	if err := s.opt.Source.Decide(c.need, c.decision, ""); err != nil {
		writeJSON(w, http.StatusOK, decideResp{Result: "gone", Late: !time.Now().Before(c.deadline)})
		return
	}
	s.log.Info("approvals in the browser: answered", "approval", c.need, "decision", c.decision,
		"login", p.Login, "browser", p.Browser, "title", n.Title)
	writeJSON(w, http.StatusOK, decideResp{Result: "done"})
}

func random(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

// code is six random digits.
func code() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", n.Int64())
}

func constEq(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return ha == hb
}

func clip(s string, n int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
