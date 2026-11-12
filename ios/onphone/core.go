// Package onphone is Pinky on the iPhone it drives: the gateway, the
// rules, approvals, history and the agent of internal/standalone, with the
// iPhone driver over the phone's own loopback as its one phone.
//
// The iPhone app's packet-tunnel extension hosts it (ios/core), or the app
// itself where the extension cannot be signed. Pinky's screens reach it
// with Call, one JSON request and one JSON reply, through the extension's
// app messages or directly. The commands are internal/standalone's
// control commands, which Call answers itself instead of a socket, and a
// few of the iPhone's own:
//
//	{"cmd":"state"}                         → {"ok":true,"rev":7,"state":{...},"reason":"..."}  (standalone.State)
//	{"cmd":"wait","rev":7,"timeout_ms":25000} → the same, once the state is no longer rev 7, or at the timeout
//	{"cmd":"start","task":"...","key":"...",...} → {"ok":true}  (standalone.StartRequest); leaves Pinky's app first
//	{"cmd":"decide","need":"...","decision":"allow_once"} → {"ok":true}; back to the task's app first
//	{"cmd":"preset","preset":1} / {"cmd":"stop"} / {"cmd":"dismiss"} → {"ok":true}
//	{"cmd":"ui","on":true}                  → {"ok":true}  (Pinky's app came to the front or left)
//	{"cmd":"history","search":"","before_ms":0,"limit":50} → {"ok":true,"history":[...]}
//	{"cmd":"info"}                          → {"ok":true,"info":{...}}
//
// reason, when the phone is not ready, says why in a word Pinky's screens
// put into the person's language: one of the Reason constants.
//
// A failed command replies {"ok":false,"error":"...","code":"..."}, where
// code, when set, is what Pinky's screens put into words: no_device,
// no_key, no_task, no_model, busy, computer (Pinky on a computer uses the
// phone: claims.go) or gone (the need was answered or expired).
//
// The key a task starts with stays in memory until the task ends. Nothing
// here logs a request.
package onphone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/ios"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/pilot"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/standalone"
)

// Why the phone is not ready.
const (
	ReasonConnecting = "connecting" // the core is connecting, or will try again
	ReasonVPNOff     = "vpn_off"    // the VPN that reflects the phone's own connections is off
	ReasonNotPaired  = "not_paired" // Pinky holds no pairing with this iPhone
	ReasonUnpaired   = "unpaired"   // the phone no longer knows Pinky's pairing
	ReasonFailed     = "failed"     // something else; the device's detail says what
)

// Options configure the core.
type Options struct {
	// ID is the phone's UDID, its id in history.
	ID string
	// Self is the bundle id of Pinky's app, whose screens the agent never
	// sees or touches.
	Self string
	// DataDir holds the history database and the rules; empty keeps
	// history in memory and uses the bundled rules.
	DataDir string
	Version string
	// Gateway and Pilot tune the gateway and the agent, for tests.
	Gateway gateway.Options
	Pilot   pilot.Options
	Log     *slog.Logger
}

// Core is Pinky on the iPhone.
type Core struct {
	opt    Options
	log    *slog.Logger
	sc     *standalone.Core
	prov   *Provider
	gate   *gate
	claims *computerClaims
	ctx    context.Context
	cancel context.CancelFunc
	kick   chan struct{}

	mu      sync.Mutex
	rev     uint64
	state   []byte        // the last standalone.State, as JSON
	reason  string        // the last reason the phone was not ready
	changed chan struct{} // closed and replaced when state or reason changes
	started bool          // a task started whose end is not logged yet
}

// New starts the core, with the phone offline until Connected.
func New(o Options) (*Core, error) {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Self == "" {
		o.Self = policy.IPhoneApp
	}
	g := newGate(o.Self)
	prov := newProvider(o.ID)
	claims := newComputerClaims()
	gopt := o.Gateway
	// The connection loop asks for a listing as the phone comes and goes,
	// so the gateway need not look often; every wakeup costs battery.
	if gopt.DiscoverEvery <= 0 {
		gopt.DiscoverEvery = 15 * time.Second
	}
	if gopt.RulesEvery <= 0 {
		gopt.RulesEvery = 15 * time.Second
	}
	// No pictures of each step: each costs a capture and memory the
	// extension does not have.
	gopt.NoScreenshots = true
	sc, err := standalone.New(standalone.Options{
		Providers: []driver.Provider{prov},
		Device:    o.ID,
		DataDir:   o.DataDir,
		// On the phone it drives, ask before commits, messages and
		// settings changes too, not only payments.
		Preset:  policy.PresetCommits,
		Version: o.Version,
		Gateway: gopt,
		Pilot:   o.Pilot,
		Claims:  claims,
		Log:     o.Log,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Core{opt: o, log: o.Log, sc: sc, prov: prov, gate: g, claims: claims, ctx: ctx, cancel: cancel,
		kick: make(chan struct{}, 1), changed: make(chan struct{})}
	claims.setNotify(c.poke)
	c.publish()
	go c.watch()
	return c, nil
}

// Close stops the task, the agent and the gateway.
func (c *Core) Close() {
	c.cancel()
	c.sc.Close()
}

// Gateway is the core's gateway.
func (c *Core) Gateway() *gateway.Gateway { return c.sc.G }

// Connected makes d the phone tasks run on, behind the guard, until
// Disconnected.
func (c *Core) Connected(d *ios.Device) { c.connect(&Guard{Device: d, g: c.gate, log: c.log}, d) }

func (c *Core) connect(dev driver.Device, raw phone) {
	c.prov.set(dev, raw, driver.StateOnline, "", "")
	c.refresh()
}

// Disconnected takes the phone offline, in state (offline, or needs_setup
// when the person must act), for reason (a Reason constant), with detail
// saying why.
func (c *Core) Disconnected(state driver.State, reason, detail string) {
	c.prov.set(nil, nil, state, reason, detail)
	c.refresh()
}

// SetUI records whether Pinky's app is in front.
func (c *Core) SetUI(on bool) { c.gate.setUI(on) }

// ComputerSeen records that Pinky's app hosts WebDriverAgent for Pinky on
// a computer (claims.go): the core starts no task for a while.
func (c *Core) ComputerSeen() { c.claims.heard() }

// Stop stops the task at once (the Live Activity's Stop).
func (c *Core) Stop() {
	c.sc.Stop()
	c.poke()
}

func (c *Core) refresh() {
	go c.sc.G.Refresh(c.ctx)
	c.poke()
}

func (c *Core) poke() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

type request struct {
	Cmd       string `json:"cmd"`
	Rev       uint64 `json:"rev"`
	TimeoutMS int    `json:"timeout_ms"`
	On        bool   `json:"on"`
	Need      string `json:"need"`
	Decision  string `json:"decision"`
	Note      string `json:"note"`
	Preset    int    `json:"preset"`
	Search    string `json:"search"`
	BeforeMS  int64  `json:"before_ms"`
	Limit     int    `json:"limit"`
	standalone.StartRequest
}

type reply struct {
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Code    string          `json:"code,omitempty"`
	Rev     uint64          `json:"rev,omitempty"`
	State   json.RawMessage `json:"state,omitempty"`
	Reason  string          `json:"reason,omitempty"`
	History []HistoryTask   `json:"history,omitempty"`
	Info    *Info           `json:"info,omitempty"`
}

// HistoryTask is a task in history.
type HistoryTask struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Agent     string            `json:"agent"`
	State     string            `json:"state"`
	StartedMS int64             `json:"started_ms"`
	EndedMS   int64             `json:"ended_ms,omitempty"`
	Steps     []standalone.Step `json:"steps"`
}

// Info is the connection as the core sees it, for diagnostics.
type Info struct {
	Connected bool   `json:"connected"`
	Detail    string `json:"detail,omitempty"`
	UI        bool   `json:"ui"`
	Services  int    `json:"services"`
}

// Call answers one request; see the package comment. It may block: wait
// until the state changes, start and decide while Pinky's app leaves the
// screen.
func (c *Core) Call(data []byte) []byte {
	var r request
	var rep reply
	if err := json.Unmarshal(data, &r); err != nil {
		rep = reply{Error: "bad request: " + err.Error()}
	} else {
		rep = c.handle(r)
	}
	out, _ := json.Marshal(rep)
	return out
}

func (c *Core) handle(r request) reply {
	ctx := c.ctx
	switch r.Cmd {
	case "state":
		return c.stateReply(c.current())
	case "wait":
		limit := time.Duration(r.TimeoutMS) * time.Millisecond
		if limit <= 0 || limit > time.Minute {
			limit = 25 * time.Second
		}
		return c.stateReply(c.wait(ctx, r.Rev, limit))
	case "start":
		return c.start(ctx, r.StartRequest)
	case "decide":
		return c.decide(ctx, r.Need, gateway.Decision(r.Decision), r.Note)
	case "preset":
		if err := c.sc.SetPreset(policy.Preset(r.Preset)); err != nil {
			return fail("", err)
		}
	case "stop":
		c.sc.Stop()
	case "dismiss":
		c.sc.Dismiss()
	case "ui":
		c.gate.setUI(r.On)
		return reply{OK: true}
	case "history":
		return c.history(ctx, r)
	case "info":
		return reply{OK: true, Info: c.info()}
	default:
		return fail("", fmt.Errorf("unknown command %q", r.Cmd))
	}
	c.poke()
	return reply{OK: true}
}

func fail(code string, err error) reply { return reply{Error: err.Error(), Code: code} }

func (c *Core) stateReply(v view) reply {
	return reply{OK: true, Rev: v.rev, State: v.state, Reason: v.reason}
}

// view is the state as Call returns it.
type view struct {
	rev    uint64
	state  []byte
	reason string
}

// Errors of start and decide, with their codes.
var (
	errNoDevice = errors.New("this iPhone is not connected yet")
	errBusy     = errors.New("a task is already running")
	errGone     = errors.New("this was already answered, or the time to answer ran out")
)

// start leaves Pinky's app, so that the agent finds the phone as the
// person left it before they opened Pinky, and starts the task. It checks
// what can fail first: a person whose task cannot start stays in Pinky.
func (c *Core) start(ctx context.Context, r standalone.StartRequest) reply {
	d := c.prov.phone()
	st := c.sc.State()
	cfg := pilot.Config{Provider: r.Provider, Model: r.Model}.Normalize()
	switch {
	case d == nil || !st.Device.Ready:
		return fail("no_device", errNoDevice)
	case strings.TrimSpace(r.Task) == "":
		return fail("no_task", pilot.ErrNoTask)
	case cfg.Model == "":
		return fail("no_model", pilot.ErrNoModel)
	case strings.TrimSpace(r.Key) == "":
		return fail("no_key", standalone.ErrNoKey)
	case st.Agent != nil && st.Agent.Running:
		return fail("busy", errBusy)
	case st.Computer != nil:
		return fail(standalone.CodeComputer, standalone.ErrComputer)
	}
	if err := c.leave(ctx, d); err != nil {
		return fail("", fmt.Errorf("leave Pinky: %w", err))
	}
	if err := c.sc.Start(r); err != nil {
		code := ""
		switch {
		case errors.Is(err, standalone.ErrNoDevice):
			code, err = "no_device", errNoDevice
		case errors.Is(err, pilot.ErrBusy):
			code = "busy"
		case errors.Is(err, standalone.ErrComputer):
			code = standalone.CodeComputer
		}
		return fail(code, err)
	}
	c.log.Info("task started", "provider", cfg.Provider, "model", cfg.Model)
	// The next publish that finds the task ended logs how, also when it
	// ended before any publish saw it run.
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()
	c.poke()
	return reply{OK: true}
}

// decide answers a need. Pinky's app, where the person answered it, first
// leaves for the app the task works in, so that what the agent does next
// lands there.
func (c *Core) decide(ctx context.Context, need string, d gateway.Decision, note string) reply {
	found := false
	for _, n := range c.sc.G.Needs() {
		found = found || n.ID == need
	}
	if !found {
		return fail("gone", errGone)
	}
	c.back(ctx)
	if err := c.sc.Decide(need, d, note); err != nil {
		return fail("", err)
	}
	c.poke()
	return reply{OK: true}
}

// leaveWait is how long Pinky's app has to report it left the screen.
const leaveWait = 3 * time.Second

// leave takes Pinky's app off the screen by pressing home, past the
// guard.
func (c *Core) leave(ctx context.Context, d phone) error {
	if !c.gate.uiActive() {
		return nil
	}
	if err := d.Key(ctx, driver.KeyHome); err != nil {
		return err
	}
	c.leftAfter(ctx)
	return nil
}

// back takes Pinky's app off the screen for the app the task last worked
// in, or the home screen.
func (c *Core) back(ctx context.Context) {
	d := c.prov.phone()
	if d == nil || !c.gate.uiActive() {
		return
	}
	var err error
	if app := c.gate.lastApp(); app != "" {
		err = d.LaunchID(ctx, app)
	} else {
		err = d.Key(ctx, driver.KeyHome)
	}
	if err != nil {
		c.log.Warn("back to the task's app", "err", err)
		return
	}
	c.leftAfter(ctx)
}

// leftAfter waits for Pinky's app to report it left. One that never does
// (its report was lost) counts as gone: the policy still hides its screen
// when a read names it.
func (c *Core) leftAfter(ctx context.Context) {
	if !c.gate.waitUI(ctx, false, leaveWait) && ctx.Err() == nil {
		c.log.Warn("Pinky's app did not report leaving the screen")
		c.gate.setUI(false)
	}
}

func (c *Core) history(ctx context.Context, r request) reply {
	q := gateway.HistoryQuery{Search: r.Search, Limit: r.Limit}
	if r.BeforeMS > 0 {
		q.Before = time.UnixMilli(r.BeforeMS)
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	tasks, err := c.sc.G.History(ctx, q)
	if err != nil {
		return fail("", err)
	}
	out := make([]HistoryTask, 0, len(tasks))
	for _, t := range tasks {
		h := HistoryTask{ID: t.ID, Title: t.Title, Agent: t.Agent, State: string(t.State),
			StartedMS: t.Started.UnixMilli(), Steps: []standalone.Step{}}
		if !t.Ended.IsZero() {
			h.EndedMS = t.Ended.UnixMilli()
		}
		for _, s := range t.Steps {
			h.Steps = append(h.Steps, standalone.Step{Text: s.Text, Status: s.Status, MS: s.Elapsed.Milliseconds()})
		}
		out = append(out, h)
	}
	return reply{OK: true, History: out}
}

func (c *Core) info() *Info {
	in := &Info{UI: c.gate.uiActive()}
	c.prov.mu.Lock()
	in.Connected, in.Detail = c.prov.dev != nil, c.prov.detail
	dev := c.prov.dev
	c.prov.mu.Unlock()
	if s, ok := dev.(interface{ Services() []string }); ok {
		in.Services = len(s.Services())
	}
	return in
}

// watch keeps the state Call returns current: after the gateway's events
// and the core's own commands, and each second while a task runs or waits
// for the person, for its elapsed time and the agent's progress. An idle
// core does not wake.
func (c *Core) watch() {
	ev := c.sc.G.Subscribe(c.ctx)
	tick := time.NewTimer(time.Hour)
	defer tick.Stop()
	for {
		active := c.publish()
		if active {
			tick.Reset(time.Second)
		} else {
			tick.Stop()
		}
		select {
		case _, ok := <-ev:
			if !ok {
				return
			}
		case <-c.kick:
		case <-tick.C:
		case <-c.ctx.Done():
			return
		}
		// A burst of events publishes once.
		select {
		case <-time.After(100 * time.Millisecond):
		case <-c.ctx.Done():
			return
		}
		for drained := false; !drained; {
			select {
			case <-ev:
			default:
				drained = true
			}
		}
	}
}

// publish reads the state and reports whether a task runs or waits.
func (c *Core) publish() bool {
	st := c.sc.State()
	js, err := json.Marshal(st)
	if err != nil {
		c.log.Error("state", "err", err)
		return false
	}
	why := ""
	if !st.Device.Ready {
		why = c.prov.why()
	}
	running := st.Agent != nil && st.Agent.Running
	c.mu.Lock()
	if !bytes.Equal(js, c.state) || why != c.reason {
		c.rev++
		c.state, c.reason = js, why
		close(c.changed)
		c.changed = make(chan struct{})
	}
	ended := c.started && !running && st.Agent != nil
	if ended {
		c.started = false
	}
	c.mu.Unlock()
	if ended {
		c.logEnd(*st.Agent)
	}
	return running || len(st.Needs) > 0
}

// logEnd logs how a task ended. Its summary goes to the log only when Pinky
// ended the task over an error: otherwise it is the model's words, which
// may tell what was on the screen.
func (c *Core) logEnd(a standalone.AgentState) {
	args := []any{"end", a.End, "steps", a.Steps, "seconds", a.Seconds}
	if a.End == string(pilot.Error) {
		args = append(args, "error", a.Summary)
	}
	c.log.Info("task ended", args...)
}

func (c *Core) current() view {
	c.mu.Lock()
	defer c.mu.Unlock()
	return view{c.rev, c.state, c.reason}
}

// wait returns the state once it is no longer rev, or after limit.
func (c *Core) wait(ctx context.Context, rev uint64, limit time.Duration) view {
	t := time.NewTimer(limit)
	defer t.Stop()
	for {
		c.mu.Lock()
		v, ch := view{c.rev, c.state, c.reason}, c.changed
		c.mu.Unlock()
		if v.rev != rev {
			return v
		}
		select {
		case <-ch:
		case <-t.C:
			return v
		case <-ctx.Done():
			return v
		}
	}
}
