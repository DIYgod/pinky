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

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// Over Wi-Fi, OCR reads a frame of WDA's MJPEG server instead of the
// full-size screenshot; when no frame comes, the screenshot, and frames are
// not asked for again for frameRetry. Over a cable, always the screenshot.
func TestWDAOCRPicture(t *testing.T) {
	f := &fakeWDA{app: `{"bundleId":"com.apple.Preferences","name":"Settings","pid":8}`}
	srv := httptest.NewServer(f)
	defer srv.Close()
	frame := jpegBytes(t, 10, 20, color.White)
	var frames, mute atomic.Int32
	mj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		frames.Add(1)
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=BoundaryString")
		if mute.Load() != 0 {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		fmt.Fprintf(w, "--BoundaryString\r\nContent-type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame))
		w.Write(frame)
	}))
	defer mj.Close()
	defer func(w time.Duration) { frameWait = w }(frameWait)
	frameWait = 200 * time.Millisecond

	d := &WDADevice{c: newWDAURL(srv.URL, mj.URL), width: 100, height: 200, scale: 1,
		info: driver.Info{Connection: "wifi"}}
	ctx := context.Background()
	shots := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		n := f.shots
		f.shots = 0
		return n
	}
	pic, from, err := d.ocrPicture(ctx)
	if err != nil || from != "mjpeg" || len(pic) == 0 || shots() != 0 || frames.Load() != 1 {
		t.Fatalf("wifi: from %q, %d bytes, %v; %d screenshots", from, len(pic), err, shots())
	}
	if _, err := d.Observe(ctx, driver.ObserveOptions{OCR: true}); err != nil || shots() != 0 {
		t.Fatalf("observe over wifi: %v; %d screenshots", err, shots())
	}

	// The MJPEG server sends nothing: the screenshot, then only it.
	mute.Store(1)
	frames.Store(0)
	if _, from, _ := d.ocrPicture(ctx); from != "screenshot" || shots() != 1 || frames.Load() != 1 {
		t.Fatalf("no frame: from %q", from)
	}
	if _, from, _ := d.ocrPicture(ctx); from != "screenshot" || frames.Load() != 1 {
		t.Fatalf("within frameRetry: from %q, %d frames asked", from, frames.Load())
	}
	mute.Store(0)
	d.framesAfter.Store(time.Now().Add(-time.Second).UnixNano())
	if _, from, _ := d.ocrPicture(ctx); from != "mjpeg" {
		t.Fatalf("after frameRetry: from %q", from)
	}

	// Over a cable, the screenshot.
	d.info.Connection = "usb"
	frames.Store(0)
	if _, from, _ := d.ocrPicture(ctx); from != "screenshot" || frames.Load() != 0 {
		t.Fatalf("usb: from %q, %d frames", from, frames.Load())
	}
}

// The field report of 2026-11-26 (an iPad on iPadOS 26 over Wi-Fi): a read
// of Settings, opened over a game that runs in landscape, gave Settings'
// elements with OCR lines of the game's notice, from a picture that still
// showed the game. OCR's lines are placed by their share of the picture,
// so a picture turned the other way than the tree is another screen than
// the tree's, and would put every line in the wrong place: the read leaves
// its text out.
func TestWDAOCRPictureTurnedAway(t *testing.T) {
	srv := httptest.NewServer(&fakeWDA{app: `{"bundleId":"com.apple.Preferences","name":"Settings","pid":8}`})
	defer srv.Close()
	var turned atomic.Bool
	// The panel as it is, 8×12, tagged as turned for landscape: the game.
	game := withEXIF(t, jpegBytes(t, 8, 12, color.Black), 6, binary.BigEndian)
	settings := jpegBytes(t, 8, 12, color.White)
	mj := mjpegServer(t, func(time.Duration) []byte {
		if turned.Load() {
			return game
		}
		return settings
	})
	d := &WDADevice{c: newWDAURL(srv.URL, mj.URL), width: 100, height: 200, scale: 1,
		info: driver.Info{Connection: "wifi"},
		recognize: func([]byte) ([]ocrLine, error) {
			return []ocrLine{{Text: "Update notice", Confidence: 1, X: 0.1, Y: 0.5, W: 0.5, H: 0.05}}, nil
		}}
	read := func() *screen.Screen {
		t.Helper()
		s, err := d.Observe(context.Background(), driver.ObserveOptions{OCR: true})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	notice := func(s *screen.Screen) bool {
		for _, e := range s.Elements {
			if e.Source == screen.SourceOCR && e.Label == "Update notice" {
				return true
			}
		}
		return false
	}

	// A picture of the tree's screen: its text is read.
	if s := read(); !notice(s) {
		t.Fatalf("portrait picture, portrait tree: %s", s.Render())
	}
	// The landscape game's picture under Settings' portrait tree.
	turned.Store(true)
	if s := read(); notice(s) || s.App.ID != "com.apple.Preferences" || len(s.Elements) != 1 {
		t.Fatalf("landscape picture, portrait tree: %s", s.Render())
	}
}
