package standalone

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/pilot"
	"github.com/DIYgod/pinky/internal/policy"
)

// The control socket is how Pinky's own screens reach the core: line
// JSON, one request a line, one reply a line. Only the prototype's own
// uid may connect (peerAllowed): the agent runs in the core and reaches the
// phone through MCP tools only, so it can never send these commands.
//
//	{"cmd":"state"}                                     → {"ok":true,"state":{...}}
//	{"cmd":"subscribe"}                                 → the state now and after each change, one a line
//	{"cmd":"start","task":"...","key":"...",...}        → {"ok":true}  (StartRequest)
//	{"cmd":"decide","need":"...","decision":"allow_once"} → {"ok":true}
//	{"cmd":"preset","preset":1}                         → {"ok":true}  (policy.Preset)
//	{"cmd":"stop"} / {"cmd":"dismiss"}                  → {"ok":true}
//	{"cmd":"stop","computer":true}                      → {"ok":true}  (StopComputer)
//	{"cmd":"history","before":<ms>,"limit":20}          → {"ok":true,"history":[...],"more":true}
//	                                                      (History: PastTask, newest first; both fields optional)
//	{"cmd":"history","id":"..."}                        → {"ok":true,"history":[the task, with its steps]}
//
// A failed command replies {"ok":false,"error":"...","code":"..."}: the
// code, when there is one, names an error the screens say in the person's
// language (the Code constants); the error is for logs.

type request struct {
	Cmd      string `json:"cmd"`
	Need     string `json:"need"`
	Decision string `json:"decision"`
	Note     string `json:"note"`
	Preset   int    `json:"preset"`
	Computer bool   `json:"computer"` // stop: Pinky on a computer, not the task
	// history: one task, or the tasks that started before Before (Unix
	// milliseconds), Limit of them.
	ID     string `json:"id"`
	Before int64  `json:"before"`
	Limit  int    `json:"limit"`
	StartRequest
}

type reply struct {
	OK      bool       `json:"ok"`
	Error   string     `json:"error,omitempty"`
	Code    string     `json:"code,omitempty"`
	State   *State     `json:"state,omitempty"`
	History []PastTask `json:"history,omitempty"`
	More    bool       `json:"more,omitempty"` // older tasks follow
}

// historyWait bounds a history command, which reads the database.
const historyWait = 10 * time.Second

// The codes of errors the screens know.
const (
	CodeNoDevice = "no_device" // ErrNoDevice: the service is off
	CodeNoKey    = "no_key"    // ErrNoKey
	CodeNoModel  = "no_model"  // an OpenAI-compatible API without a model
	CodeNoTask   = "no_task"   // an empty task
	CodeBusy     = "busy"      // a task already runs
	CodeComputer = "computer"  // ErrComputer: Pinky on a computer uses the phone
	CodeGone     = "gone"      // ErrGone: the task is no longer in the history
)

// codeOf is the code of a command's error, or "" for one the screens show
// as it is.
func codeOf(err error) string {
	switch {
	case errors.Is(err, ErrNoDevice):
		return CodeNoDevice
	case errors.Is(err, ErrNoKey):
		return CodeNoKey
	case errors.Is(err, pilot.ErrNoModel):
		return CodeNoModel
	case errors.Is(err, pilot.ErrNoTask):
		return CodeNoTask
	case errors.Is(err, pilot.ErrBusy):
		return CodeBusy
	case errors.Is(err, ErrComputer):
		return CodeComputer
	case errors.Is(err, ErrGone):
		return CodeGone
	}
	return ""
}

// Serve answers the control socket until ctx ends. On the phone, ln
// listens at an abstract address (net.Listen("unix", "@name")).
func (c *Core) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		if err := peerAllowed(conn); err != nil {
			c.log.Warn("control: refused a connection", "err", err)
			conn.Close()
			continue
		}
		go c.serveConn(ctx, conn)
	}
}

func (c *Core) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	in := bufio.NewScanner(conn)
	in.Buffer(make([]byte, 64<<10), 1<<20)
	enc := json.NewEncoder(conn)
	for in.Scan() {
		var r request
		if err := json.Unmarshal(in.Bytes(), &r); err != nil {
			_ = enc.Encode(reply{Error: "bad request: " + err.Error()})
			continue
		}
		if r.Cmd == "subscribe" {
			c.stream(ctx, enc)
			return
		}
		rep := c.handle(r)
		if err := enc.Encode(rep); err != nil {
			return
		}
	}
}

func (c *Core) handle(r request) reply {
	var err error
	switch r.Cmd {
	case "state":
		st := c.State()
		return reply{OK: true, State: &st}
	case "start":
		err = c.Start(r.StartRequest)
	case "decide":
		err = c.Decide(r.Need, gateway.Decision(r.Decision), r.Note)
	case "preset":
		err = c.SetPreset(policy.Preset(r.Preset))
	case "stop":
		if r.Computer {
			err = c.StopComputer()
		} else {
			c.Stop()
		}
	case "dismiss":
		c.Dismiss()
	case "history":
		return c.history(r)
	default:
		err = fmt.Errorf("unknown command %q", r.Cmd)
	}
	if err != nil {
		return reply{Error: err.Error(), Code: codeOf(err)}
	}
	return reply{OK: true}
}

func (c *Core) history(r request) reply {
	ctx, cancel := context.WithTimeout(c.ctx, historyWait)
	defer cancel()
	if r.ID != "" {
		t, err := c.PastTask(ctx, r.ID)
		if err != nil {
			return reply{Error: err.Error(), Code: codeOf(err)}
		}
		return reply{OK: true, History: []PastTask{t}}
	}
	var before time.Time
	if r.Before > 0 {
		before = time.UnixMilli(r.Before)
	}
	list, more, err := c.History(ctx, before, r.Limit)
	if err != nil {
		return reply{Error: err.Error(), Code: codeOf(err)}
	}
	return reply{OK: true, History: list, More: more}
}

// stream sends the state now and after each change, at most every 100 ms,
// until the reader goes away.
func (c *Core) stream(ctx context.Context, enc *json.Encoder) {
	ch, done := c.subscribe()
	defer done()
	for {
		st := c.State()
		if err := enc.Encode(reply{OK: true, State: &st}); err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ch:
		case <-time.After(time.Second): // deadlines and elapsed time move on their own
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
