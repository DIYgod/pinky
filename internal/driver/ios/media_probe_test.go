package ios

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	goios "github.com/danielpaulus/go-ios/ios"
)

// probeStream is a fake stream that showed a picture, with a viewer when
// watched, and a log in buf.
func probeStream(t *testing.T, watched bool) (*fakeStream, *bytes.Buffer) {
	t.Helper()
	f := newFakeStream(t)
	var buf bytes.Buffer
	f.m.setLog(slog.New(slog.NewTextHandler(&buf, nil)))
	if watched {
		f.m.subscribe()
	}
	f.rtp(100, false, 0, hevcParams(testHEVCSPS(1136, 2464, 0)))
	f.keyFrame()
	select {
	case <-f.m.ready:
	default:
		t.Fatal("no picture")
	}
	return f, &buf
}

// keyFrame feeds a key frame, as the phone's answer to a request.
func (f *fakeStream) keyFrame() {
	f.ts += 400
	f.rtp(100, true, f.ts, hevcNAL(19, 0xAA))
}

// fragments is a frame that is no key frame: one slice in n RTP payloads.
func fragments(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		h := byte(1) // TRAIL_R
		if i == 0 {
			h |= 0x80
		}
		if i == n-1 {
			h |= 0x40
		}
		out[i] = []byte{hevcFU << 1, 1, h, byte(i)}
	}
	return out
}

// srCount feeds a sender report that says the phone sent n packets.
func (f *fakeStream) srCount(n uint32) {
	b := make([]byte, 28)
	b[0], b[1] = 0x80, 200
	binary.BigEndian.PutUint16(b[2:], 6)
	binary.BigEndian.PutUint32(b[20:], n)
	f.m.onRTCP(b)
}

// ticks runs the probe from from to to, as the feedback loop does every
// 200 ms, and returns the requests it sent and the error it failed with.
func ticks(m *media, from, to time.Time) (asks int, err error) {
	for now := from; !now.After(to); now = now.Add(200 * time.Millisecond) {
		before := m.probe.requests
		err = m.probeTick(now)
		asks += m.probe.requests - before
		if err != nil {
			return asks, err
		}
	}
	return asks, nil
}

// failAfterInput gives the stream input that no frame follows and runs its
// probe, which must find the stream broken after probeAsks requests.
func failAfterInput(t *testing.T, m *media) *probeError {
	t.Helper()
	var in atomic.Int64
	m.setProbe(&in, 0, nil)
	at := time.Now().Add(time.Millisecond)
	in.Store(at.UnixNano())
	asks, err := ticks(m, at, at.Add(probeInput+probeWait+time.Second))
	var pe *probeError
	if !errors.As(err, &pe) || asks != probeAsks {
		t.Fatalf("after %d requests: %v; want the stream broken after %d", asks, err, probeAsks)
	}
	return pe
}

// A still screen a mirror watches is never failed, nor asked for key
// frames, however long it stays still, also after input it answered: the
// phone may ignore key frame requests then, and probes of still screens
// restarted working streams of the owner's iPhone 13 mini and iPad and of
// #235's iPhone 17 (docs/notes/ios.md, "Probes").
func TestProbeStillScreenNotFailed(t *testing.T) {
	f, buf := probeStream(t, true)
	m, t0 := f.m, time.Now()
	var in atomic.Int64
	m.setProbe(&in, 0, func() bool { return false })
	if asks, err := ticks(m, t0, t0.Add(10*time.Minute)); asks != 0 || err != nil {
		t.Fatalf("a still screen: %d requests, %v", asks, err)
	}
	// Input the screen answers, then a still screen again.
	in.Store(time.Now().UnixNano())
	f.ts += 400
	f.rtp(100, true, f.ts, hevcNAL(1, 0xBB))
	now := time.Now()
	if asks, err := ticks(m, now, now.Add(10*time.Minute)); asks != 0 || err != nil {
		t.Fatalf("a still screen after input it answered: %d requests, %v", asks, err)
	}
	if m.Err() != nil || m.stopped.Load() || strings.Contains(buf.String(), "level=WARN") {
		t.Fatalf("a still screen was failed: %v\n%s", m.Err(), buf)
	}
}

// A phone that stops sending video while its sender reports go on (the
// alert of #204) gives no frame after input, even when asked: the stream
// fails, and the log says no RTP came while the phone counted packets it
// sent.
func TestProbeNoRTPFails(t *testing.T) {
	f, buf := probeStream(t, true)
	m := f.m
	f.srCount(40)
	f.srCount(90) // 88 sent that never came
	pe := failAfterInput(t, m)
	m.fail(pe)
	if !m.stopped.Load() || !errors.As(m.Err(), &pe) {
		t.Fatalf("the stream did not fail: %v", m.Err())
	}
	got := buf.String()
	t.Logf("the log:\n%s", got)
	for _, want := range []string{
		"level=WARN", "the video stream stopped showing pictures", " since_input=", " requests=3 ", " rtp_since_frame=0 ",
		" sender_reports=2 ", " sr_packets=90 ", " sr_missing=88 ", " frames=1 ", " payload_types=100×2 ", " codec=hevc ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the log lacks %q", want)
		}
	}
	if strings.Contains(got, " why=") {
		t.Error("the log names why the probe ran; only input starts one")
	}
}

// RTP that gives no frame (another payload type, fragments without their
// start) counts as no picture: the probe after input fails the stream, and
// the log says packets came.
func TestProbeUnreadableRTPFails(t *testing.T) {
	f, buf := probeStream(t, true)
	fu := []byte{hevcFU << 1, 1, 0x01, 0xEE} // a middle fragment
	for i := range 30 {
		f.rtp(127, i%3 == 2, f.ts+uint32(400*(i/3+1)), fu)
	}
	failAfterInput(t, f.m)
	got := buf.String()
	for _, want := range []string{" rtp_since_frame=30 ", " payload_types=\"100×2 127×30\" ", " last_pt=127 ", " frames=1 "} {
		if !strings.Contains(got, want) {
			t.Errorf("the log lacks %q:\n%s", want, got)
		}
	}
}

// Input that no frame follows is probed probeInput later, watched or not,
// and once: a frame answers the probe, and the same input starts no other.
// Input a frame follows, or no input, starts none.
func TestProbeAfterInput(t *testing.T) {
	f, _ := probeStream(t, false)
	m, t0 := f.m, time.Now()
	var in atomic.Int64
	m.setProbe(&in, 0, nil)
	if asks, _ := ticks(m, t0, t0.Add(time.Minute)); asks != 0 {
		t.Fatalf("%d requests without input", asks)
	}
	// Input the screen answers: no probe.
	in.Store(time.Now().Add(-time.Millisecond).UnixNano())
	f.ts += 400
	f.rtp(100, true, f.ts, hevcNAL(1, 0xBB))
	now := time.Now()
	if asks, _ := ticks(m, now, now.Add(5*time.Second)); asks != 0 {
		t.Fatalf("%d requests after input the screen answered", asks)
	}
	// Input with no frame after it.
	at := time.Now().Add(time.Millisecond)
	in.Store(at.UnixNano())
	if asks, _ := ticks(m, at, at.Add(probeInput-200*time.Millisecond)); asks != 0 {
		t.Fatal("a probe before probeInput")
	}
	if asks, _ := ticks(m, at.Add(probeInput), at.Add(probeInput)); asks != 1 || m.probe.at.IsZero() {
		t.Fatalf("%d requests probeInput after input; want a probe", asks)
	}
	f.keyFrame() // the phone answers
	if asks, err := ticks(m, at.Add(probeInput+200*time.Millisecond), at.Add(time.Minute)); asks != 0 || err != nil || m.probe.answered != 1 {
		t.Fatalf("after the answer: %d requests, %v, %d probes answered", asks, err, m.probe.answered)
	}
	// Input again, and no answer: the stream is broken.
	at = at.Add(time.Minute)
	in.Store(at.UnixNano())
	_, err := ticks(m, at, at.Add(probeInput+probeWait+time.Second))
	var pe *probeError
	if !errors.As(err, &pe) || m.probe.sent != 2 {
		t.Fatalf("an unanswered probe after input: %v, after %d probes", err, m.probe.sent)
	}
}

// A frame that comes while the probe decides answers it. Before it fails a
// stream the probe asks the phone whether its screen is off, which takes a
// while; a frame that came meanwhile once failed a stream that worked
// (since_frame=-100ms, twice in the owner's log).
func TestProbeFrameWhileDeciding(t *testing.T) {
	f, buf := probeStream(t, true)
	m := f.m
	var in atomic.Int64
	asked := 0
	m.setProbe(&in, 0, func() bool {
		asked++
		f.keyFrame() // comes while the phone is asked
		return false
	})
	at := time.Now().Add(time.Millisecond)
	in.Store(at.UnixNano())
	if _, err := ticks(m, at, at.Add(time.Minute)); err != nil || asked != 1 {
		t.Fatalf("%v, the screen asked about %d times", err, asked)
	}
	if m.probe.answered != 1 || m.Err() != nil || strings.Contains(buf.String(), "level=WARN") {
		t.Fatalf("the frame did not answer the probe: %d answered, %v\n%s", m.probe.answered, m.Err(), buf)
	}
}

// sr_missing is the packet count of the phone's last sender report minus
// the packets that had come when it did. A report sent between the packets
// of the last frame counts only those before it, so nothing is missing once
// the rest came, also when a later report counts them all. The field it
// replaced, sr_sent_since_frame, subtracted the count of the last report
// before the last frame, which can be a second older than the frame, so it
// counted packets that came (180 in #235's line, which lost none). Packets
// the report counts that never came are missing, the last ones too, which
// leave no gap in the sequence numbers for lost to count.
func TestProbeSRMissing(t *testing.T) {
	// lastFrame feeds a frame in four packets with a sender report before
	// the third, which counts the four packets that had come by then.
	lastFrame := func(f *fakeStream) {
		f.ts += 400
		for i, p := range fragments(4) {
			if i == 2 {
				f.srCount(4)
			}
			f.rtp(100, i == 3, f.ts, p)
		}
	}
	for _, c := range []struct {
		name string
		feed func(*fakeStream)
		want []string
	}{
		{"a report within the last frame", lastFrame,
			[]string{" sr_packets=4 ", " sr_missing=0 ", " packets=6 "}},
		{"and one after it", func(f *fakeStream) {
			lastFrame(f)
			f.srCount(6)
		}, []string{" sender_reports=2 ", " sr_packets=6 ", " sr_missing=0 ", " packets=6 "}},
		{"packets that never came", func(f *fakeStream) {
			f.ts += 400
			f.rtp(100, true, f.ts, hevcNAL(1, 0xBB))
			f.srCount(10) // 7 more than came
		}, []string{" sr_packets=10 ", " sr_missing=7 ", " packets=3 "}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, buf := probeStream(t, true)
			c.feed(f)
			failAfterInput(t, f.m)
			got := buf.String()
			for _, want := range append(c.want, " rtp_since_frame=0 ", " lost=0 ") {
				if !strings.Contains(got, want) {
					t.Errorf("the log lacks %q:\n%s", want, got)
				}
			}
		})
	}
}

// A probe fails no stream younger than its hold, nor one whose phone says
// its screen is off.
func TestProbeHoldAndDarkScreen(t *testing.T) {
	f, buf := probeStream(t, true)
	m := f.m
	dark := true
	var in atomic.Int64
	m.setProbe(&in, 20*time.Second, func() bool { return dark })
	hold := m.started.Add(20 * time.Second)
	at := time.Now().Add(time.Millisecond)
	in.Store(at.UnixNano())
	if _, err := ticks(m, at, hold.Add(-time.Millisecond)); err != nil || m.probe.dark != 0 {
		t.Fatalf("within its hold: %v, %d dark", err, m.probe.dark)
	}
	if _, err := ticks(m, hold, hold.Add(time.Minute)); err != nil {
		t.Fatalf("failed while the screen is off: %v", err)
	}
	if m.probe.dark != 1 || !strings.Contains(buf.String(), "while its screen is off") {
		t.Fatalf("dark probes %d, log:\n%s", m.probe.dark, buf)
	}
	dark = false
	at = hold.Add(time.Minute)
	in.Store(at.UnixNano())
	if _, err := ticks(m, at, at.Add(probeInput+probeWait+time.Second)); err == nil {
		t.Fatal("not failed once the screen is on")
	}
}

// A stream a probe fails ends its view, as any failed stream does: the
// mirror opens the stream again (ui: TestMirrorLogsItsLife), and the next
// view starts a new one, which may be failed by a probe only after a
// longer hold while streams keep breaking young.
func TestProbeFailureRestarts(t *testing.T) {
	var mu sync.Mutex
	var started []*media
	d := &Device{l: &link{}, done: make(chan struct{})}
	d.startFn = func(context.Context, *link, int) (*media, error) {
		f, _ := probeStream(t, false)
		mu.Lock()
		started = append(started, f.m)
		mu.Unlock()
		return f.m, nil
	}
	d.hidFn = func(goios.DeviceEntry) (*hidSession, error) { return &hidSession{}, nil }
	holdOf := func(m *media) time.Duration { return unixTime(m.probeHold.Load()).Sub(m.started) }

	view, err := d.Stream(context.Background(), driver.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first := started[0]
	if h := holdOf(first); h != probeHoldBase {
		t.Fatalf("the first stream's hold %v; want %v", h, probeHoldBase)
	}
	// Input that the phone shows nothing for.
	now := first.started.Add(probeHoldBase)
	d.lastInput.Store(now.UnixNano())
	_, perr := ticks(first, now, now.Add(probeInput+probeWait+time.Second))
	if perr == nil {
		t.Fatal("the probe did not find the stream broken")
	}
	first.fail(perr)
	if _, ok := <-view.Packets(); ok {
		// The key frame the view started at; then the end.
		for range view.Packets() {
		}
	}
	var pe *probeError
	if !errors.As(view.Err(), &pe) {
		t.Fatalf("the view ended with %v; want the probe's error", view.Err())
	}
	view.Close()

	// The mirror opens it again: a new stream, with a longer hold.
	view, err = d.Stream(context.Background(), driver.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	mu.Lock()
	n := len(started)
	mu.Unlock()
	if n != 2 || !first.stopped.Load() {
		t.Fatalf("%d streams started, the first stopped %v", n, first.stopped.Load())
	}
	if h := holdOf(started[1]); h != 2*probeHoldBase {
		t.Fatalf("the second stream's hold %v; want %v", h, 2*probeHoldBase)
	}
	d.mu.Lock()
	cur := d.m
	d.mu.Unlock()
	if cur != started[1] {
		t.Fatal("the device does not run the new stream")
	}
}

func TestProbeHoldGrows(t *testing.T) {
	for i, want := range []time.Duration{10, 20, 40, 80, 160, 160} {
		if got := probeHold(i); got != want*time.Second {
			t.Errorf("probeHold(%d) = %v; want %v", i, got, want*time.Second)
		}
	}
}
