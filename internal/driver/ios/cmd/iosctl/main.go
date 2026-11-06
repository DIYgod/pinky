// Command iosctl drives Pinky's iOS driver by hand, for debugging.
//
//	iosctl devices
//	iosctl [-udid U] rsd
//	iosctl [-udid U] observe [-ocr] [-n 1]
//	iosctl [-udid U] tap X Y [-long]
//	iosctl [-udid U] swipe X1 Y1 X2 Y2 [-ms 300]
//	iosctl [-udid U] key home|back|enter|delete|...
//	iosctl [-udid U] type TEXT [-append] [-submit]
//	iosctl [-udid U] stream [-seconds 10] [-out /tmp/pinky-stream.hevc]
//	iosctl [-udid U] latency [-n 6] [-vertical]
//	iosctl [-udid U] screenshot [-out /tmp/pinky-shot.png]
//	iosctl [-udid U] launch APP
//	iosctl [-udid U] apps
//	iosctl [-udid U] locked
//
// Coordinates are screenshot pixels (1125x2436 on an iPhone 13 mini).
// Without -udid it uses the only connected iPhone. Hold the device lock
// while it runs: scripts/devlock.sh <udid> iosctl ...
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	pios "github.com/DIYgod/pinky/internal/driver/ios"
	"github.com/DIYgod/pinky/internal/screen"
)

func main() {
	udid := flag.String("udid", "", "device UDID (default: the only iPhone)")
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() == 0 {
		usage()
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	cmd, args := flag.Arg(0), flag.Args()[1:]
	if err := run(ctx, cmd, *udid, args); err != nil {
		fmt.Fprintln(os.Stderr, "iosctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: iosctl [-udid UDID] COMMAND [ARGS]

commands:
  devices                       list iPhones and what they need
  rsd                           open a tunnel and list the device's services
  observe [-ocr] [-n N]         print the screen as agents read it
  tap X Y [-long]               tap at screenshot pixels
  swipe X1 Y1 X2 Y2 [-ms 300]   swipe
  key KEY                       home, back, enter, delete, tab, escape, dpad_*
  type TEXT [-append] [-submit] type ASCII text into the focused field
  stream [-seconds 10] [-out F] video statistics; -out writes Annex B HEVC
  latency [-n 6] [-vertical]    swipe and time the video: home screen pages, or
                                with -vertical scroll the current screen
  screenshot [-out F]           save a PNG
  launch APP                    open an app by bundle id or name
  apps                          list installed apps
  locked                        report whether the phone needs its passcode
`)
}

func run(ctx context.Context, cmd, udid string, args []string) error {
	p := pios.NewProvider()
	switch cmd {
	case "devices":
		return devices(ctx, p)
	case "rsd":
		id, err := pick(ctx, p, udid)
		if err != nil {
			return err
		}
		return rsd(ctx, id)
	}
	id, err := pick(ctx, p, udid)
	if err != nil {
		return err
	}
	start := time.Now()
	d, err := pios.Open(ctx, driver.Info{ID: id, Platform: driver.IOS})
	if err != nil {
		return err
	}
	defer d.Close()
	fmt.Fprintf(os.Stderr, "session open in %v\n", time.Since(start).Round(time.Millisecond))
	switch cmd {
	case "observe":
		return observe(ctx, d, args)
	case "tap":
		fs := flag.NewFlagSet("tap", flag.ContinueOnError)
		long := fs.Bool("long", false, "long press")
		pts, err := ints(fs, args, 2)
		if err != nil {
			return err
		}
		return d.Tap(ctx, screen.Point{X: pts[0], Y: pts[1]}, driver.TapOptions{LongPress: *long})
	case "swipe":
		fs := flag.NewFlagSet("swipe", flag.ContinueOnError)
		ms := fs.Int("ms", 300, "duration in milliseconds")
		pts, err := ints(fs, args, 4)
		if err != nil {
			return err
		}
		return d.Swipe(ctx, screen.Point{X: pts[0], Y: pts[1]}, screen.Point{X: pts[2], Y: pts[3]}, time.Duration(*ms)*time.Millisecond)
	case "key":
		if len(args) != 1 {
			return errors.New("key: one key name")
		}
		return d.Key(ctx, driver.Key(args[0]))
	case "type":
		fs := flag.NewFlagSet("type", flag.ContinueOnError)
		appendText := fs.Bool("append", false, "keep the field's text")
		submit := fs.Bool("submit", false, "press return afterwards")
		var text []string
		var flags []string
		for _, a := range args {
			if a == "-append" || a == "-submit" || a == "--append" || a == "--submit" {
				flags = append(flags, a)
			} else {
				text = append(text, a)
			}
		}
		if err := fs.Parse(flags); err != nil {
			return err
		}
		if len(text) != 1 {
			return errors.New("type: one text argument (quote text with spaces)")
		}
		return d.Type(ctx, text[0], driver.TypeOptions{Append: *appendText, Submit: *submit})
	case "stream":
		return stream(ctx, d, args)
	case "latency":
		return latency(ctx, d, args)
	case "screenshot":
		fs := flag.NewFlagSet("screenshot", flag.ContinueOnError)
		out := fs.String("out", "/tmp/pinky-shot.png", "output file")
		if err := fs.Parse(args); err != nil {
			return err
		}
		s := time.Now()
		b, err := d.ScreenshotPNG(ctx)
		if err != nil {
			return err
		}
		if err := os.WriteFile(*out, b, 0o600); err != nil {
			return err
		}
		fmt.Printf("%s: %d bytes in %v\n", *out, len(b), time.Since(s).Round(time.Millisecond))
		return nil
	case "launch":
		if len(args) != 1 {
			return errors.New("launch: one app")
		}
		s := time.Now()
		if err := d.Launch(ctx, args[0]); err != nil {
			return err
		}
		fmt.Printf("launched in %v\n", time.Since(s).Round(time.Millisecond))
		return nil
	case "apps":
		apps, err := d.Apps(ctx)
		if err != nil {
			return err
		}
		for _, a := range apps {
			kind := "user"
			if a.System {
				kind = "system"
			}
			fmt.Printf("%-40s %-6s %s\n", a.ID, kind, a.Name)
		}
		return nil
	case "locked":
		l, err := d.Locked(ctx)
		if err != nil {
			return err
		}
		fmt.Println("locked:", l)
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func pick(ctx context.Context, p *pios.Provider, udid string) (string, error) {
	if udid != "" {
		return udid, nil
	}
	list, err := p.List(ctx)
	if err != nil {
		return "", err
	}
	if len(list) != 1 {
		return "", fmt.Errorf("%d iPhones connected; pass -udid", len(list))
	}
	return list[0].ID, nil
}

// ints parses n numbers and the command's flags, in any order.
func ints(fs *flag.FlagSet, args []string, n int) ([]int, error) {
	var nums, flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || isNumber(a) {
			nums = append(nums, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if f := fs.Lookup(name); f != nil {
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
				continue
			}
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	if len(nums) != n {
		return nil, fmt.Errorf("%s: want %d numbers", fs.Name(), n)
	}
	out := make([]int, n)
	for i, s := range nums {
		v, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a number", fs.Name(), s)
		}
		out[i] = v
	}
	return out, nil
}

func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func devices(ctx context.Context, p *pios.Provider) error {
	list, err := p.List(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("no iPhone connected")
	}
	for _, d := range list {
		fmt.Printf("%s  %s · %s · iOS %s · %s · %s\n", d.ID, d.Name, d.Model, d.OSVersion, d.Connection, d.State)
		if d.Detail != "" {
			fmt.Printf("    %s\n", d.Detail)
		}
	}
	return nil
}

func rsd(ctx context.Context, id string) error {
	s := time.Now()
	svcs, info, err := pios.Services(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("tunnel and RSD handshake in %v; %s\n%d services:\n", time.Since(s).Round(time.Millisecond), info, len(svcs))
	for _, n := range svcs {
		fmt.Println(" ", n)
	}
	return nil
}

func observe(ctx context.Context, d *pios.Device, args []string) error {
	fs := flag.NewFlagSet("observe", flag.ContinueOnError)
	ocr := fs.Bool("ocr", false, "add OCR text the tree misses")
	n := fs.Int("n", 1, "observations")
	if err := fs.Parse(args); err != nil {
		return err
	}
	refs := screen.NewRefs()
	for i := 0; i < *n; i++ {
		s, st, err := d.ObserveWithStats(ctx, driver.ObserveOptions{OCR: *ocr})
		if err != nil {
			return err
		}
		refs.Assign(s)
		fmt.Println(s.Render())
		fmt.Fprintf(os.Stderr, "elements %d (placed by OCR %d), OCR lines %d, walk %v, screenshot %v, OCR %v, total %v, complete %v\n",
			st.Elements, st.Placed, st.OCRLines, st.Walk.Round(time.Millisecond), st.Shot.Round(time.Millisecond),
			st.OCR.Round(time.Millisecond), st.Total.Round(time.Millisecond), st.Complete)
		if st.WalkError != nil || st.OCRError != nil {
			fmt.Fprintf(os.Stderr, "walk error: %v; OCR error: %v\n", st.WalkError, st.OCRError)
		}
	}
	return nil
}

func stream(ctx context.Context, d *pios.Device, args []string) error {
	fs := flag.NewFlagSet("stream", flag.ContinueOnError)
	secs := fs.Int("seconds", 10, "how long to receive")
	out := fs.String("out", "", "write Annex B video here (keep it under /tmp/pinky-*)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := d.Stream(ctx, driver.StreamOptions{})
	if err != nil {
		return err
	}
	defer s.Close()
	var f *os.File
	if *out != "" {
		if f, err = os.Create(*out); err != nil {
			return err
		}
		defer f.Close()
	}
	w, h := s.Size()
	fmt.Printf("codec %s, %dx%d\n", s.Codec(), w, h)
	var frames, keys, configs int
	var bytes int64
	var first, last time.Duration
	sizes := []int{}
	start := time.Now()
	timer := time.NewTimer(time.Duration(*secs) * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
loop:
	for {
		select {
		case p, ok := <-s.Packets():
			if !ok {
				return fmt.Errorf("stream ended: %v", s.Err())
			}
			if f != nil {
				f.Write(p.Data)
			}
			if p.Config {
				configs++
				continue
			}
			if frames == 0 {
				first = p.PTS
			}
			last = p.PTS
			frames++
			bytes += int64(len(p.Data))
			sizes = append(sizes, len(p.Data))
			if p.KeyFrame {
				keys++
			}
		case <-tick.C:
			st, _ := d.StreamStats()
			fmt.Printf("  %5.1fs  frames %d  %.1f Mbit/s  lost packets %d  key requests %d\n",
				time.Since(start).Seconds(), frames, float64(bytes)*8/time.Since(start).Seconds()/1e6, st.Lost, st.KeyRequests)
		case <-timer.C:
			break loop
		case <-ctx.Done():
			break loop
		}
	}
	el := time.Since(start)
	sort.Ints(sizes)
	med := 0
	if len(sizes) > 0 {
		med = sizes[len(sizes)/2]
	}
	st, _ := d.StreamStats()
	fmt.Printf("%d frames in %v: %.1f fps by arrival, %.1f fps by timestamp; %d keyframes, %d config packets\n",
		frames, el.Round(time.Millisecond), float64(frames)/el.Seconds(), float64(frames-1)/(last-first).Seconds(), keys, configs)
	fmt.Printf("%.2f Mbit/s, median frame %d bytes; since the session started: %d packets lost, %d frames dropped, %d keyframe requests\n",
		float64(bytes)*8/el.Seconds()/1e6, med, st.Lost, st.Dropped, st.KeyRequests)
	if st.DelayKnown {
		fmt.Printf("one-way delay by RTCP sender report: %v (assumes synchronized clocks)\n", st.Delay.Round(time.Millisecond))
	}
	return nil
}

func latency(ctx context.Context, d *pios.Device, args []string) error {
	fs := flag.NewFlagSet("latency", flag.ContinueOnError)
	n := fs.Int("n", 6, "swipes")
	vertical := fs.Bool("vertical", false, "scroll the current screen up and down instead of paging the home screen")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*vertical {
		if err := d.Key(ctx, driver.KeyHome); err != nil {
			return err
		}
		time.Sleep(1500 * time.Millisecond)
	}
	lat, err := d.SwipeLatency(ctx, *n, *vertical)
	if err != nil {
		return err
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	for _, l := range lat {
		fmt.Printf("  %v\n", l.Round(time.Millisecond))
	}
	if len(lat) > 0 {
		fmt.Printf("touch-down to first changed frame: median %v (min %v, max %v)\n",
			lat[len(lat)/2].Round(time.Millisecond), lat[0].Round(time.Millisecond), lat[len(lat)-1].Round(time.Millisecond))
	}
	return nil
}
