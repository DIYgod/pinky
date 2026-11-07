package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/mcp"
)

// remoteAgents serves MCP over Streamable HTTP while the person has remote
// agents on (Settings › Remote agents, or `pinky daemon --http`). It is off
// by default and listens on this computer only unless the person picks
// another address.
type remoteAgents struct {
	mcp   *mcp.Server
	token string
	log   *slog.Logger

	mu   sync.Mutex
	addr string
	ln   net.Listener
	srv  *http.Server
	h    *mcp.HTTP
}

func newRemoteAgents(m *mcp.Server, token string, log *slog.Logger) *remoteAgents {
	return &remoteAgents{mcp: m, token: token, log: log}
}

// Set starts, moves or stops the HTTP endpoint.
func (r *remoteAgents) Set(on bool, addr string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	addr = strings.TrimSpace(addr)
	if on && r.srv != nil && addr == r.addr {
		return nil
	}
	r.stopLocked()
	if !on {
		return nil
	}
	if addr == "" {
		return errors.New("remote agents: no address")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("remote agents: listen on %s: %w", addr, err)
	}
	h := r.mcp.HTTP(r.token)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	r.addr, r.ln, r.srv, r.h = addr, ln, srv, h
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.log.Warn("remote agents stopped", "addr", addr, "err", err)
		}
	}()
	r.log.Info("remote agents on", "url", r.urlLocked())
	if !loopback(ln.Addr()) {
		r.log.Warn("remote agents are reachable from other computers; anyone with the agent token can use the phones", "addr", ln.Addr().String())
	}
	return nil
}

// URL is where agents connect, or "" while off.
func (r *remoteAgents) URL() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.urlLocked()
}

func (r *remoteAgents) urlLocked() string {
	if r.ln == nil {
		return ""
	}
	return "http://" + r.ln.Addr().String() + mcp.HTTPPath
}

// Close stops the endpoint and ends its sessions.
func (r *remoteAgents) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
}

func (r *remoteAgents) stopLocked() {
	if r.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r.srv.Shutdown(ctx)
	// Shutdown waits for requests, not for MCP sessions, which outlive
	// them: end those, with their leases.
	r.h.CloseSessions()
	r.log.Info("remote agents off", "addr", r.addr)
	r.addr, r.ln, r.srv, r.h = "", nil, nil, nil
}

func loopback(a net.Addr) bool {
	tcp, ok := a.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}
