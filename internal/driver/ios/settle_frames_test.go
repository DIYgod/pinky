package ios

import (
	"context"
	"encoding/binary"
	"fmt"
	"image/color"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// mjpegServer streams frames the way WebDriverAgent's MJPEG server does,
// one every 30 ms from the moment a client connects: pick returns the
// frame to send that long after the connection opened, or nil to send
// none.
func mjpegServer(t *testing.T, pick func(since time.Duration) []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opened := time.Now()
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=BoundaryString")
		w.(http.Flusher).Flush()
		tick := time.NewTicker(30 * time.Millisecond)
		defer tick.Stop()
		for {
			if b := pick(time.Since(opened)); b != nil {
				fmt.Fprintf(w, "--BoundaryString\r\nContent-type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(b))
				w.Write(b)
				w.(http.Flusher).Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A gap in the frames is no stillness: WDA's MJPEG server sends frames of
// a still screen too, and leaves them out only when it cannot take a
// picture, as while an app turns the screen. The same frame on both sides
// of a gap starts the quiet stretch again.
func TestWDASettleAfterGap(t *testing.T) {
	srv := httptest.NewServer(&fakeWDA{}) // no GET /rotation
	defer srv.Close()
	still := jpegBytes(t, 8, 12, color.White)
	const gapEnds = 800 * time.Millisecond
	mj := mjpegServer(t, func(since time.Duration) []byte {
		if since > 60*time.Millisecond && since < gapEnds {
			return nil
		}
		return still
	})
	d := &WDADevice{c: newWDAURL(srv.URL, mj.URL)}
	start := time.Now()
	ok, err := d.WaitSettled(context.Background(), 3*time.Second)
	if took := time.Since(start); err != nil || !ok || took < gapEnds+settleQuiet {
		t.Fatalf("settled %v (%v) after %v, want true %v after the gap", ok, err, took, settleQuiet)
	}
}

// Settings opened over a game that runs in landscape, on an iPad standing
// upright (field report, iPadOS 26, 2026-11-26): for a while the frames
// still show the game, still and turned its way, while GET /rotation
// already says Settings' portrait. The screen counts as settled once the
// frames are Settings' own and still; when frames and app agree, as soon
// as the frames are still.
func TestWDASettleWaitsForTheTurn(t *testing.T) {
	var z atomic.Int32 // GET /rotation's answer
	f := &fakeWDA{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rotation" {
			fmt.Fprintf(w, `{"value":{"x":0,"y":0,"z":%d}}`, z.Load())
			return
		}
		f.ServeHTTP(w, r)
	}))
	defer srv.Close()
	// The panel as it is, 8×12, tagged as turned for landscape: the game.
	game := withEXIF(t, jpegBytes(t, 8, 12, color.Black), 6, binary.BigEndian)
	settings := jpegBytes(t, 8, 12, color.White)
	const turnsAt = 900 * time.Millisecond
	mj := mjpegServer(t, func(since time.Duration) []byte {
		if since < turnsAt {
			return game
		}
		return settings
	})
	d := &WDADevice{c: newWDAURL(srv.URL, mj.URL)}
	ctx := context.Background()

	start := time.Now()
	ok, err := d.WaitSettled(ctx, 3*time.Second)
	if took := time.Since(start); err != nil || !ok || took < turnsAt+settleQuiet {
		t.Fatalf("settled %v (%v) after %v, want true once Settings' frames were still for %v", ok, err, took, settleQuiet)
	}
	// A limit that ends before the frames turn: not settled.
	if ok, err := d.WaitSettled(ctx, 700*time.Millisecond); err != nil || ok {
		t.Fatalf("frames of the game under Settings: settled %v (%v), want false at the limit", ok, err)
	}
	// The game in front, in landscape: its still frames are the screen.
	z.Store(90)
	start = time.Now()
	ok, err = d.WaitSettled(ctx, 3*time.Second)
	if took := time.Since(start); err != nil || !ok || took >= turnsAt {
		t.Fatalf("landscape app, landscape frames: settled %v (%v) after %v", ok, err, took)
	}
}
