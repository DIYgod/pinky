// Command companionctl drives the Android companion app by hand: install
// it, turn its accessibility service on and off, and read trees, set text
// and watch events. Run it through scripts/devlock.sh on a shared phone.
//
//	go run ./internal/driver/android/companion/cmd/companionctl -s SERIAL <command> [args]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver/android/companion"
	"github.com/DIYgod/pinky/internal/screen"
)

const usage = `usage: companionctl -s SERIAL [-keep] <command> [args]

commands:
  install [-force] APK adb install -r the companion APK, unless the phone
                       already has this exact file
  installed            print the installed version
  enable               turn the accessibility service on and leave it on
  disable              remove Pinky's entry from the setting
  status               show whether the service is listed and answering
  ping                 print the ping result
  tree [-raw] [-ime]   print the screen (or the raw JSON tree)
  settext [-append] [-submit] [-id ID] [-x X -y Y] TEXT
                       write TEXT into the focused (or chosen) field
  events [KIND...]     print events until interrupted
  idle [MAX_MS]        wait for the screen to settle
  bench [N]            time N companion trees against N uiautomator dumps

Commands from ping on turn the service on for their run and restore
enabled_accessibility_services afterwards; -keep leaves it on. "enable"
leaves it on; "disable" removes Pinky's entry whoever added it.`

func main() {
	serial := flag.String("s", os.Getenv("PINKY_DEVICE"), "device serial (default $PINKY_DEVICE)")
	keep := flag.Bool("keep", false, "leave the service on after the command")
	flag.Usage = func() { fmt.Fprintln(os.Stderr, usage) }
	flag.Parse()
	if *serial == "" || flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	run := &companion.ExecRunner{Serial: *serial}
	c := companion.New(run)
	defer c.Close()
	if err := dispatch(ctx, c, run, flag.Arg(0), flag.Args()[1:], *keep); err != nil {
		fmt.Fprintln(os.Stderr, "companionctl:", err)
		os.Exit(1)
	}
}

func dispatch(ctx context.Context, c *companion.Client, run *companion.ExecRunner, cmd string, args []string, keep bool) error {
	switch cmd {
	case "install":
		fs := flag.NewFlagSet("install", flag.ContinueOnError)
		force := fs.Bool("force", false, "install even if the phone has this exact APK")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("install needs the APK path")
		}
		if *force {
			return c.Install(ctx, fs.Arg(0))
		}
		did, err := c.InstallIfChanged(ctx, fs.Arg(0))
		if err == nil && !did {
			fmt.Println("already installed")
		}
		return err
	case "installed":
		v, ok, err := c.Installed(ctx)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("not installed")
			return nil
		}
		fmt.Println(v)
		return nil
	case "enable":
		// Leaves the service on; "disable" turns it off again.
		return c.Enable(ctx)
	case "disable":
		return c.ForceDisable(ctx)
	case "status":
		on, err := c.Enabled(ctx)
		if err != nil {
			return err
		}
		fmt.Println("listed:", on)
		info, err := c.Ping(ctx)
		if err != nil {
			fmt.Println("answering: no:", err)
			return nil
		}
		fmt.Printf("answering: yes, version %s, Android %s (SDK %d), %s %s\n",
			info.Version, info.Release, info.SDK, info.Manufacturer, info.Model)
		return nil
	}

	switch cmd {
	case "ping", "tree", "settext", "events", "idle", "bench":
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	// The rest need the service: turn it on for the command and restore the
	// setting afterwards, unless -keep.
	if err := c.Enable(ctx); err != nil {
		return err
	}
	if !keep {
		defer func() {
			dctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := c.Disable(dctx); err != nil {
				fmt.Fprintln(os.Stderr, "companionctl: disable:", err)
			}
		}()
	}
	switch cmd {
	case "ping":
		info, err := c.Ping(ctx)
		if err != nil {
			return err
		}
		return printJSON(info)
	case "tree":
		return tree(ctx, c, args)
	case "settext":
		return setText(ctx, c, args)
	case "events":
		return events(ctx, c, args)
	case "idle":
		max := 3 * time.Second
		if len(args) > 0 {
			ms, err := strconv.Atoi(args[0])
			if err != nil {
				return err
			}
			max = time.Duration(ms) * time.Millisecond
		}
		start := time.Now()
		settled, err := c.WaitSettled(ctx, max)
		if err != nil {
			return err
		}
		fmt.Printf("settled=%v after %s\n", settled, time.Since(start).Round(time.Millisecond))
		return nil
	case "bench":
		n := 10
		if len(args) > 0 {
			v, err := strconv.Atoi(args[0])
			if err != nil {
				return err
			}
			n = v
		}
		return bench(ctx, c, run, n)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func tree(ctx context.Context, c *companion.Client, args []string) error {
	fs := flag.NewFlagSet("tree", flag.ContinueOnError)
	raw := fs.Bool("raw", false, "print the raw JSON tree")
	ime := fs.Bool("ime", false, "include the keyboard")
	bars := fs.Bool("bars", false, "include the status and navigation bars")
	if err := fs.Parse(args); err != nil {
		return err
	}
	start := time.Now()
	t, err := c.Dump(ctx, companion.DumpOptions{IME: *ime, Bars: *bars})
	if err != nil {
		return err
	}
	took := time.Since(start)
	if *raw {
		return printJSON(t)
	}
	s := t.Screen(companion.ScreenOptions{Keyboard: *ime, SystemBars: *bars})
	screen.NewRefs().Assign(s)
	fmt.Println(s.Render())
	fmt.Fprintf(os.Stderr, "%d nodes, %d elements, phone %d ms, round trip %s\n",
		t.Nodes, len(s.Elements), t.TookMS, took.Round(time.Millisecond))
	return nil
}

func setText(ctx context.Context, c *companion.Client, args []string) error {
	fs := flag.NewFlagSet("settext", flag.ContinueOnError)
	appendText := fs.Bool("append", false, "keep the current text")
	submit := fs.Bool("submit", false, "run the IME action afterwards")
	id := fs.String("id", "", "resource id of the field")
	x := fs.Int("x", -1, "x of a point inside the field")
	y := fs.Int("y", -1, "y of a point inside the field")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("settext needs exactly one TEXT argument")
	}
	var t companion.Target
	if *x >= 0 && *y >= 0 {
		t.Point = &screen.Point{X: *x, Y: *y}
	}
	t.ID = *id
	r, err := c.SetText(ctx, t, fs.Arg(0), companion.SetTextOptions{Append: *appendText, Submit: *submit})
	if err != nil {
		return err
	}
	return printJSON(r)
}

func events(ctx context.Context, c *companion.Client, args []string) error {
	if _, err := c.Ping(ctx); err != nil { // measures the clock skew
		return err
	}
	var kinds []companion.EventKind
	for _, a := range args {
		kinds = append(kinds, companion.EventKind(a))
	}
	sub, err := c.Events(ctx, kinds...)
	if err != nil {
		return err
	}
	for ev := range sub.C() {
		b, _ := json.Marshal(ev)
		fmt.Printf("%s %s\n", ev.Time.Format("15:04:05.000"), b)
	}
	return sub.Err()
}

// bench times companion trees, then uiautomator dumps, of the screen in
// front. Dumps come last because each one makes Android unbind every other
// accessibility service while it runs. Samples taken while another app (or
// the lock screen after a screen timeout) was in front are discarded, and a
// screen that went dark is woken with KEYCODE_WAKEUP; nothing else is
// touched.
func bench(ctx context.Context, c *companion.Client, run *companion.ExecRunner, n int) error {
	var tree, phone, dump []time.Duration
	var nodes, elements, dumpNodes int
	target := ""
	for tries := 0; len(tree) < n; tries++ {
		if tries > 3*n {
			return errors.New("bench: the screen kept changing")
		}
		if err := ensureAwake(ctx, run); err != nil {
			return err
		}
		start := time.Now()
		t, err := c.Dump(ctx, companion.DumpOptions{})
		if err != nil {
			return err
		}
		d := time.Since(start)
		s := t.Screen(companion.ScreenOptions{})
		if s.Locked {
			return errors.New("bench: the phone is locked; leaving it alone")
		}
		if target == "" {
			target = s.App.ID
		}
		if s.App.ID != target {
			continue
		}
		tree = append(tree, d)
		phone = append(phone, time.Duration(t.TookMS)*time.Millisecond)
		nodes, elements = t.Nodes, len(s.Elements)
		time.Sleep(300 * time.Millisecond)
	}
	for tries := 0; len(dump) < n; tries++ {
		if tries > 3*n {
			return errors.New("bench: the screen kept changing")
		}
		if err := ensureAwake(ctx, run); err != nil {
			return err
		}
		start := time.Now()
		out, err := uiautomatorDump(ctx, run)
		if err != nil {
			return err
		}
		d := time.Since(start)
		if !strings.Contains(out, `package="`+target+`"`) {
			continue
		}
		dump = append(dump, d)
		dumpNodes = strings.Count(out, "<node ")
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Printf("app: %s\n", target)
	fmt.Printf("companion tree:    %s; phone side %s; %d nodes, %d elements\n", stats(tree), stats(phone), nodes, elements)
	fmt.Printf("uiautomator dump:  %s; %d nodes\n", stats(dump), dumpNodes)
	return nil
}

// ensureAwake wakes a screen that timed out. KEYCODE_WAKEUP does nothing
// on a screen that is already on.
func ensureAwake(ctx context.Context, run *companion.ExecRunner) error {
	out, err := run.Shell(ctx, "dumpsys power | grep -m1 mWakefulness=")
	if err != nil {
		return err
	}
	if strings.Contains(out, "Awake") {
		return nil
	}
	if _, err := run.Shell(ctx, "input keyevent KEYCODE_WAKEUP"); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	return nil
}

// uiautomatorDump runs `uiautomator dump` and reads the XML back, the way a
// driver without the companion has to.
func uiautomatorDump(ctx context.Context, run *companion.ExecRunner) (string, error) {
	const path = "/data/local/tmp/pinky-window-dump.xml"
	out, err := run.Shell(ctx, "uiautomator dump "+path+" >/dev/null && cat "+path+" && rm "+path)
	if err != nil {
		return "", err
	}
	if !strings.Contains(out, "<hierarchy") {
		return "", fmt.Errorf("uiautomator dump: %s", strings.TrimSpace(out))
	}
	return out, nil
}

func stats(ds []time.Duration) string {
	if len(ds) == 0 {
		return "-"
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	var sum time.Duration
	for _, d := range s {
		sum += d
	}
	ms := func(d time.Duration) string { return strconv.FormatFloat(float64(d)/1e6, 'f', 0, 64) }
	return fmt.Sprintf("median %s ms, mean %s, min %s, max %s (n=%d)",
		ms(s[len(s)/2]), ms(sum/time.Duration(len(s))), ms(s[0]), ms(s[len(s)-1]), len(s))
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
