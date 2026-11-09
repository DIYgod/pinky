package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
)

// fastWatch makes phone_watch run in milliseconds: a "minute" is minute.
func fastWatch(t *testing.T, minute time.Duration) {
	t.Helper()
	m, r, o, p := watchMinute, watchReadEvery, watchOCREvery, watchProgressEvery
	watchMinute, watchReadEvery, watchOCREvery, watchProgressEvery = minute, 5*time.Millisecond, 50*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { watchMinute, watchReadEvery, watchOCREvery, watchProgressEvery = m, r, o, p })
}

// watchStep returns the task's last phone_watch step.
func watchStep(g *Gateway) (StepView, bool) {
	for _, task := range g.Tasks() {
		for i := len(task.Steps) - 1; i >= 0; i-- {
			if task.Steps[i].Tool == "phone_watch" {
				return task.Steps[i], true
			}
		}
	}
	return StepView{}, false
}

// startWatch runs a watch in the background and waits until the person
// sees it.
func startWatch(t *testing.T, g *Gateway, s *Session, req WatchRequest) (<-chan *Result, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var progress []string
	req.Progress = func(_, _ float64, msg string) {
		mu.Lock()
		defer mu.Unlock()
		progress = append(progress, msg)
	}
	sent := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), progress...)
	}
	done := make(chan *Result, 1)
	go func() { done <- s.Watch(context.Background(), req) }()
	waitUntil(t, "the watch step", func() bool {
		st, ok := watchStep(g)
		return ok && st.Status == "running"
	})
	return done, sent
}

func waitResult(t *testing.T, done <-chan *Result) *Result {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(10 * time.Second):
		t.Fatal("the watch did not end")
		return nil
	}
}

func TestWatchNotification(t *testing.T) {
	fastWatch(t, time.Minute)
	g, dev := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	done, progress := startWatch(t, g, s, WatchRequest{Text: "has arrived", Minutes: 30})
	if st, _ := watchStep(g); st.Text != "Watching for “has arrived”" {
		t.Fatalf("step while watching: %+v", st)
	}
	time.Sleep(60 * time.Millisecond)
	dev.Notify(demo.Dinely, "Your order from Dinely has arrived")
	res := waitResult(t, done)
	mustStatus(t, res, StatusSettled)
	if res.Matched == nil || !*res.Matched || !strings.Contains(res.Message, "“has arrived” appeared after") {
		t.Fatalf("result: %+v", res)
	}
	found := false
	for _, ev := range res.Screen.Events {
		found = found || strings.Contains(ev.Text, "has arrived")
	}
	if !found {
		t.Fatalf("the notification is not in the result: %+v", res.Screen.Events)
	}
	if p := progress(); len(p) == 0 || p[0] != "Watching for “has arrived”" {
		t.Fatalf("progress: %q", p)
	}
	st, _ := watchStep(g)
	if st.Text != "Waited for “has arrived”" || st.Status != string(StatusSettled) {
		t.Fatalf("step after the watch: %+v", st)
	}
	// The notification was handed to the agent once.
	res = s.Observe(context.Background(), ObserveRequest{})
	if len(res.Screen.Events) != 0 {
		t.Fatalf("events again: %+v", res.Screen.Events)
	}
}

func TestWatchTimesOut(t *testing.T) {
	fastWatch(t, 100*time.Millisecond)
	g, _ := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	res := s.Watch(context.Background(), WatchRequest{Text: "has arrived", Minutes: 1})
	mustStatus(t, res, StatusSettled)
	if res.Matched == nil || *res.Matched || !strings.Contains(res.Message, "has not appeared") || res.Screen == nil {
		t.Fatalf("result: %+v", res)
	}
	// Text already on the screen, waited to be gone.
	res = s.Watch(context.Background(), WatchRequest{Text: "Dinely", Gone: true, Minutes: 1})
	if res.Matched == nil || *res.Matched || !strings.Contains(res.Message, "has not disappeared") {
		t.Fatalf("gone result: %+v", res)
	}
}

func TestWatchArguments(t *testing.T) {
	g, _ := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	for _, req := range []WatchRequest{{Text: " "}, {Text: "x", Minutes: -1}, {Text: "x", Minutes: MaxWatchMinutes + 1}} {
		if res := s.Watch(context.Background(), req); res.Error != ErrInvalidArgument {
			t.Fatalf("%+v: %+v", req, res)
		}
	}
}

func TestWatchTakeOver(t *testing.T) {
	fastWatch(t, time.Minute)
	g, dev := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	done, progress := startWatch(t, g, s, WatchRequest{Text: "has arrived", Minutes: 30})
	if err := g.TakeOver("pixel"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the paused progress", func() bool {
		for _, p := range progress() {
			if p == "Paused while the person has the phone" {
				return true
			}
		}
		return false
	})
	// While the person has the phone, nothing is read: their notification
	// stays with the phone until they hand back.
	dev.Notify(demo.Dinely, "Your order from Dinely has arrived")
	time.Sleep(50 * time.Millisecond)
	select {
	case res := <-done:
		t.Fatalf("the watch read while the person had the phone: %+v", res)
	default:
	}
	if err := g.HandBack("pixel", ""); err != nil {
		t.Fatal(err)
	}
	res := waitResult(t, done)
	mustStatus(t, res, StatusSettled)
	if !*res.Matched || !strings.Contains(res.Message, "The person took over during the watch") {
		t.Fatalf("result: %+v", res)
	}
}

func TestWatchHandBackNote(t *testing.T) {
	fastWatch(t, time.Minute)
	g, _ := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	done, _ := startWatch(t, g, s, WatchRequest{Text: "has arrived", Minutes: 30})
	if err := g.TakeOver("pixel"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the watch to pause", func() bool { return g.Devices()[0].Status == DeviceInControl })
	time.Sleep(30 * time.Millisecond)
	if err := g.HandBack("pixel", "it was cancelled, stop waiting"); err != nil {
		t.Fatal(err)
	}
	res := waitResult(t, done)
	mustStatus(t, res, StatusSettled)
	if *res.Matched || res.Note != "it was cancelled, stop waiting" || !strings.Contains(res.Message, "handed the phone back with a note") {
		t.Fatalf("result: %+v", res)
	}
}

func TestWatchStillHeldByPerson(t *testing.T) {
	fastWatch(t, 100*time.Millisecond)
	g, _ := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	if err := g.TakeOver("pixel"); err != nil {
		t.Fatal(err)
	}
	res := s.Watch(context.Background(), WatchRequest{Text: "has arrived", Minutes: 1})
	mustStatus(t, res, StatusPaused)
	if *res.Matched || !strings.Contains(res.Message, "phone_handoff") {
		t.Fatalf("result: %+v", res)
	}
}

func TestWatchStop(t *testing.T) {
	fastWatch(t, time.Minute)
	g, _ := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	done, _ := startWatch(t, g, s, WatchRequest{Text: "has arrived", Minutes: 30})
	if err := g.Stop("pixel"); err != nil {
		t.Fatal(err)
	}
	res := waitResult(t, done)
	mustStatus(t, res, StatusStopped)
	st, _ := watchStep(g)
	if st.Text != "Waited for “has arrived”" || st.Status != string(StatusStopped) {
		t.Fatalf("step after stop: %+v", st)
	}
}

func TestWatchCancelled(t *testing.T) {
	fastWatch(t, time.Minute)
	g, _ := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *Result, 1)
	go func() { done <- s.Watch(ctx, WatchRequest{Text: "has arrived", Minutes: 30}) }()
	waitUntil(t, "the watch step", func() bool { st, ok := watchStep(g); return ok && st.Status == "running" })
	cancel()
	res := waitResult(t, done)
	if res.Error != ErrInternal || !strings.Contains(res.Message, "cancelled") {
		t.Fatalf("result: %+v", res)
	}
	// The task goes on.
	mustStatus(t, s.Observe(context.Background(), ObserveRequest{}), StatusSettled)
}

func TestWatchLockedPhone(t *testing.T) {
	fastWatch(t, 200*time.Millisecond)
	g, dev := newTestGateway(t)
	s, _ := connect(t, g, "Lunch")
	dev.SetLocked(true)
	// A locked screen does not count as the text being gone.
	res := s.Watch(context.Background(), WatchRequest{Text: "Dinely", Gone: true, Minutes: 1})
	mustStatus(t, res, StatusSettled)
	if *res.Matched || !strings.Contains(res.Message, "The phone is locked") {
		t.Fatalf("gone on a locked phone: %+v", res)
	}
	// A notification that arrives while it is locked counts.
	done, _ := startWatch(t, g, s, WatchRequest{Text: "has arrived", Minutes: 1})
	dev.Notify(demo.Dinely, "Your order from Dinely has arrived")
	res = waitResult(t, done)
	mustStatus(t, res, StatusSettled)
	if !*res.Matched || !strings.Contains(res.Message, "call phone_handoff so the person can unlock it") {
		t.Fatalf("notification on a locked phone: %+v", res)
	}
}
