package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/recording"
)

// streamer sends packets of the test stream with PTS on the stream's own
// clock and a little time between them, as a phone does.
type streamer struct {
	t           *testing.T
	src         *feedStream
	cfg         driver.Packet
	keys, plain []driver.Packet
	k, p        int
	start       time.Time
}

func newStreamer(t *testing.T, src *feedStream) *streamer {
	st := &streamer{t: t, src: src, start: time.Now()}
	for _, p := range testPackets(t) {
		switch {
		case p.Config:
			st.cfg = p
		case p.KeyFrame:
			st.keys = append(st.keys, p)
		default:
			st.plain = append(st.plain, p)
		}
	}
	return st
}

// config sends the configuration packet.
func (s *streamer) config() { s.src.send(s.cfg) }

// pictures sends n pictures, the first a key frame when key is set.
func (s *streamer) pictures(n int, key bool) {
	for i := 0; i < n; i++ {
		var p driver.Packet
		if i == 0 && key {
			p = s.keys[s.k%len(s.keys)]
			s.k++
		} else {
			p = s.plain[s.p%len(s.plain)]
			s.p++
		}
		p.PTS = time.Since(s.start)
		s.src.send(p)
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(5 * time.Millisecond)
}

func taskID(t *testing.T, g *Gateway, s *Session) string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, l := range s.leases {
		return l.task.id
	}
	t.Fatal("the session has no task")
	return ""
}

func TestRecordingKeepsOnlyWhatReadsVouchFor(t *testing.T) {
	ctx := context.Background()
	v := newVideoDev("pixel")
	g := newVideoGateway(t, v)
	g.SetRecording(true)
	s, _ := connect(t, g, "Lunch")
	id := taskID(t, g, s)
	st := newStreamer(t, v.open(t))

	// A second of video, then a read of a plain screen: kept.
	st.config()
	st.pictures(10, true)
	mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusSettled)

	// A payment hands over to a blocked app: what came since the last
	// read is dropped, and so is what comes while it is in front.
	st.pictures(5, false)
	v.Show("wallet")
	s.Observe(ctx, ObserveRequest{})
	st.pictures(4, false)

	// Home again: writing resumes at a key frame, which the recorder asks
	// for (once at the start, once now).
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "key", Key: "home"}), StatusSettled)
	time.Sleep(10 * time.Millisecond)
	if n := st.src.keyRequests(); n < 2 {
		t.Fatalf("%d key frame requests", n)
	}
	st.pictures(3, false)
	st.pictures(5, true)

	// The task ends: one more read keeps the last part.
	mustStatus(t, s.Release(ctx, ReleaseRequest{}), StatusSettled)
	g.Flush()
	rec, err := g.Recording(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	info, err := recording.Probe(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Frames != 15 || info.Width != 96 || info.Codec != driver.H264 {
		t.Fatalf("recorded %+v, want the 10 frames before the wallet and the 5 after the key frame", info)
	}
	if rec.Length <= 0 || rec.Start.IsZero() {
		t.Fatalf("recording %+v", rec)
	}
	var home bool
	for _, m := range rec.Marks {
		home = home || m.Text == "Pressed home"
		if m.At < 0 || m.At > rec.Length+time.Second {
			t.Fatalf("mark %+v outside the recording of %v", m, rec.Length)
		}
	}
	if !home || len(rec.Marks) < 3 {
		t.Fatalf("marks %+v", rec.Marks)
	}
}

func TestRecordingDropsTheTakeover(t *testing.T) {
	ctx := context.Background()
	v := newVideoDev("pixel")
	g := newVideoGateway(t, v)
	g.SetRecording(true)
	s, _ := connect(t, g, "Lunch")
	id := taskID(t, g, s)
	st := newStreamer(t, v.open(t))
	st.config()
	st.pictures(10, true)
	mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusSettled)
	// The person takes over (to type a password, say): nothing of it is
	// kept, even with a plain screen afterwards.
	st.pictures(4, false)
	if err := g.TakeOver("pixel"); err != nil {
		t.Fatal(err)
	}
	st.pictures(6, false)
	s.Observe(ctx, ObserveRequest{})
	if err := g.HandBack("pixel", ""); err != nil {
		t.Fatal(err)
	}
	// Stopped: no read afterwards, so what is held is not kept.
	st.pictures(5, true)
	if err := g.Stop("pixel"); err != nil {
		t.Fatal(err)
	}
	g.Flush()
	rec, err := g.Recording(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	info, err := recording.Probe(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Frames != 10 {
		t.Fatalf("recorded %d frames, want only the 10 before the takeover", info.Frames)
	}
}

func TestNoRecordingWhenOff(t *testing.T) {
	ctx := context.Background()
	v := newVideoDev("pixel")
	g := newVideoGateway(t, v)
	s, _ := connect(t, g, "Lunch")
	id := taskID(t, g, s)
	select {
	case <-v.opened:
		t.Fatal("recording off, yet the stream opened")
	case <-time.After(50 * time.Millisecond):
	}
	mustStatus(t, s.Release(ctx, ReleaseRequest{}), StatusSettled)
	if _, err := g.Recording(ctx, id); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("recording of an unrecorded task: %v", err)
	}
}

// Packets from before the task, which a late member of the hub gets to
// start from, are never written: the person may have used the phone.
func TestRecordingStartsWithTheTask(t *testing.T) {
	ctx := context.Background()
	v := newVideoDev("pixel")
	g := newVideoGateway(t, v)
	mirror, err := g.Stream(ctx, "pixel", driver.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer mirror.Close()
	st := newStreamer(t, v.open(t))
	st.config()
	st.pictures(8, true) // before the task; the hub keeps them for late viewers
	time.Sleep(10 * time.Millisecond)
	g.SetRecording(true)
	s, _ := connect(t, g, "Lunch")
	id := taskID(t, g, s)
	st.pictures(3, false) // still the old key frame's pictures: not decodable on their own
	st.pictures(4, true)
	mustStatus(t, s.Release(ctx, ReleaseRequest{}), StatusSettled)
	g.Flush()
	rec, err := g.Recording(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	info, err := recording.Probe(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Frames != 4 {
		t.Fatalf("recorded %d frames, want the 4 from the task's first key frame", info.Frames)
	}
}
