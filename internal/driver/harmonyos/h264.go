package harmonyos

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/video"
)

const sdkJar = "hosScrcpy-1.0.18-beta.jar"

// Stream opens the tablet's H.264 encoder through the HOScrcpy SDK. The
// desktop owns this feed. Agent screenshots still use the checked capture path.
func (d *device) Stream(ctx context.Context, opt driver.StreamOptions) (driver.Stream, error) {
	dir := os.Getenv("PINKY_HARMONYOS_H264")
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("set PINKY_HARMONYOS_H264 to the HOScrcpy bridge directory")
	}
	for _, name := range []string{sdkJar, filepath.Join("rotation", "PinkyH264.class")} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return nil, fmt.Errorf("HOScrcpy bridge is missing %s: %w", name, err)
		}
	}
	java := os.Getenv("PINKY_HARMONYOS_JAVA")
	if java == "" {
		java = "java"
	}
	focus, err := d.focusedOwnerSurface(ctx)
	if err != nil {
		return nil, err
	}
	if focus.width == 0 || focus.height == 0 {
		return nil, fmt.Errorf("waiting for the HarmonyOS display layout")
	}
	limit := 1400
	if opt.MaxSize > 0 {
		limit = min(limit, opt.MaxSize)
	}
	scale := max(1, (max(focus.width, focus.height)+limit-1)/limit)
	fps := 60
	if opt.MaxFPS > 0 {
		fps = min(fps, opt.MaxFPS)
	}

	ctx, cancel := context.WithCancel(ctx)
	s := &mirrorStream{cancel: cancel, packets: make(chan driver.Packet, 8), done: make(chan struct{}), changed: make(chan struct{})}
	ready := make(chan struct{})
	go s.run(ctx, d, dir, java, focus, scale, fps, ready)
	timer := time.NewTimer(22 * time.Second)
	defer timer.Stop()
	select {
	case <-ready:
		return s, nil
	case <-s.done:
		return nil, s.Err()
	case <-timer.C:
		s.Close()
		return nil, fmt.Errorf("HOScrcpy timed out waiting for the first H.264 picture")
	case <-ctx.Done():
		s.Close()
		return nil, ctx.Err()
	}
}

type mirrorStream struct {
	cancel  context.CancelFunc
	packets chan driver.Packet
	done    chan struct{}
	w, h    int
	control io.Writer // immutable after the stream is published

	mu         sync.Mutex
	err        error
	frame      mirrorFrame
	checked    surface // Settings or Home verified for an agent read
	lastMotion time.Time
	changed    chan struct{}
	trace      *os.File
}

func (s *mirrorStream) Codec() driver.Codec           { return driver.H264 }
func (s *mirrorStream) Size() (int, int)              { return s.w, s.h }
func (s *mirrorStream) Packets() <-chan driver.Packet { return s.packets }
func (s *mirrorStream) Close() error                  { s.cancel(); <-s.done; return nil }
func (s *mirrorStream) Err() error                    { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

func (s *mirrorStream) run(ctx context.Context, d *device, dir, java string, focus surface, scale, fps int, ready chan struct{}) {
	defer close(s.done)
	defer close(s.packets)
	defer s.cancel()
	defer func() {
		d.previewMu.Lock()
		if d.preview == s {
			d.preview = nil
		}
		d.previewMu.Unlock()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.trace != nil {
			s.trace.Close()
			s.trace = nil
		}
	}()
	err := s.receive(ctx, d, dir, java, focus, scale, fps, ready)
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *mirrorStream) receive(ctx context.Context, d *device, dir, java string, focus surface, scale, fps int, ready chan struct{}) error {
	dec, err := video.NewDecoder(driver.H264, video.DecoderOptions{Software: true})
	if err != nil {
		return err
	}
	defer dec.Close()

	cmd := exec.CommandContext(ctx, java, "-XX:-UsePerfData", "-Djava.io.tmpdir="+dir,
		"-cp", filepath.Join(dir, "rotation")+string(os.PathListSeparator)+filepath.Join(dir, sdkJar), "PinkyH264",
		d.serial, d.provider.hdc, strconv.Itoa(scale), strconv.Itoa(fps))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TEMP="+dir, "TMP="+dir)
	hideConsole(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer in.Close()
	s.control = in
	cmd.Cancel = in.Close
	cmd.WaitDelay = 4 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer out.Close()
	if diag := os.Getenv("PINKY_HARMONYOS_H264_DIAGNOSTICS"); diag != "" {
		if log, err := os.OpenFile(filepath.Join(diag, "sdk.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600); err == nil {
			cmd.Stderr = log
			defer log.Close()
		}
		s.trace, _ = os.OpenFile(filepath.Join(diag, "timing.tsv"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start HOScrcpy: %w", err)
	}
	// Interrupt a blocked pipe read even if the SDK fails to close its
	// socket. Wait then gives the bridge a chance to stop the remote capture.
	stopRead := context.AfterFunc(ctx, func() { out.Close() })
	defer func() {
		s.cancel()
		stopRead()
		in.Close()
		out.Close()
		cmd.Wait()
	}()

	start := time.Now()
	var config []byte
	var pending []driver.Packet
	var previous []uint8
	small := image.NewRGBA(image.Rect(0, 0, 48, 48))
	live := false
	for {
		data, err := readAccessUnit(out)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("HOScrcpy H.264 feed ended: %w", err)
		}
		p, hasPicture := packetFromH264(data, time.Since(start))
		if p.Config {
			config = append(config, p.Data...)
			if len(config) > 64*1024 {
				return fmt.Errorf("HOScrcpy sent invalid H.264 parameters")
			}
			continue
		}
		if !hasPicture || (!live && len(pending) == 0 && !p.KeyFrame) {
			continue
		}
		batch := make([]driver.Packet, 0, 2)
		if len(config) != 0 {
			batch = append(batch, driver.Packet{Data: config, PTS: p.PTS, Config: true})
			config = nil
		}
		batch = append(batch, p)
		for _, packet := range batch {
			if err := dec.Send(packet); err != nil {
				return err
			}
		}
		if !live {
			pending = append(pending, batch...)
			if len(pending) > 30 {
				return fmt.Errorf("HOScrcpy sent H.264 without a decodable picture")
			}
		}
		pic, decoded, err := dec.Receive()
		if err != nil {
			return err
		}
		if decoded {
			if !live {
				current, err := d.focusedOwnerSurface(ctx)
				if err != nil {
					return err
				}
				if !matchingAspect(pic.Width, pic.Height, focus) {
					return fmt.Errorf("HOScrcpy picture does not match the HarmonyOS display")
				}
				if current.width != focus.width || current.height != focus.height {
					current = surface{} // wait for the next checked pointer transform
				}
				s.w, s.h = pic.Width, pic.Height
				s.frame = mirrorFrame{width: s.w, height: s.h, at: time.Now(), surface: current}
				d.previewMu.Lock()
				d.preview = s
				d.previewMu.Unlock()
				live = true
				close(ready)
				go s.watchFocus(ctx, d)
				batch, pending = pending, nil
			}
			if err := dec.Convert(small); err != nil {
				return err
			}
			sample := sampleImage(small)
			moving := !samePicture(previous, sample)
			now := time.Now()
			s.mu.Lock()
			if pic.Width != s.frame.width || pic.Height != s.frame.height {
				// The encoder can switch resolutions within one H.264 stream.
				// The pointer waits for a transform for the new orientation.
				s.frame = mirrorFrame{width: pic.Width, height: pic.Height}
				s.checked = surface{}
			}
			if moving {
				previous = sample
				s.lastMotion = now
				close(s.changed)
				s.changed = make(chan struct{})
			}
			s.logTimingLocked(now, "frame", moving)
			s.mu.Unlock()
		}
		if !live {
			continue
		}
		for _, packet := range batch {
			select {
			case <-ctx.Done():
				return nil
			case s.packets <- packet:
			}
		}
	}
}

// The SDK aligns scaled encoder dimensions to codec boundaries.
func matchingAspect(w, h int, focus surface) bool {
	return w > 0 && h > 0 && focus.width > 0 && focus.height > 0 && abs(w*focus.height-h*focus.width) <= 16*max(focus.width, focus.height)
}

func readAccessUnit(r io.Reader) ([]byte, error) {
	var length [4]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(length[:])
	if n == 0 || n > 8<<20 {
		return nil, fmt.Errorf("invalid HOScrcpy packet length %d", n)
	}
	data := make([]byte, n)
	_, err := io.ReadFull(r, data)
	return data, err
}

func packetFromH264(data []byte, pts time.Duration) (driver.Packet, bool) {
	p := driver.Packet{Data: data, PTS: pts}
	hasPicture, parameters := false, false
	for _, nalu := range video.SplitNALs(data) {
		switch video.NALType(driver.H264, nalu) {
		case 1, 2, 3, 4:
			hasPicture = true
		case 5:
			hasPicture, p.KeyFrame = true, true
		case 7, 8:
			parameters = true
		}
	}
	p.Config = parameters && !hasPicture
	return p, hasPicture
}

func (s *mirrorStream) watchFocus(ctx context.Context, d *device) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		check, cancel := context.WithTimeout(ctx, 2*time.Second)
		out, err := d.shell(check, focusCommand)
		cancel()
		if ctx.Err() != nil {
			return
		}
		var current, checked surface
		if err == nil {
			current, err = parseOwnerSurface(string(out))
			if err == nil {
				checked, _ = verifyFocusReport(out)
			}
		}
		s.mu.Lock()
		if err != nil {
			s.err = err
			s.frame = mirrorFrame{}
			s.checked = surface{}
			s.mu.Unlock()
			s.cancel()
			return
		}
		// Keep decoding during an app switch. Without a matched, verified
		// transform the picture is still visible, but input is suspended.
		if !matchingAspect(s.frame.width, s.frame.height, current) {
			current, checked = surface{}, surface{}
		}
		s.frame.surface = current
		s.frame.at = time.Now()
		s.checked = checked
		s.mu.Unlock()
	}
}

func (s *mirrorStream) logTimingLocked(at time.Time, event string, changed bool) {
	if s.trace != nil {
		fmt.Fprintf(s.trace, "%d\t%s\t%t\n", at.UnixMilli(), event, changed)
	}
}
