package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/update"
)

// shimSession forwards an agent's MCP session (newline-delimited JSON-RPC
// on stdio) to the gateway's socket. When the gateway goes away because
// the app restarts for an update (update.Restarting), it keeps the agent's
// side open, waits for the new version, connects again and replays the
// agent's initialize and initialized, so the agent's session goes on.
// Requests the old gateway did not answer get an error to retry.
type shimSession struct {
	dirs  api.Dirs
	token string
	out   io.Writer
	// wait is how long to wait for the new version; tests shorten it.
	wait time.Duration

	mu           sync.Mutex
	cond         *sync.Cond
	conn         net.Conn                  // nil while reconnecting
	closed       bool                      // gave up: writes fail
	init, inited []byte                    // the agent's initialize request and initialized notification
	pending      map[string]pendingRequest // by id
	replayID     string
	agentDone    bool
	seq          int
	outMu        sync.Mutex
}

func newShimSession(dirs api.Dirs, token string, out io.Writer) *shimSession {
	s := &shimSession{dirs: dirs, token: token, out: out, wait: 3 * time.Minute, pending: map[string]pendingRequest{}}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// greetMCP says hello on a new connection as an MCP client.
func greetMCP(conn net.Conn, token string) (*bufio.Reader, error) {
	br, _, err := api.Greet(conn, api.Hello{Role: api.RoleMCP, Token: token})
	return br, err
}

// run forwards until the agent closes stdin and the gateway ends the
// session, or the gateway goes away for good.
func (s *shimSession) run(conn net.Conn, br *bufio.Reader, stdin io.Reader) error {
	s.setConn(conn)
	go s.fromAgent(stdin)
	for {
		err := s.toAgent(br)
		s.mu.Lock()
		done := s.agentDone
		s.conn = nil
		s.mu.Unlock()
		conn.Close()
		if done || !update.Restarting(s.dirs.Config) {
			s.giveUp()
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				return err
			}
			return nil
		}
		if conn, br, err = s.reconnect(); err != nil {
			s.giveUp()
			if errors.Is(err, errAgentGone) {
				return nil
			}
			return err
		}
	}
}

// errAgentGone ends a reconnect: the agent closed its side meanwhile.
var errAgentGone = errors.New("the agent ended the session")

func (s *shimSession) setConn(c net.Conn) {
	s.mu.Lock()
	s.conn = c
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *shimSession) giveUp() {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

// pendingRequest is a request sent to the gateway and not answered yet.
type pendingRequest struct {
	method string
	line   []byte
}

// jsonrpcHead is what the shim reads of a message.
type jsonrpcHead struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func head(line []byte) (jsonrpcHead, bool) {
	var h jsonrpcHead
	if json.Unmarshal(line, &h) != nil {
		return h, false
	}
	return h, true
}

// fromAgent forwards the agent's messages, noting its initialize and the
// requests not yet answered, and waits while the gateway reconnects.
func (s *shimSession) fromAgent(stdin io.Reader) {
	r := bufio.NewReaderSize(stdin, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			request, method := "", ""
			if h, ok := head(line); ok {
				s.mu.Lock()
				switch {
				case h.Method == "initialize":
					s.init = append([]byte(nil), line...)
				case h.Method == "notifications/initialized":
					s.inited = append([]byte(nil), line...)
				}
				s.mu.Unlock()
				if len(h.ID) > 0 && h.Method != "" {
					request, method = string(h.ID), h.Method
				}
			}
			if !s.send(line, request, method) {
				return
			}
		}
		if err != nil {
			break
		}
	}
	// The agent closed stdin: let the gateway end the session.
	s.mu.Lock()
	s.agentDone = true
	c := s.conn
	s.mu.Unlock()
	if uc, ok := c.(*net.UnixConn); ok {
		uc.CloseWrite()
	} else if c != nil {
		c.Close()
	}
}

// send writes one line to the gateway, waiting out a reconnect, and notes
// a request (its id) as unanswered on that connection; false once the
// shim gave up. Nothing is sent while there is no connection, so what is
// pending when the gateway goes away went to the old one.
func (s *shimSession) send(line []byte, request, method string) bool {
	s.mu.Lock()
	for s.conn == nil && !s.closed {
		s.cond.Wait()
	}
	c, closed := s.conn, s.closed
	if !closed && request != "" {
		s.pending[request] = pendingRequest{method: method, line: append([]byte(nil), line...)}
	}
	s.mu.Unlock()
	if closed {
		return false
	}
	// A write that fails means the gateway went away; its request stays
	// pending and is answered with an error once the shim reconnects.
	c.Write(line)
	return true
}

// toAgent forwards the gateway's messages to stdout until its connection
// ends.
func (s *shimSession) toAgent(br *bufio.Reader) error {
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if h, ok := head(line); ok && len(h.ID) > 0 && h.Method == "" {
				s.mu.Lock()
				delete(s.pending, string(h.ID))
				s.mu.Unlock()
			}
			s.write(line)
		}
		if err != nil {
			return err
		}
	}
}

func (s *shimSession) write(line []byte) {
	s.outMu.Lock()
	s.out.Write(line)
	s.outMu.Unlock()
}

// reconnect waits for the new version's gateway, says hello and replays
// the agent's initialize, swallowing its answer.
func (s *shimSession) reconnect() (net.Conn, *bufio.Reader, error) {
	deadline := time.Now().Add(s.wait)
	for {
		s.mu.Lock()
		gone := s.agentDone
		s.mu.Unlock()
		if gone {
			return nil, nil, errAgentGone
		}
		conn, err := api.Dial(s.dirs)
		if err == nil {
			br, err := greetMCP(conn, s.token)
			if err == nil {
				if err = s.replay(conn, br); err == nil {
					s.settlePending(conn)
					s.setConn(conn)
					return conn, br, nil
				}
			}
			conn.Close()
		}
		if time.Now().After(deadline) {
			return nil, nil, fmt.Errorf("Pinky did not come back within %s of restarting for an update", s.wait)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (s *shimSession) replay(conn net.Conn, br *bufio.Reader) error {
	s.mu.Lock()
	init, inited := s.init, s.inited
	s.seq++
	id := fmt.Sprintf("pinky-reconnect-%d", s.seq)
	s.mu.Unlock()
	if init == nil {
		return nil // the agent had not started its session
	}
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(init, &msg); err != nil {
		return err
	}
	msg["id"], _ = json.Marshal(id)
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	defer conn.SetDeadline(time.Time{})
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return err
	}
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return err
		}
		if h, ok := head(line); ok && string(h.ID) == `"`+id+`"` {
			break
		}
	}
	if inited != nil {
		if _, err := conn.Write(inited); err != nil {
			return err
		}
	}
	return nil
}

// settlePending deals with what the old gateway left unanswered, most
// often a request the agent sent as it went away: requests that only read
// are sent again to the new one; a tool call may have acted on a phone, so
// the agent gets an error and decides whether to call it again.
func (s *shimSession) settlePending(conn net.Conn) {
	s.mu.Lock()
	var resend [][]byte
	var failed []string
	for id, p := range s.pending {
		if p.method == "tools/call" || p.method == "initialize" {
			failed = append(failed, id)
			delete(s.pending, id)
		} else {
			resend = append(resend, p.line)
		}
	}
	s.mu.Unlock()
	for _, id := range failed {
		s.write([]byte(`{"jsonrpc":"2.0","id":` + id + `,"error":{"code":-32603,"message":"Pinky restarted to update itself; call the tool again"}}` + "\n"))
	}
	for _, line := range resend {
		conn.Write(line)
	}
}
