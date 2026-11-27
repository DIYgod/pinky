package harmony

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/ocr"
	"github.com/DIYgod/pinky/internal/screen"
	"golang.org/x/image/draw"
)

type Device struct {
	info          driver.Info
	path          string
	run           func(context.Context, string, ...string) ([]byte, error)
	mu            sync.Mutex // UITest is single-session; serialize reads, input and screenshots.
	closed        bool
	labels        map[string]string
	apps          []driver.App
	appsAt        time.Time
	cancel        context.CancelFunc
	previewMu     sync.Mutex
	previewCancel context.CancelFunc
	inputs        int
	touchMu       sync.Mutex
	down          screen.Point
	downAt        time.Time
}

// User and agent input preempt the optional mirror capture, rather than
// waiting behind an entire screenshot. Input still checks the lock itself.
func (d *Device) preferInput() func() {
	d.previewMu.Lock()
	d.inputs++
	if d.previewCancel != nil {
		d.previewCancel()
	}
	d.previewMu.Unlock()
	return func() {
		d.previewMu.Lock()
		d.inputs--
		d.previewMu.Unlock()
	}
}

func (d *Device) Info() driver.Info { return d.info }

func (d *Device) shell(ctx context.Context, args ...string) ([]byte, error) {
	return d.shellScript(ctx, shellCommand(args...))
}

func (d *Device) shellScript(ctx context.Context, command string) ([]byte, error) {
	if d.closed {
		return nil, driver.ErrOffline
	}
	return d.run(ctx, d.path, "-t", d.info.ID, "shell", command)
}

func (d *Device) locked(ctx context.Context) (bool, error) {
	out, err := d.shell(ctx, "hidumper", "-s", "ScreenlockService", "-a", "-all")
	if err != nil {
		return false, err
	}
	return parseLock(string(out))
}

func (d *Device) Locked(ctx context.Context) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.locked(ctx)
}

func (d *Device) requireUnlocked(ctx context.Context) error {
	locked, err := d.locked(ctx)
	if err != nil {
		return err
	}
	if locked {
		return driver.ErrLocked
	}
	return nil
}

// tempOutput reads an explicitly named native output and removes it before
// returning. Images are never written to the host filesystem or cached.
func (d *Device) tempOutput(ctx context.Context, tool, ext string, extra ...string) ([]byte, error) {
	return d.tempOutputChecked(ctx, tool, ext, nil, extra...)
}

func (d *Device) tempOutputChecked(ctx context.Context, tool, ext string, checkHeader func([]byte) error, extra ...string) ([]byte, error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	remote := "/data/local/tmp/pinky-" + hex.EncodeToString(nonce[:]) + ext
	cleaned := false
	defer func() {
		if cleaned {
			return
		}
		clean, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = d.shell(clean, "rm", "-f", remote)
	}()
	if tool == "snapshot_display" {
		// Keep capture, transport and cleanup in one shell session. A trap
		// also removes the file if capture/encoding fails on the device;
		// the deferred command is a fallback when the session is interrupted.
		const marker = "__PINKY_JPEG__"
		args := append([]string{tool, "-f", remote, "-t", "jpeg"}, extra...)
		command := shellCommand("trap", shellCommand("rm", "-f", remote), "EXIT") + "; " +
			shellCommand(args...) + " && " +
			shellCommand("printf", "\n"+marker+"\n") + " && " + shellCommand("base64", remote)
		out, err := d.shellScript(ctx, command)
		if err != nil {
			return nil, err
		}
		header, payload, ok := bytes.Cut(out, []byte(marker))
		if !ok || !bytes.Contains(header, []byte("success: snapshot display")) {
			return nil, fmt.Errorf("harmony: snapshot did not produce output")
		}
		if checkHeader != nil {
			if err := checkHeader(header); err != nil {
				return nil, err
			}
		}
		raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(payload)), ""))
		cleaned = err == nil
		return raw, err
	}
	args := append([]string{"uitest", tool, "-p", remote}, extra...)
	out, err := d.shell(ctx, args...)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(out, []byte("saved to")) {
		return nil, fmt.Errorf("harmony: %s did not produce output: %s", tool, strings.TrimSpace(string(out)))
	}
	if ext == ".png" || ext == ".jpeg" {
		encoded, err := d.shell(ctx, "base64", remote)
		if err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(encoded)), ""))
	}
	return d.shell(ctx, "cat", remote)
}

// screenState batches the two read-only guards without caching either one.
// Every capture still checks both lock and visible windows before and after.
func (d *Device) screenState(ctx context.Context) (string, error) {
	out, err := d.shellScript(ctx, shellCommand("hidumper", "-s", "ScreenlockService", "-a", "-all")+" && "+
		shellCommand("hidumper", "-s", "WindowManagerService", "-a", "-a"))
	if err != nil {
		return "", err
	}
	locked, err := parseLock(string(out))
	if err != nil {
		return "", err
	}
	if locked {
		return "", driver.ErrLocked
	}
	focus, err := focusedWindow(string(out))
	if err != nil {
		return "", err
	}
	return focus, safeWindow(string(out), focus)
}

func (d *Device) tree(ctx context.Context) (*screen.Screen, error) {
	locked, err := d.locked(ctx)
	if err != nil {
		return nil, err
	}
	if locked {
		return &screen.Screen{Locked: true, CapturedAt: time.Now()}, nil
	}
	out, err := d.shell(ctx, "hidumper", "-s", "WindowManagerService", "-a", "-a")
	if err != nil {
		return nil, err
	}
	focus, err := focusedWindow(string(out))
	if err != nil {
		return nil, err
	}
	if err := safeWindow(string(out), focus); err != nil {
		return nil, err
	}
	raw, err := d.tempOutput(ctx, "dumpLayout", ".json", "-w", focus)
	if err != nil {
		return nil, err
	}
	s, err := parseLayout(raw, focus)
	if err != nil {
		return nil, err
	}
	if err := d.requireUnlocked(ctx); err != nil {
		return nil, err
	}
	s.App.Name = d.label(ctx, s.App.ID)
	return s, nil
}

func (d *Device) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, err := d.tree(ctx)
	if err != nil || s.Locked {
		return s, err
	}
	missing := false
	for _, e := range s.Elements {
		if e.Role == screen.RoleButton && e.Label == "" {
			missing = true
			break
		}
	}
	if (opt.OCR || missing) && ocr.Supported() == nil && (opt.MayCapture == nil || opt.MayCapture(s)) {
		img, err := d.capture(ctx)
		if err != nil {
			return nil, err
		}
		if img.Bounds().Dx() != s.Width || img.Bounds().Dy() != s.Height {
			return nil, fmt.Errorf("harmony: screenshot coordinates changed")
		}
		lines, err := ocr.RecognizeImage(img, ocr.Options{Languages: ocr.Languages})
		if err != nil {
			return nil, err
		}
		after, err := d.tree(ctx)
		if err != nil {
			return nil, err
		}
		if after.Locked || after.App.ID != s.App.ID {
			return nil, fmt.Errorf("harmony: screen changed during OCR")
		}
		s.Elements = append(s.Elements, ocr.Extras(s.Elements, lines, s.Width, s.Height)...)
	}
	return s, nil
}

func (d *Device) capture(ctx context.Context) (image.Image, error) {
	return d.capturePreview(ctx, image.Point{})
}

var errDisplayChanged = errors.New("harmony: display dimensions changed")

func displaySize(header []byte) image.Point {
	for _, line := range strings.Split(string(header), "\n") {
		var id, w, h int
		var kind string
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "process: display %d, file type: %s width: %d, height: %d", &id, &kind, &w, &h); err == nil && w > 0 && h > 0 {
			return image.Pt(w, h)
		}
	}
	return image.Point{}
}

// The live mirror captures fewer pixels, then restores the native coordinate
// space on the host. Agent screenshots and OCR retain full resolution.
func (d *Device) capturePreview(ctx context.Context, native image.Point) (image.Image, error) {
	focus, err := d.screenState(ctx)
	if err != nil {
		return nil, err
	}
	// JPEG keeps a busy home screen small enough for HDC's shell channel.
	// Raw binary shell output is unsafe: HDC transforms control bytes, so
	// even compressed images are transported as base64 and decoded here.
	var raw []byte
	want := native
	scaled := false
	if side := max(native.X, native.Y); side > 1600 {
		w, h := max(2, native.X*1600/side)&^1, max(2, native.Y*1600/side)&^1
		want, scaled = image.Pt(w, h), true
		raw, err = d.tempOutputChecked(ctx, "snapshot_display", ".jpeg", func(header []byte) error {
			actual := displaySize(header)
			if actual == (image.Point{}) {
				return fmt.Errorf("harmony: snapshot lacks native display dimensions")
			}
			if actual != native {
				return errDisplayChanged
			}
			return nil
		}, "-w", fmt.Sprint(w), "-h", fmt.Sprint(h))
	} else {
		raw, err = d.tempOutput(ctx, "snapshot_display", ".jpeg")
	}
	if err != nil {
		return nil, err
	}
	focusAfter, err := d.screenState(ctx)
	if err != nil {
		return nil, err
	}
	if focusAfter != focus {
		return nil, fmt.Errorf("harmony: window changed during screenshot")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err == nil && native.X > 0 && img.Bounds().Size() != want {
		return nil, errDisplayChanged
	}
	if err == nil && scaled {
		full := image.NewRGBA(image.Rectangle{Max: native})
		draw.ApproxBiLinear.Scale(full, full.Bounds(), img, img.Bounds(), draw.Src, nil)
		img = full
	}
	return img, err
}

func (d *Device) Screenshot(ctx context.Context) (image.Image, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.capture(ctx)
}
func (*Device) ReadsText() bool { return ocr.Supported() == nil }

func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	return nil
}

var _ driver.Device = (*Device)(nil)
var _ driver.LockReporter = (*Device)(nil)
var _ driver.TextReader = (*Device)(nil)
