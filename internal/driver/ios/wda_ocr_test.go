package ios

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/policy"
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

// A passcode keypad in a layer that WDA's pictures leave out (Trust, read
// through the helper): its keys come out marked not_in_screenshot, so the
// credentials rule refuses taps on it, as on the CoreDevice path; reads
// without a picture keep the marks. Keys the picture shows are not marked.
func TestWDAObserveMarksUncaptured(t *testing.T) {
	const w, h = 375, 812
	var keys []string
	var rects []screen.Rect
	for i, l := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "FaceID", "0", "Backspace"} {
		r := screen.Rect{X: 40 + (i%3)*100, Y: 360 + (i/3)*100, W: 80, H: 80}
		rects = append(rects, r)
		keys = append(keys, fmt.Sprintf(`{"type":"Button","label":%q,"rect":{"x":%d,"y":%d,"width":%d,"height":%d},"isVisible":"1","isAccessible":"1"}`,
			l, r.X, r.Y, r.W, r.H))
	}
	f := &fakeWDA{app: `{"bundleId":"sg.trust","name":"Trust","pid":8}`,
		source: fmt.Sprintf(`{"type":"Application","label":"Trust","rect":{"x":0,"y":0,"width":%d,"height":%d},"isVisible":"1",`+
			`"children":[{"type":"StaticText","label":"Enter your passcode","rect":{"x":80,"y":150,"width":215,"height":40},"isVisible":"1","isAccessible":"1"},%s]}`,
			w, h, strings.Join(keys, ","))}
	srv := httptest.NewServer(f)
	defer srv.Close()
	title := screen.Rect{X: 80, Y: 150, W: 215, H: 40}
	blank := shotWithText(w, h, title) // the keypad is left out
	drawn := shotWithText(w, h, append([]screen.Rect{title}, rects...)...)
	var pic atomic.Pointer[image.RGBA]
	pic.Store(blank)
	mj := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, pic.Load(), &jpeg.Options{Quality: 50}); err != nil {
			t.Error(err)
		}
		rw.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=BoundaryString")
		fmt.Fprintf(rw, "--BoundaryString\r\nContent-type: image/jpeg\r\nContent-Length: %d\r\n\r\n", b.Len())
		rw.Write(b.Bytes())
	}))
	defer mj.Close()
	d := &WDADevice{c: newWDAURL(srv.URL, mj.URL), width: w, height: h, scale: 1,
		info: driver.Info{Connection: "wifi"}}
	ctx := context.Background()

	marked := func(s *screen.Screen) (n int) {
		for _, e := range s.Elements {
			if e.Has(screen.StateNotInScreenshot) {
				n++
				if e.Label == "Enter your passcode" {
					t.Errorf("the title, which the picture shows, is marked")
				}
			}
		}
		return n
	}
	s, err := d.Observe(ctx, driver.ObserveOptions{OCR: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := marked(s); n != len(rects) {
		t.Fatalf("%d elements marked; want the %d keys", n, len(rects))
	}
	if _, ok := policy.HiddenKeypad(s); !ok {
		t.Fatal("policy does not see the hidden keypad")
	}
	// A read without OCR keeps the marks.
	s, err = d.Observe(ctx, driver.ObserveOptions{})
	if err != nil || marked(s) != len(rects) {
		t.Fatalf("read without a picture: %v, %d marked", err, marked(s))
	}
	// A picture that shows the keys clears them.
	pic.Store(drawn)
	s, err = d.Observe(ctx, driver.ObserveOptions{OCR: true})
	if err != nil || marked(s) != 0 {
		t.Fatalf("keys shown: %v, %d marked", err, marked(s))
	}
}
