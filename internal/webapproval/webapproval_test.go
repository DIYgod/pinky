package webapproval

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/internal/webmirror"
)

const (
	host  = "mac.tail1234.ts.net:8443"
	owner = "person@example.com"
)

// newGateway is the real gateway on a demo phone, as the gateway's own
// tests make it.
func newGateway(t *testing.T, timeout time.Duration) *gateway.Gateway {
	t.Helper()
	dev := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	pol := policy.Default()
	if err := pol.SetPreset(policy.PresetCommits); err != nil {
		t.Fatal(err)
	}
	g, err := gateway.New(gateway.Options{
		Providers: []driver.Provider{demo.NewProvider(driver.Android, dev)}, Policy: pol, TrustNewAgents: true,
		ApprovalTimeout: timeout, MaxBlock: 200 * time.Millisecond, SettleMax: 200 * time.Millisecond,
		PollInterval: 5 * time.Millisecond, DiscoverEvery: 20 * time.Millisecond, NoScreenshots: true, DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}

// ask has an agent ask the person a question and returns its approval id
// and a channel with the agent's answer.
func ask(t *testing.T, g *gateway.Gateway) (string, <-chan string) {
	t.Helper()
	ctx := context.Background()
	s := g.OpenSession(gateway.AgentInfo{Name: "Claude Code", Client: "test"})
	t.Cleanup(s.Close)
	if res := s.Connect(ctx, gateway.ConnectRequest{Task: "Lunch"}); res.IsError() {
		t.Fatalf("connect: %+v", res)
	}
	res := s.RequestApproval(ctx, gateway.ApprovalRequest{Summary: "May I order lunch?"})
	if res.Status != gateway.StatusPending {
		t.Fatalf("request: %+v", res)
	}
	out := make(chan string, 1)
	go func() {
		for {
			r := s.RequestApproval(ctx, gateway.ApprovalRequest{Summary: "May I order lunch?", ApprovalID: res.ApprovalID})
			if r.Status != gateway.StatusPending {
				out <- r.Decision
				return
			}
		}
	}()
	return res.ApprovalID, out
}

// harness is the page as the web mirror serves it, through a fake
// `tailscale serve`, and a phone with a software passkey.
type harness struct {
	t   *testing.T
	s   *Server
	srv *httptest.Server
	// What the requests carry, as `tailscale serve` would set them.
	login   string
	proxied bool
	key     *ecdsa.PrivateKey
	credID  []byte
	session string
	uv      bool
}

func newHarness(t *testing.T, src Source) *harness {
	h := &harness{t: t, login: owner, proxied: true, uv: true, credID: []byte("cred-1")}
	h.key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	h.s = New(Options{Source: src, Owner: func(context.Context) string { return owner },
		Proxied: webmirror.Proxied})
	h.s.Set(true)
	m := webmirror.New(webmirror.Options{Source: nil, OwnNames: func(context.Context) []string { return []string{"mac.tail1234.ts.net"} },
		Approvals: h.s.Handler()})
	h.srv = httptest.NewServer(m.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) post(path string, body any, out any) int {
	h.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", h.srv.URL+Path+"/api/"+path, bytes.NewReader(b))
	h.headers(req)
	req.Header.Set("Origin", "https://"+host)
	if h.session != "" {
		req.Header.Set("Authorization", "Bearer "+h.session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil {
		json.Unmarshal(data, out)
	}
	return resp.StatusCode
}

func (h *harness) headers(req *http.Request) {
	req.Host = host
	if h.proxied {
		req.Header.Set("X-Forwarded-For", "100.64.0.2")
	} else {
		// Straight to the mirror's address on this computer.
		req.Host = "127.0.0.1:7466"
	}
	if h.login != "" {
		req.Header.Set("Tailscale-User-Login", h.login)
	}
}

func (h *harness) clientData(typ string, ch []byte) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": b64.EncodeToString(ch), "origin": "https://" + host})
	return b
}

func (h *harness) authData() []byte {
	rp := sha256.Sum256([]byte("mac.tail1234.ts.net"))
	flags := byte(flagUserPresent)
	if h.uv {
		flags |= flagUserVerified
	}
	return append(rp[:], flags, 0, 0, 0, 1)
}

// pair adds the harness's phone, the person allowing it in Pinky's window.
func (h *harness) pair() {
	h.t.Helper()
	var begun pairBeginResp
	if c := h.post("pair/begin", pairBeginReq{Link: h.s.NewLink()}, &begun); c != 200 {
		h.t.Fatalf("begin: %d", c)
	}
	var fin pairFinishResp
	if c := h.post("pair/finish", h.finishReq(begun), &fin); c != 200 || len(fin.Code) != 6 {
		h.t.Fatalf("finish: %d %+v", c, fin)
	}
	reqs := h.s.Requests()
	if len(reqs) != 1 || reqs[0].Code != fin.Code || reqs[0].Login != owner || reqs[0].Browser != "iPhone · Safari" {
		h.t.Fatalf("requests %+v, code %s", reqs, fin.Code)
	}
	var st pairStatusResp
	if h.post("pair/status", pairStatusReq{ID: begun.ID}, &st); st.State != "waiting" {
		h.t.Fatalf("status before Allow: %+v", st)
	}
	if err := h.s.Allow(reqs[0].ID); err != nil {
		h.t.Fatal(err)
	}
	if h.post("pair/status", pairStatusReq{ID: begun.ID}, &st); st.State != "allowed" || st.Session == "" {
		h.t.Fatalf("status after Allow: %+v", st)
	}
	h.session = st.Session
	// Once.
	if h.post("pair/status", pairStatusReq{ID: begun.ID}, &st); st.State != "gone" {
		h.t.Fatalf("status again: %+v", st)
	}
}

func (h *harness) finishReq(begun pairBeginResp) pairFinishReq {
	ch, _ := b64.DecodeString(begun.Challenge)
	spki, _ := x509.MarshalPKIXPublicKey(&h.key.PublicKey)
	return pairFinishReq{ID: begun.ID, CredentialID: b64.EncodeToString(h.credID), PublicKey: b64.EncodeToString(spki),
		Alg: algES256, ClientData: b64.EncodeToString(h.clientData("webauthn.create", ch)),
		AuthData: b64.EncodeToString(h.authData()), Browser: "iPhone · Safari"}
}

// sign makes the assertion the phone's passkey would for a challenge.
func (h *harness) sign(ch challengeResp) decideReq {
	v, _ := b64.DecodeString(ch.Challenge)
	cd := h.clientData("webauthn.get", v)
	ad := h.authData()
	sum := sha256.Sum256(cd)
	d := sha256.Sum256(append(append([]byte(nil), ad...), sum[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, h.key, d[:])
	return decideReq{Challenge: ch.Challenge, CredentialID: b64.EncodeToString(h.credID), ClientData: b64.EncodeToString(cd),
		AuthData: b64.EncodeToString(ad), Signature: b64.EncodeToString(sig)}
}

func (h *harness) challenge(id, d string) challengeResp {
	h.t.Helper()
	var ch challengeResp
	if c := h.post("challenge", challengeReq{ID: id, Decision: d}, &ch); c != 200 {
		h.t.Fatalf("challenge: %d", c)
	}
	return ch
}

func TestAnswerFromThePhone(t *testing.T) {
	for _, tc := range []struct{ decision, want string }{{"allow", "allow"}, {"deny", "deny"}} {
		t.Run(tc.decision, func(t *testing.T) {
			g := newGateway(t, 30*time.Second)
			h := newHarness(t, g)
			h.pair()
			id, answer := ask(t, g)

			var needs needsResp
			if c := h.post("needs", nil, &needs); c != 200 || len(needs.Needs) != 1 {
				t.Fatalf("needs: %d %+v", c, needs)
			}
			n := needs.Needs[0]
			if n.ID != id || !n.Answerable || n.Title == "" || n.Agent != "Claude Code" || n.Phone == "" || n.Deadline <= needs.Now {
				t.Fatalf("card %+v", n)
			}
			var res decideResp
			if c := h.post("decide", h.sign(h.challenge(id, tc.decision)), &res); c != 200 || res.Result != "done" {
				t.Fatalf("decide: %d %+v", c, res)
			}
			select {
			case got := <-answer:
				if got != tc.want {
					t.Fatalf("agent heard %q", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the agent heard nothing")
			}
		})
	}
}

func TestRefused(t *testing.T) {
	g := newGateway(t, 30*time.Second)

	t.Run("not through Tailscale", func(t *testing.T) {
		h := newHarness(t, g)
		h.proxied, h.login = false, ""
		if c := h.post("pair/begin", pairBeginReq{Link: h.s.NewLink()}, nil); c != http.StatusForbidden {
			t.Fatalf("got %d", c)
		}
		req, _ := http.NewRequest("GET", h.srv.URL+Path, nil)
		h.headers(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("page: %d", resp.StatusCode)
		}
	})
	t.Run("another tailnet user", func(t *testing.T) {
		h := newHarness(t, g)
		h.login = "someone@example.com"
		if c := h.post("pair/begin", pairBeginReq{Link: h.s.NewLink()}, nil); c != http.StatusForbidden {
			t.Fatalf("got %d", c)
		}
	})
	t.Run("off", func(t *testing.T) {
		h := newHarness(t, g)
		link := h.s.NewLink()
		h.s.Set(false)
		if c := h.post("pair/begin", pairBeginReq{Link: link}, nil); c != http.StatusNotFound {
			t.Fatalf("got %d", c)
		}
	})
	t.Run("Funnel", func(t *testing.T) {
		h := newHarness(t, g)
		req, _ := http.NewRequest("GET", h.srv.URL+Path, nil)
		h.headers(req)
		req.Header.Set("Tailscale-Funnel-Request", "?1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("got %d", resp.StatusCode)
		}
	})
	t.Run("link used twice or made up", func(t *testing.T) {
		h := newHarness(t, g)
		link := h.s.NewLink()
		if c := h.post("pair/begin", pairBeginReq{Link: link}, nil); c != 200 {
			t.Fatalf("first: %d", c)
		}
		if c := h.post("pair/begin", pairBeginReq{Link: link}, nil); c != http.StatusForbidden {
			t.Fatalf("again: %d", c)
		}
		if c := h.post("pair/begin", pairBeginReq{Link: "AAAA"}, nil); c != http.StatusForbidden {
			t.Fatalf("made up: %d", c)
		}
	})
	t.Run("passkey without user verification", func(t *testing.T) {
		h := newHarness(t, g)
		h.uv = false
		var begun pairBeginResp
		h.post("pair/begin", pairBeginReq{Link: h.s.NewLink()}, &begun)
		if c := h.post("pair/finish", h.finishReq(begun), nil); c != http.StatusForbidden || len(h.s.Requests()) != 0 {
			t.Fatalf("got %d, requests %+v", c, h.s.Requests())
		}
	})
	t.Run("turned down in Pinky's window", func(t *testing.T) {
		h := newHarness(t, g)
		var begun pairBeginResp
		h.post("pair/begin", pairBeginReq{Link: h.s.NewLink()}, &begun)
		h.post("pair/finish", h.finishReq(begun), nil)
		h.s.Deny(h.s.Requests()[0].ID)
		var st pairStatusResp
		if h.post("pair/status", pairStatusReq{ID: begun.ID}, &st); st.State != "denied" || st.Session != "" || len(h.s.Phones()) != 0 {
			t.Fatalf("status %+v, phones %+v", st, h.s.Phones())
		}
	})
	t.Run("no session", func(t *testing.T) {
		h := newHarness(t, g)
		if c := h.post("needs", nil, nil); c != http.StatusUnauthorized {
			t.Fatalf("got %d", c)
		}
		h.session = "AAAAAAAAAAAAAAAAAAAAAAAAAA"
		if c := h.post("needs", nil, nil); c != http.StatusUnauthorized {
			t.Fatalf("made up: %d", c)
		}
	})
	t.Run("another origin", func(t *testing.T) {
		h := newHarness(t, g)
		b, _ := json.Marshal(pairBeginReq{Link: h.s.NewLink()})
		req, _ := http.NewRequest("POST", h.srv.URL+Path+"/api/pair/begin", bytes.NewReader(b))
		h.headers(req)
		req.Header.Set("Origin", "https://evil.example")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("got %d", resp.StatusCode)
		}
	})
}

func TestAnswersThatDoNotCount(t *testing.T) {
	g := newGateway(t, 30*time.Second)
	h := newHarness(t, g)
	h.pair()
	id, answer := ask(t, g)

	// Without user verification: a tap on the page by whoever drives the
	// phone, without Face ID.
	h.uv = false
	if c := h.post("decide", h.sign(h.challenge(id, "allow")), nil); c != http.StatusForbidden {
		t.Fatalf("no UV: %d", c)
	}
	h.uv = true

	// Another key: a passkey the person never added.
	ch := h.challenge(id, "allow")
	good := h.key
	h.key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if c := h.post("decide", h.sign(ch), nil); c != http.StatusForbidden {
		t.Fatalf("other key: %d", c)
	}
	h.key = good
	// That challenge went with the failed try.
	if c := h.post("decide", h.sign(ch), nil); c != http.StatusForbidden {
		t.Fatalf("used challenge: %d", c)
	}

	// Another phone's session cannot use this phone's challenge.
	other := newHarness(t, g)
	other.s = h.s
	other.srv = h.srv
	other.credID = []byte("cred-2")
	other.key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	other.pair()
	ch = h.challenge(id, "allow")
	if c := other.post("decide", other.sign(ch), nil); c != http.StatusForbidden {
		t.Fatalf("other phone: %d", c)
	}

	// A challenge for Deny answers Deny, whatever the page says.
	var res decideResp
	if c := h.post("decide", h.sign(h.challenge(id, "deny")), &res); c != 200 || res.Result != "done" {
		t.Fatalf("decide: %d %+v", c, res)
	}
	if got := <-answer; got != "deny" {
		t.Fatalf("agent heard %q", got)
	}

	// Removed in Settings: the session no longer works.
	h.s.Remove(h.s.Phones()[0].ID)
	if c := h.post("needs", nil, nil); c != http.StatusUnauthorized {
		t.Fatalf("after Remove: %d", c)
	}
}

func TestTwoPlacesAnswer(t *testing.T) {
	g := newGateway(t, 30*time.Second)
	h := newHarness(t, g)
	h.pair()
	id, answer := ask(t, g)
	ch := h.challenge(id, "allow")
	// The person answers in Pinky's window first.
	if err := g.Decide(id, gateway.Deny, ""); err != nil {
		t.Fatal(err)
	}
	var res decideResp
	if c := h.post("decide", h.sign(ch), &res); c != 200 || res.Result != "gone" || res.Late {
		t.Fatalf("decide: %d %+v", c, res)
	}
	if got := <-answer; got != "deny" {
		t.Fatalf("agent heard %q", got)
	}
}

func TestTimeoutUnchanged(t *testing.T) {
	g := newGateway(t, time.Second)
	h := newHarness(t, g)
	h.pair()
	id, answer := ask(t, g)
	ch := h.challenge(id, "allow")
	if ch.Expires > time.Now().Add(time.Second).UnixMilli()+50 {
		t.Fatalf("challenge outlives the approval: %d", ch.Expires)
	}
	if got := <-answer; got != "deny" {
		t.Fatalf("agent heard %q", got)
	}
	var res decideResp
	if c := h.post("decide", h.sign(ch), &res); c != 200 || res.Result != "gone" || !res.Late {
		t.Fatalf("decide: %d %+v", c, res)
	}
}

func TestAnswerable(t *testing.T) {
	ok := gateway.NeedView{Kind: gateway.NeedApproval, DeviceID: "pixel", Rule: "Payments"}
	if !Answerable(ok) {
		t.Fatal("payment")
	}
	for name, n := range map[string]gateway.NeedView{
		"new agent": {Kind: gateway.NeedApproval, Rule: "New agent"},
		"unblock":   {Kind: gateway.NeedApproval, DeviceID: "pixel", Rule: "Finance apps", Unblock: true},
		"handoff":   {Kind: gateway.NeedHandoff, DeviceID: "pixel"},
		"unlock":    {Kind: gateway.NeedUnlock, DeviceID: "pixel"},
		"setup":     {Kind: gateway.NeedSetup, DeviceID: "pixel"},
	} {
		if Answerable(n) {
			t.Errorf("%s answerable", name)
		}
	}
}

// fakeSource holds cards the page cannot answer, and one with a
// screenshot.
type fakeSource struct{ needs []gateway.NeedView }

func (f *fakeSource) Needs() []gateway.NeedView { return f.needs }
func (f *fakeSource) Decide(string, gateway.Decision, string) error {
	panic("the page answered what it may not")
}

func TestCardsAndComputerOnly(t *testing.T) {
	src := &fakeSource{needs: []gateway.NeedView{
		{ID: "ap_1", Kind: gateway.NeedApproval, Rule: "New agent", Title: "Let Codex use your phones?", Deadline: time.Now().Add(time.Minute)},
		{ID: "ap_2", Kind: gateway.NeedApproval, DeviceID: "pixel", Rule: "Finance apps", Unblock: true, Title: "Open Bank?",
			Deadline: time.Now().Add(time.Minute), Screenshot: image.NewRGBA(image.Rect(0, 0, 90, 160)),
			Target: screen.Rect{X: 10, Y: 20, W: 30, H: 40}, Point: screen.Point{X: 25, Y: 40}},
		{ID: "unlock:pixel", Kind: gateway.NeedUnlock, DeviceID: "pixel"},
	}}
	h := newHarness(t, src)
	h.pair()
	var needs needsResp
	h.post("needs", nil, &needs)
	if len(needs.Needs) != 2 || needs.Needs[0].Answerable || needs.Needs[1].Answerable {
		t.Fatalf("needs %+v", needs.Needs)
	}
	n := needs.Needs[1]
	if n.Shot == nil || n.Shot.W != 90 || n.Target == nil || n.Target.W != 30 || len(n.Points) != 1 {
		t.Fatalf("card %+v", n)
	}
	for _, id := range []string{"ap_1", "ap_2"} {
		var e apiErr
		if c := h.post("challenge", challengeReq{ID: id, Decision: "allow"}, &e); c != http.StatusForbidden || e.Error != "computer" {
			t.Fatalf("%s: %d %+v", id, c, e)
		}
	}
	// The screenshot, as JPEG.
	b, _ := json.Marshal(idReq{ID: "ap_2"})
	req, _ := http.NewRequest("POST", h.srv.URL+Path+"/api/shot", bytes.NewReader(b))
	h.headers(req)
	req.Header.Set("Origin", "https://"+host)
	req.Header.Set("Authorization", "Bearer "+h.session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("shot: %d %v", resp.StatusCode, resp.Header)
	}
}

func TestPageText(t *testing.T) {
	h := newHarness(t, &fakeSource{})
	h.s.opt.Text = func(string) (string, map[string]string) {
		return "ja", map[string]string{"title": "Pinky の承認"}
	}
	req, _ := http.NewRequest("GET", h.srv.URL+Path, nil)
	h.headers(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `<html lang="ja">`) || !strings.Contains(string(body), "Pinky の承認") ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("page: %d %s", resp.StatusCode, body[:200])
	}
}
