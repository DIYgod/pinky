package gateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/history"
	"github.com/DIYgod/pinky/internal/recording"
	"github.com/DIYgod/pinky/internal/screen"
)

// Screen recordings: one MP4 per task, remuxed without re-encoding from
// the stream of the screen the agent uses (the phone's, or its background
// display), shared with the mirror through the device's hub.
//
// Nothing of a blocked app, a locked screen or the person's own use of the
// phone may be stored, and the gateway knows what is on the screen only
// from reads. So the recorder holds packets back until a read vouches for
// them: the packets that arrived before a read started are written when
// that read shows a recordable screen (unlocked, an app that is not
// blocked, the person not in control), and dropped, with everything held
// since the last read that did, when it does not. Until a read shows a
// recordable screen again, packets are dropped, and writing resumes at a
// key frame. Taking over, a lock reported by the driver, an unlock from
// the computer and the phone going offline drop what is held too, and so
// does a new stream. Packets that arrived before the task started are
// never written. When packets have waited for videoConfirm and no action
// is running, the recorder reads the screen itself, so it never holds
// much; when the task ends, it reads once more, except after Stop, which
// leaves the phone alone.
//
// A blocked app that comes and goes between two reads, neither of which
// sees it, is not caught. Payment hand-offs stay in the bank's app until
// the person is done, so the next read sees those.

// Recorder limits.
const (
	videoConfirm  = 10 * time.Second // held this long, the recorder reads the screen
	maxHeldBytes  = 48 << 20         // held beyond this, packets are dropped
	videoRetry    = 2 * time.Second  // between attempts to open the stream
	videoStopRead = 15 * time.Second // the last read, when a task ends
)

// vpacket is a packet on its way to the file. A key frame carries the
// configuration it was encoded with, written before it when it starts the
// file again after a gap.
type vpacket struct {
	p       driver.Packet
	arrived time.Time // when the hub got it, which reads are compared with
	at      time.Time // where it goes, on the wall clock
	cfg     *driver.Packet
}

// taskVideo records one task.
type taskVideo struct {
	g     *Gateway
	d     *device
	t     *task
	since time.Time // packets that arrived before are never written

	ctx    context.Context
	cancel context.CancelFunc
	ran    chan struct{} // closed when run returns
	wrote  chan struct{} // closed when writeLoop returns

	mu        sync.Mutex
	cond      *sync.Cond    // ready or stopping changed
	key       string        // the hub the recording taps
	dev       driver.Device // its device
	moved     chan struct{} // closed when key changes
	held      []vpacket     // waiting for a read
	heldSize  int
	paused    bool // dropping until a recordable read
	needKey   bool // the next picture moved to ready must be a key frame
	cfg       *driver.Packet
	ready     []vpacket
	reading   bool
	stopping  bool
	codec     driver.Codec
	anchorAt  time.Time
	anchorPTS time.Duration
	lastPTS   time.Duration

	// What the write loop has written so far, for Recording.
	file    string
	origin  time.Time // wall time of the file's start
	length  time.Duration
	started bool

	// Owned by writeLoop, then by stop.
	w      *recording.Writer
	saved  bool
	failed bool
}

// startVideoLocked starts recording a lease's task. Callers hold g.mu.
func (g *Gateway) startVideoLocked(l *lease) {
	d := l.dev
	if !g.recordOn || g.store == nil || d.video != nil || l.ended || g.closed {
		return
	}
	key, dev := hubKey(d.id, false), d.dev
	if l.bg != nil {
		key, dev = hubKey(d.id, true), l.bg
	}
	if dev == nil {
		return
	}
	v := &taskVideo{g: g, d: d, t: l.task, since: time.Now(), key: key, dev: dev, moved: make(chan struct{}),
		needKey: true, ran: make(chan struct{}), wrote: make(chan struct{})}
	v.cond = sync.NewCond(&v.mu)
	v.ctx, v.cancel = context.WithCancel(g.ctx)
	d.video = v
	g.videos.add()
	go v.run()
	go v.writeLoop()
}

// stopVideoLocked ends the recording of a device's task; with read, the
// recorder reads the screen once more to keep what it holds. Callers hold
// g.mu; the rest happens in the background.
func (g *Gateway) stopVideoLocked(d *device, read bool) {
	v := d.video
	if v == nil {
		return
	}
	d.video = nil
	g.videoStops.add()
	go v.stop(read)
}

// moveVideoLocked moves the recording to another screen of the device, as
// when a task leaves its background display. Callers hold g.mu.
func (g *Gateway) moveVideoLocked(d *device, key string, dev driver.Device) {
	v := d.video
	if v == nil || dev == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == key {
		return
	}
	v.key, v.dev = key, dev
	close(v.moved)
	v.moved = make(chan struct{})
	v.dropLocked()
}

// run taps the stream until the recording stops, tapping it again when it
// ends (the phone dropped) or the recording moves to another screen.
func (v *taskVideo) run() {
	defer close(v.ran)
	g := v.g
	for v.ctx.Err() == nil {
		v.mu.Lock()
		key, dev, moved := v.key, v.dev, v.moved
		v.mu.Unlock()
		t, err := g.tapStream(v.ctx, key, dev, v.packet)
		if err != nil {
			if errors.Is(err, driver.ErrUnsupported) {
				g.log.Info("no screen recording: the phone sends no video", "device", v.d.id, "err", err)
				return
			}
			if !sleepCtx(v.ctx, videoRetry, nil) {
				return
			}
			continue
		}
		select {
		case <-t.Done():
		case <-moved:
		case <-v.ctx.Done():
		}
		t.Close()
		v.mu.Lock()
		// A new stream starts over: nothing held belongs to it.
		v.dropLocked()
		v.anchorAt, v.lastPTS = time.Time{}, 0
		v.mu.Unlock()
		if !sleepCtx(v.ctx, time.Second/4, nil) {
			return
		}
	}
}

// packet holds a packet from the hub, on the hub's goroutine.
func (v *taskVideo) packet(tp timedPacket, codec driver.Codec) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stopping {
		return
	}
	if v.codec == "" {
		v.codec = codec
	}
	if codec != v.codec {
		return // another codec on a new stream: the file cannot take it
	}
	p := tp.p
	if p.Config {
		cp := p
		v.cfg = &cp
		if !v.paused && !tp.at.Before(v.since) {
			v.held = append(v.held, vpacket{p: p, arrived: tp.at, at: tp.at})
		}
		return
	}
	at := v.timeOf(tp)
	if v.paused || tp.at.Before(v.since) {
		v.needKey = true
		return
	}
	vp := vpacket{p: p, arrived: tp.at, at: at}
	if p.KeyFrame {
		vp.cfg = v.cfg
	}
	v.held = append(v.held, vp)
	v.heldSize += len(p.Data)
	if v.heldSize > maxHeldBytes {
		v.dropLocked() // no read came in time: this part is lost
		return
	}
	if !v.reading && tp.at.Sub(v.held[0].arrived) >= videoConfirm {
		v.reading = true
		go v.confirm()
	}
}

// timeOf places a picture on the wall clock by its PTS, counted from the
// first picture of the stream, which is smoother than arrival times; by
// its arrival when the PTS does not move forward or strays.
func (v *taskVideo) timeOf(tp timedPacket) time.Time {
	p := tp.p
	if v.anchorAt.IsZero() || p.PTS <= v.lastPTS {
		v.anchorAt, v.anchorPTS = tp.at, p.PTS
	}
	v.lastPTS = p.PTS
	at := v.anchorAt.Add(p.PTS - v.anchorPTS)
	if d := at.Sub(tp.at); d > time.Second || d < -time.Second {
		v.anchorAt, v.anchorPTS = tp.at, p.PTS
		at = tp.at
	}
	return at
}

// dropLocked drops what is held and waits for a recordable read.
func (v *taskVideo) dropLocked() {
	v.held, v.heldSize = nil, 0
	v.paused, v.needKey = true, true
}

// read takes a read of the screen the agent uses, which started at start:
// what arrived before it goes to the file when the screen is recordable,
// and is dropped otherwise.
func (v *taskVideo) read(start time.Time, recordable bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !recordable {
		v.dropLocked()
		return
	}
	n := 0
	for n < len(v.held) && v.held[n].arrived.Before(start) {
		n++
	}
	for _, vp := range v.held[:n] {
		switch {
		case vp.p.Config:
			v.ready = append(v.ready, vp)
		case v.needKey && !vp.p.KeyFrame:
			// Cannot be decoded without the frames dropped before it.
		default:
			if vp.p.KeyFrame && v.needKey && vp.cfg != nil {
				v.ready = append(v.ready, vpacket{p: *vp.cfg, arrived: vp.arrived, at: vp.at})
			}
			v.needKey = false
			v.ready = append(v.ready, vp)
		}
		v.heldSize -= len(vp.p.Data)
	}
	v.held = append([]vpacket(nil), v.held[n:]...)
	v.paused = false
	v.cond.Broadcast()
}

// pause drops what is held: the screen shows, or may show, what must not
// be recorded.
func (v *taskVideo) pause() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.dropLocked()
}

// confirm reads the screen for the packets held, unless an action runs,
// which reads it soon anyway.
func (v *taskVideo) confirm() {
	defer func() {
		v.mu.Lock()
		v.reading = false
		v.mu.Unlock()
	}()
	d := v.d
	if !d.act.TryLock() {
		return
	}
	defer d.act.Unlock()
	ctx, cancel := context.WithTimeout(v.ctx, observeTimeout)
	defer cancel()
	v.g.observe(ctx, d, false)
}

// writeLoop writes what reads confirmed.
func (v *taskVideo) writeLoop() {
	defer close(v.wrote)
	for {
		v.mu.Lock()
		for len(v.ready) == 0 && !v.stopping {
			v.cond.Wait()
		}
		batch := v.ready
		v.ready = nil
		stopping := v.stopping
		codec := v.codec
		v.mu.Unlock()
		for _, vp := range batch {
			v.write(vp, codec)
		}
		if stopping && len(batch) == 0 {
			return
		}
	}
}

// write adds one confirmed packet to the file; the file starts with the
// first key frame.
func (v *taskVideo) write(vp vpacket, codec driver.Codec) {
	if v.failed {
		return
	}
	g := v.g
	if v.w == nil {
		path, err := g.store.RecordingPath(v.t.id, v.t.started)
		if err == nil {
			v.w, err = recording.Create(path, codec)
		}
		if err != nil {
			v.failed = true
			g.log.Warn("screen recording", "task", v.t.id, "err", err)
			return
		}
	}
	var at time.Duration
	if !vp.p.Config {
		if !v.w.Started() {
			v.mu.Lock()
			v.origin = vp.at
			v.mu.Unlock()
		}
		at = vp.at.Sub(v.origin)
	}
	if err := v.w.Write(vp.p, at); err != nil {
		v.failed = true
		g.log.Warn("screen recording", "task", v.t.id, "err", err)
		return
	}
	if v.w.Started() {
		v.mu.Lock()
		v.file, v.length, v.started = v.w.Path(), v.w.Length(), true
		v.mu.Unlock()
		if !v.saved {
			v.saved = true
			v.save()
		}
	}
}

// save records the recording in history.
func (v *taskVideo) save() {
	g := v.g
	w := v.w
	width, height := w.Size()
	v.mu.Lock()
	origin := v.origin
	v.mu.Unlock()
	rec := history.Recording{TaskID: v.t.id, Path: w.Path(), Started: origin, Length: w.Length(),
		Codec: string(v.codec), Width: width, Height: height}
	if fi, err := os.Stat(w.Path()); err == nil {
		rec.Size = fi.Size()
	}
	g.rec.do(func(ctx context.Context) error { return g.store.SaveRecording(ctx, rec) })
}

// stop ends the recording: with read, one more read keeps what it holds
// when the screen is still recordable; then the file is closed.
func (v *taskVideo) stop(read bool) {
	g := v.g
	defer g.videos.done()
	defer g.videoStops.done()
	if read {
		v.mu.Lock()
		holds := len(v.held) > 0
		v.mu.Unlock()
		if holds && v.ctx.Err() == nil {
			d := v.d
			d.act.Lock()
			ctx, cancel := context.WithTimeout(v.ctx, videoStopRead)
			start := time.Now()
			if s, err := g.observe(ctx, d, false); err == nil {
				v.read(start, g.recordable(d, s))
			}
			cancel()
			d.act.Unlock()
		}
	}
	v.mu.Lock()
	v.stopping = true
	v.held, v.heldSize = nil, 0
	v.cond.Broadcast()
	v.mu.Unlock()
	v.cancel()
	<-v.ran
	<-v.wrote
	if v.w == nil {
		return
	}
	v.mu.Lock()
	origin := v.origin
	v.mu.Unlock()
	if err := v.w.Close(time.Since(origin)); err != nil {
		g.log.Warn("screen recording", "task", v.t.id, "err", err)
	}
	if v.w.Started() {
		v.mu.Lock()
		v.length = v.w.Length()
		v.mu.Unlock()
		v.save()
	}
}

// view returns what the recording holds so far.
func (v *taskVideo) view() (RecordingView, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.started {
		return RecordingView{}, false
	}
	return RecordingView{TaskID: v.t.id, Path: v.file, Start: v.origin, Length: v.length}, true
}

// recordable reports whether read s of the screen the agent uses may be
// recorded.
func (g *Gateway) recordable(d *device, s *screen.Screen) bool {
	if s == nil || s.Locked {
		return false
	}
	if _, blocked := g.policy.Blocked(s.App); blocked {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return !d.person
}

// pauseVideoLocked drops what a device's recording holds. Callers hold
// g.mu.
func (g *Gateway) pauseVideoLocked(d *device) {
	if v := d.video; v != nil {
		v.pause()
	}
}

// SetRecording turns screen recording of agent tasks on or off. Turning it
// on starts recording running tasks; turning it off finishes their files.
// It needs a history folder (Options.DataDir). Recording pauses while a
// blocked app is in front.
func (g *Gateway) SetRecording(on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.recordOn == on {
		return
	}
	g.recordOn = on
	for _, d := range g.devices {
		switch {
		case on && d.lease != nil:
			g.startVideoLocked(d.lease)
		case !on:
			g.stopVideoLocked(d, true)
		}
	}
}

// Recording returns a task's screen recording, or driver.ErrNotFound when
// it has none (recording was off, the phone never showed a recordable
// screen, or the retention limit removed it). A running task's recording
// grows; Length is what is written so far.
func (g *Gateway) Recording(ctx context.Context, taskID string) (RecordingView, error) {
	notFound := fmt.Errorf("task %s has no recording: %w", taskID, driver.ErrNotFound)
	g.mu.Lock()
	var running *taskVideo
	for _, d := range g.devices {
		if v := d.video; v != nil && v.t.id == taskID {
			running = v
		}
	}
	var steps []StepView
	if running != nil {
		steps = append(steps, running.t.steps...)
	}
	g.mu.Unlock()
	var view RecordingView
	if running != nil {
		v, ok := running.view()
		if !ok {
			return RecordingView{}, notFound
		}
		view = v
	} else {
		if g.store == nil {
			return RecordingView{}, notFound
		}
		g.Flush()
		r, err := g.store.Recording(ctx, taskID)
		if errors.Is(err, history.ErrNoRecording) {
			return RecordingView{}, notFound
		}
		if err != nil {
			return RecordingView{}, err
		}
		view = RecordingView{TaskID: taskID, Path: r.Path, Start: r.Started, Length: r.Length}
		// The file is the truth: a crash leaves the row's length behind.
		if info, err := recording.Probe(r.Path); err == nil {
			view.Length = info.Length
		}
		recs, err := g.store.Steps(ctx, taskID)
		if err != nil {
			return RecordingView{}, err
		}
		for _, st := range recs {
			steps = append(steps, StepView{Index: st.Index, Text: st.Text, At: st.Started})
		}
	}
	view.Marks = marksFor(view, steps)
	return view, nil
}

// marksFor places steps on a recording: where each started, from the
// recording's start. Steps from before it start at 0; steps after its end
// are left out.
func marksFor(view RecordingView, steps []StepView) []RecordingMark {
	var out []RecordingMark
	for _, st := range steps {
		if st.At.IsZero() {
			continue
		}
		at := st.At.Sub(view.Start)
		if at < 0 {
			at = 0
		}
		if at > view.Length+time.Second {
			continue
		}
		out = append(out, RecordingMark{At: at, Step: st.Index, Text: st.Text})
	}
	return out
}
