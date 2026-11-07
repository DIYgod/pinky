package mcp

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPPath is where the Streamable HTTP endpoint is served.
const HTTPPath = "/mcp"

// httpSessionTimeout ends HTTP sessions that sent nothing for this long,
// and with them their leases; stdio sessions end when the agent exits.
const httpSessionTimeout = time.Hour

// HTTP serves MCP over Streamable HTTP (round three, decision 6), for
// agents that cannot start `pinky mcp`: on another machine, in a
// container, or a client that speaks only HTTP. Every request needs the
// agent token as a bearer token. Each MCP session is one gateway session,
// with the same first-connection approval and scopes as over stdio, under
// a name of its own (RemoteSuffix). The SDK refuses requests to a loopback
// address that name another host (DNS rebinding), and cross-origin
// browser requests are refused too.
type HTTP struct {
	srv     *sdk.Server
	handler http.Handler
	token   string
}

// HTTP returns the Streamable HTTP endpoint for the server's tools, at
// HTTPPath, accepting token.
func (s *Server) HTTP(token string) *HTTP {
	// A server of its own, so turning HTTP off can end exactly its
	// sessions, and its agents are named apart from local ones.
	srv := s.newSDK(true)
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{SessionTimeout: httpSessionTimeout})
	mux := http.NewServeMux()
	mux.Handle(HTTPPath, http.NewCrossOriginProtection().Handler(h))
	return &HTTP{srv: srv, handler: mux, token: token}
}

// ServeHTTP checks the bearer token, then serves MCP.
func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="pinky"`)
		http.Error(w, "Pinky needs the agent token: send Authorization: Bearer <token>.", http.StatusUnauthorized)
		return
	}
	h.handler.ServeHTTP(w, r)
}

func (h *HTTP) authorized(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	scheme, tok, ok := strings.Cut(auth, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || h.token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(tok)), []byte(h.token)) == 1
}

// CloseSessions ends every HTTP session, with their leases and approvals,
// as when the person turns remote agents off.
func (h *HTTP) CloseSessions() {
	var all []*sdk.ServerSession
	for ss := range h.srv.Sessions() {
		all = append(all, ss)
	}
	for _, ss := range all {
		ss.Close()
	}
}
