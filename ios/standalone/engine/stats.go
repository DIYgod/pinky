package engine

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Stats are the numbers the prototype measures: how long the engine has
// lived, how often it reconnected and how fast and how reliably each kind
// of call went.
type Stats struct {
	Started     time.Time          `json:"started"`
	Connects    int                `json:"connects"`
	ConnectedAt time.Time          `json:"connected_at,omitzero"`
	Ops         map[string]*OpStat `json:"ops,omitempty"`
}

// OpStat is one kind of call.
type OpStat struct {
	Count  int       `json:"count"`
	Errors int       `json:"errors"`
	LastOK time.Time `json:"last_ok,omitzero"`
	LastMS int64     `json:"last_ms"`
	// MS are the latencies of the last calls, newest last.
	MS      []int64 `json:"-"`
	LastErr string  `json:"last_err,omitempty"`
}

func (e *Engine) record(name string, start time.Time, err error) {
	ms := time.Since(start).Milliseconds()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stats.Ops == nil {
		e.stats.Ops = map[string]*OpStat{}
	}
	o := e.stats.Ops[name]
	if o == nil {
		o = &OpStat{}
		e.stats.Ops[name] = o
	}
	o.Count++
	o.LastMS = ms
	if err != nil {
		o.Errors++
		o.LastErr = err.Error()
		return
	}
	o.LastOK = time.Now()
	o.MS = append(o.MS, ms)
	if len(o.MS) > 200 {
		o.MS = o.MS[len(o.MS)-200:]
	}
}

// percentile is the p-th percentile of ms (0..100).
func percentile(ms []int64, p int) int64 {
	if len(ms) == 0 {
		return 0
	}
	s := slices.Clone(ms)
	slices.Sort(s)
	return s[min(len(s)-1, len(s)*p/100)]
}

// Heartbeat is one log line with the engine's state and numbers, for the
// survival measurements: grep the phone's log for "pinky hb".
func (e *Engine) Heartbeat() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "pinky hb up=%s state=%q connects=%d ui=%t",
		time.Since(e.stats.Started).Round(time.Second), e.state, e.stats.Connects, e.uiActive)
	if e.last != nil {
		fmt.Fprintf(&b, " app=%s", e.last.App.ID)
	}
	if e.run != nil {
		st := e.run.Status()
		fmt.Fprintf(&b, " task=%s steps=%d cost=%.3f", map[bool]string{true: "running", false: string(st.End)}[st.Running()], st.Steps, st.Cost)
	}
	if e.soak != nil {
		fmt.Fprintf(&b, " soak=%d/%d,pixels=%d", e.soak.ok, e.soak.rounds, e.soak.pixels)
	}
	names := make([]string, 0, len(e.stats.Ops))
	for n := range e.stats.Ops {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		o := e.stats.Ops[n]
		fmt.Fprintf(&b, " %s=%d/%d,p50=%d,p90=%d", n, o.Count-o.Errors, o.Count, percentile(o.MS, 50), percentile(o.MS, 90))
		if !o.LastOK.IsZero() {
			fmt.Fprintf(&b, ",ok%s", time.Since(o.LastOK).Round(time.Second))
		}
	}
	return b.String()
}
