// Command androidctl exercises Pinky's Android driver from a terminal, for
// development and acceptance tests.
//
//	androidctl devices
//	androidctl [-s serial] observe [-json]
//	androidctl [-s serial] screenshot FILE.png
//	androidctl [-s serial] tap X Y [-long]
//	androidctl [-s serial] swipe X1 Y1 X2 Y2 [MS]
//	androidctl [-s serial] type [-append] [-submit] TEXT
//	androidctl [-s serial] key NAME          (back, home, recents, enter, ...)
//	androidctl [-s serial] launch APP        (package or display name)
//	androidctl [-s serial] apps
//	androidctl [-s serial] locked
//	androidctl [-s serial] stream [-seconds N] [-max-size N] [-out FILE.h264]
//	androidctl [-s serial] settle [-max MS]  (time until the screen is still)
//	androidctl [-s serial] run STEP...       (steps with action and settle timings)
//	androidctl [-s serial] bg [-app APP] [-out FILE.h264] [-seconds N] [STEP...]
//
// Steps for run and bg: tap:X,Y  longtap:X,Y  swipe:X1,Y1,X2,Y2[,MS]  key:NAME
// type:TEXT  launch:APP  sleep:MS  observe  shot:FILE.png
//
// Run commands that touch a phone through scripts/devlock.sh.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android"
	"github.com/DIYgod/pinky/internal/screen"
)

func main() {
	serial := flag.String("s", os.Getenv("ANDROID_SERIAL"), "device serial (default: the only device)")
	verbose := flag.Bool("v", false, "print driver debug messages")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() == 0 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cfg := android.Config{}
	if *verbose {
		cfg.Logf = func(f string, a ...any) { fmt.Fprintf(os.Stderr, "\u00b7 "+f+"\n", a...) }
	}
	p := android.NewProvider(cfg)
	cmd, args := flag.Arg(0), flag.Args()[1:]
	if err := run(ctx, p, *serial, cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "androidctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: androidctl [-s serial] [-v] command [args]

commands:
  devices                         list phones
  observe [-json]                 print the screen as agents read it
  screenshot FILE.png             save a screenshot
  tap X Y [-long]                 tap, or long-press with -long
  swipe X1 Y1 X2 Y2 [MS]          swipe (default 300 ms)
  type [-append] [-submit] TEXT   type into the focused field
  key NAME                        back, home, recents, enter, tab, delete, ...
  launch APP                      start an app by package or name
  apps                            list launcher apps
  locked                          print whether the lock screen shows
  stream [-seconds N] [-max-size N] [-out FILE.h264]
                                  video statistics, optionally saving the stream
  settle [-max MS]                time until the screen stops changing
  run STEP...                     run steps, timing each action and its settle
  bg [-app APP] [-seconds N] [-out FILE.h264] [STEP...]
                                  open a virtual display and run steps on it

steps: tap:X,Y longtap:X,Y swipe:X1,Y1,X2,Y2[,MS] key:NAME type:TEXT
       launch:APP sleep:MS observe shot:FILE.png
`)
}

func run(ctx context.Context, p *android.Provider, serial, cmd string, args []string) error {
	if cmd == "devices" {
		return devices(ctx, p)
	}
	if serial == "" {
		infos, err := p.List(ctx)
		if err != nil {
			return err
		}
		var online []string
		for _, i := range infos {
			if i.State == driver.StateOnline {
				online = append(online, i.ID)
			}
		}
		if len(online) != 1 {
			return fmt.Errorf("%d phones online; choose one with -s", len(online))
		}
		serial = online[0]
	}
	d, err := p.OpenDevice(ctx, serial)
	if err != nil {
		return err
	}
	defer d.Close()
	switch cmd {
	case "observe":
		fs := flag.NewFlagSet("observe", flag.ExitOnError)
		asJSON := fs.Bool("json", false, "print JSON")
		fs.Parse(args)
		return observe(ctx, d, *asJSON)
	case "screenshot":
		if len(args) != 1 {
			return errors.New("usage: screenshot FILE.png")
		}
		return screenshot(ctx, d, args[0])
	case "tap":
		fs := flag.NewFlagSet("tap", flag.ExitOnError)
		long := fs.Bool("long", false, "long press")
		fs.Parse(reorder(args))
		x, y, err := ints2(fs.Args())
		if err != nil {
			return err
		}
		return timed("tap", func() error { return d.Tap(ctx, screen.Point{X: x, Y: y}, driver.TapOptions{LongPress: *long}) })
	case "swipe":
		if len(args) < 4 {
			return errors.New("usage: swipe X1 Y1 X2 Y2 [MS]")
		}
		v, err := atois(args)
		if err != nil {
			return err
		}
		dur := 300 * time.Millisecond
		if len(v) > 4 {
			dur = time.Duration(v[4]) * time.Millisecond
		}
		return timed("swipe", func() error {
			return d.Swipe(ctx, screen.Point{X: v[0], Y: v[1]}, screen.Point{X: v[2], Y: v[3]}, dur)
		})
	case "type":
		fs := flag.NewFlagSet("type", flag.ExitOnError)
		appendText := fs.Bool("append", false, "keep the field's text")
		submit := fs.Bool("submit", false, "press Enter afterwards")
		fs.Parse(args)
		text := strings.Join(fs.Args(), " ")
		return timed("type", func() error {
			return d.Type(ctx, text, driver.TypeOptions{Append: *appendText, Submit: *submit})
		})
	case "key":
		if len(args) != 1 {
			return errors.New("usage: key NAME")
		}
		return timed("key", func() error { return d.Key(ctx, driver.Key(args[0])) })
	case "launch":
		if len(args) == 0 {
			return errors.New("usage: launch APP")
		}
		return timed("launch", func() error { return d.Launch(ctx, strings.Join(args, " ")) })
	case "apps":
		apps, err := d.Apps(ctx)
		if err != nil {
			return err
		}
		for _, a := range apps {
			sys := " "
			if a.System {
				sys = "*"
			}
			fmt.Printf("%s %-40s %s\n", sys, a.ID, a.Name)
		}
		return nil
	case "locked":
		l, err := d.Locked(ctx)
		if err != nil {
			return err
		}
		fmt.Println(l)
		return nil
	case "stream":
		fs := flag.NewFlagSet("stream", flag.ExitOnError)
		secs := fs.Float64("seconds", 5, "how long to read")
		maxSize := fs.Int("max-size", 0, "longest side in pixels")
		out := fs.String("out", "", "write the H.264 stream (Annex B) to this file")
		fs.Parse(args)
		st, err := d.Stream(ctx, driver.StreamOptions{MaxSize: *maxSize})
		if err != nil {
			return err
		}
		return streamStats(ctx, st, time.Duration(*secs*float64(time.Second)), *out)
	case "settle":
		fs := flag.NewFlagSet("settle", flag.ExitOnError)
		maxMS := fs.Int("max", 3000, "limit in milliseconds")
		fs.Parse(args)
		start := time.Now()
		ok, err := d.WaitSettled(ctx, time.Duration(*maxMS)*time.Millisecond)
		if err != nil {
			return err
		}
		fmt.Printf("settled=%v after %v\n", ok, time.Since(start).Round(time.Millisecond))
		return nil
	case "run":
		return runSteps(ctx, d, d, args)
	case "bg":
		return background(ctx, d, args)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func devices(ctx context.Context, p *android.Provider) error {
	infos, err := p.List(ctx)
	if err != nil {
		return err
	}
	for _, i := range infos {
		fmt.Printf("%-16s %-12s %-14s Android %-4s %-4s %s", i.ID, i.State, i.Name, i.OSVersion, i.Connection, i.Model)
		if i.Detail != "" {
			fmt.Printf("  (%s)", i.Detail)
		}
		fmt.Println()
	}
	return nil
}

func observe(ctx context.Context, d *android.Device, asJSON bool) error {
	start := time.Now()
	s, err := d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		return err
	}
	took := time.Since(start)
	screen.NewRefs().Assign(s)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	}
	fmt.Println(s.Render())
	fmt.Fprintf(os.Stderr, "(%d elements in %v)\n", len(s.Elements), took.Round(time.Millisecond))
	return nil
}

func screenshot(ctx context.Context, d driver.Device, path string) error {
	start := time.Now()
	img, err := d.Screenshot(ctx)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	b := img.Bounds()
	fmt.Fprintf(os.Stderr, "%dx%d screenshot in %v \u2192 %s\n", b.Dx(), b.Dy(), time.Since(start).Round(time.Millisecond), path)
	return nil
}

func timed(what string, f func() error) error {
	start := time.Now()
	if err := f(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s done in %v\n", what, time.Since(start).Round(time.Millisecond))
	return nil
}

func streamStats(ctx context.Context, st driver.Stream, dur time.Duration, out string) error {
	defer st.Close()
	var w io.Writer
	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	timer := time.NewTimer(dur)
	defer timer.Stop()
	start := time.Now()
	var first, last time.Time
	var frames, keys, configs int
	var bytes int64
	var sizes []int
loop:
	for {
		select {
		case p, ok := <-st.Packets():
			if !ok {
				if err := st.Err(); err != nil {
					return err
				}
				break loop
			}
			now := time.Now()
			if w != nil {
				w.Write(p.Data)
			}
			if p.Config {
				configs++
				continue
			}
			if first.IsZero() {
				first = now
			}
			last = now
			frames++
			bytes += int64(len(p.Data))
			sizes = append(sizes, len(p.Data))
			if p.KeyFrame {
				keys++
			}
		case <-timer.C:
			break loop
		case <-ctx.Done():
			break loop
		}
	}
	vw, vh := st.Size()
	elapsed := time.Since(start)
	fmt.Printf("video %dx%d %s, %v\n", vw, vh, st.Codec(), elapsed.Round(time.Millisecond))
	fmt.Printf("frames %d (key frames %d, config packets %d), %d bytes\n", frames, keys, configs, bytes)
	if frames > 1 {
		span := last.Sub(first).Seconds()
		fmt.Printf("frame rate %.1f fps over %.2fs of frames; %.1f fps over the whole run\n",
			float64(frames-1)/span, span, float64(frames)/elapsed.Seconds())
		fmt.Printf("bit rate %.2f Mbit/s over the whole run\n", float64(bytes)*8/elapsed.Seconds()/1e6)
		sort.Ints(sizes)
		fmt.Printf("frame bytes: min %d, median %d, p90 %d, max %d\n",
			sizes[0], sizes[len(sizes)/2], sizes[len(sizes)*9/10], sizes[len(sizes)-1])
	}
	return nil
}

// stepper is what run steps need: a device, optionally with settle.
type stepper interface {
	driver.Device
	driver.Settler
}

type statser interface {
	VideoStats(ctx context.Context, since time.Time) (android.VideoStats, error)
}

// trace prints every video packet of each step when set (ANDROIDCTL_TRACE=1).
var trace = os.Getenv("ANDROIDCTL_TRACE") != ""

func runSteps(ctx context.Context, d stepper, st statser, steps []string) error {
	runStart := time.Now()
	defer videoSummary(ctx, st, runStart)
	var settleTimes []time.Duration
	for i, step := range steps {
		if i > 0 {
			// Pace input like a person.
			time.Sleep(300 * time.Millisecond)
		}
		kind, arg, _ := strings.Cut(step, ":")
		start := time.Now()
		var err error
		action := true
		switch kind {
		case "tap", "longtap":
			var x, y int
			x, y, err = ints2(strings.Split(arg, ","))
			if err == nil {
				err = d.Tap(ctx, screen.Point{X: x, Y: y}, driver.TapOptions{LongPress: kind == "longtap"})
			}
		case "swipe":
			var v []int
			v, err = atois(strings.Split(arg, ","))
			if err == nil && len(v) < 4 {
				err = errors.New("swipe needs X1,Y1,X2,Y2")
			}
			if err == nil {
				dur := 300 * time.Millisecond
				if len(v) > 4 {
					dur = time.Duration(v[4]) * time.Millisecond
				}
				err = d.Swipe(ctx, screen.Point{X: v[0], Y: v[1]}, screen.Point{X: v[2], Y: v[3]}, dur)
			}
		case "key":
			err = d.Key(ctx, driver.Key(arg))
		case "type":
			err = d.Type(ctx, arg, driver.TypeOptions{})
		case "launch":
			err = d.Launch(ctx, arg)
		case "sleep":
			action = false
			var ms int
			ms, err = strconv.Atoi(arg)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		case "observe":
			action = false
			var s *screen.Screen
			s, err = d.Observe(ctx, driver.ObserveOptions{})
			if err == nil {
				screen.NewRefs().Assign(s)
				fmt.Println(s.Render())
				fmt.Printf("(observe took %v)\n", time.Since(start).Round(time.Millisecond))
			}
		case "shot":
			action = false
			err = screenshot(ctx, d, arg)
		default:
			err = fmt.Errorf("unknown step %q", step)
		}
		if err != nil {
			return fmt.Errorf("step %d %q: %w", i+1, step, err)
		}
		if !action {
			continue
		}
		acted := time.Now()
		ok, err := d.WaitSettled(ctx, 0)
		if err != nil {
			return fmt.Errorf("step %d %q: settle: %w", i+1, step, err)
		}
		done := time.Now()
		total := done.Sub(start)
		settleTimes = append(settleTimes, total)
		line := fmt.Sprintf("%-28s action %6v  settle %6v  total %6v  settled=%v", step,
			acted.Sub(start).Round(time.Millisecond), done.Sub(acted).Round(time.Millisecond), total.Round(time.Millisecond), ok)
		if vs, err := st.VideoStats(ctx, start); err == nil {
			var n, changes, bytes int
			for _, p := range vs.Packets {
				if p.Config {
					continue
				}
				n++
				bytes += p.Size
				if p.Change {
					changes++
				}
			}
			line += fmt.Sprintf("  frames %d (changes %d), %d bytes", n, changes, bytes)
			if trace {
				var prev time.Duration
				for i, p := range vs.Packets {
					gap := time.Duration(0)
					if i > 0 {
						gap = p.PTS - prev
					}
					prev = p.PTS
					flags := ""
					if p.Config {
						flags += " config"
					}
					if p.KeyFrame {
						flags += " key"
					}
					if p.Change {
						flags += " CHANGE"
					}
					line += fmt.Sprintf("\n    +%5dms  pts gap %4dms  %7d B%s", p.At.Sub(start).Milliseconds(), gap.Milliseconds(), p.Size, flags)
				}
			}
		}
		fmt.Println(line)
	}
	if len(settleTimes) > 0 {
		sorted := append([]time.Duration(nil), settleTimes...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		fmt.Printf("action\u2192settled over %d actions: median %v, min %v, max %v\n", len(sorted),
			sorted[len(sorted)/2].Round(time.Millisecond), sorted[0].Round(time.Millisecond), sorted[len(sorted)-1].Round(time.Millisecond))
	}
	return nil
}

// videoSummary prints the frame rate and bit rate of the video since
// start: the average over the whole run and the busiest second.
func videoSummary(ctx context.Context, st statser, start time.Time) {
	vs, err := st.VideoStats(ctx, start)
	if err != nil {
		return
	}
	elapsed := time.Since(start)
	var frames, bytes int
	var times []time.Time
	var sizes []int
	for _, p := range vs.Packets {
		if p.Config {
			continue
		}
		frames++
		bytes += p.Size
		times = append(times, p.At)
		sizes = append(sizes, p.Size)
	}
	if frames == 0 || elapsed <= 0 {
		return
	}
	// Busiest one-second window.
	var peakFrames, peakBytes, j, wbytes int
	for i := range times {
		wbytes += sizes[i]
		for times[i].Sub(times[j]) >= time.Second {
			wbytes -= sizes[j]
			j++
		}
		if n := i - j + 1; n > peakFrames {
			peakFrames = n
		}
		peakBytes = max(peakBytes, wbytes)
	}
	fmt.Printf("video %dx%d %s over %v: %d frames, %.1f fps and %.2f Mbit/s on average; busiest second %d fps, %.2f Mbit/s\n",
		vs.Width, vs.Height, vs.Codec, elapsed.Round(time.Millisecond), frames,
		float64(frames)/elapsed.Seconds(), float64(bytes)*8/elapsed.Seconds()/1e6, peakFrames, float64(peakBytes)*8/1e6)
}

func background(ctx context.Context, d *android.Device, args []string) error {
	fs := flag.NewFlagSet("bg", flag.ExitOnError)
	app := fs.String("app", "", "app to start on the virtual display")
	out := fs.String("out", "", "write the display's H.264 stream to this file")
	secs := fs.Float64("seconds", 2, "seconds to keep streaming after the steps")
	fs.Parse(args)
	start := time.Now()
	b, err := d.OpenBackground(ctx, android.BackgroundOptions{App: *app})
	if err != nil {
		return err
	}
	defer b.Close()
	fmt.Printf("virtual display %d ready in %v\n", b.DisplayID(), time.Since(start).Round(time.Millisecond))
	var st driver.Stream
	var done chan struct{}
	if *out != "" {
		st, err = b.Stream(ctx, driver.StreamOptions{})
		if err != nil {
			return err
		}
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		done = make(chan struct{})
		go func() {
			defer close(done)
			for p := range st.Packets() {
				f.Write(p.Data)
			}
		}()
	}
	if _, err := b.WaitSettled(ctx, 0); err != nil {
		return err
	}
	if err := runSteps(ctx, b, b, fs.Args()); err != nil {
		return err
	}
	time.Sleep(time.Duration(*secs * float64(time.Second)))
	if st != nil {
		st.Close()
		<-done
	}
	return nil
}

func ints2(args []string) (int, int, error) {
	v, err := atois(args)
	if err != nil {
		return 0, 0, err
	}
	if len(v) != 2 {
		return 0, 0, errors.New("want X Y")
	}
	return v[0], v[1], nil
}

func atois(args []string) ([]int, error) {
	out := make([]int, len(args))
	for i, a := range args {
		n, err := strconv.Atoi(strings.TrimSpace(a))
		if err != nil {
			return nil, fmt.Errorf("not a number: %q", a)
		}
		out[i] = n
	}
	return out, nil
}

// reorder moves flags in front of positional arguments, so "tap 1 2 -long"
// works like "tap -long 1 2".
func reorder(args []string) []string {
	var flags, rest []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") && len(a) > 1 && (a[1] < '0' || a[1] > '9') {
			flags = append(flags, a)
		} else {
			rest = append(rest, a)
		}
	}
	return append(flags, rest...)
}
