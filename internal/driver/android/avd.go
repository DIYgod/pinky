package android

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
)

// Android Virtual Devices: the person starts the AVDs Android Studio made
// from Add phone (driver.VirtualLauncher), with the SDK's emulator, and a
// running one is a phone like any other, by its adb serial
// ("emulator-5554").

// avdStarting is how long an AVD Pinky started counts as starting before
// adb lists it.
const avdStarting = 3 * time.Minute

// avdLauncher finds the SDK's emulator and the AVDs, and starts and stops
// them. Tests replace its functions.
type avdLauncher struct {
	goos   string
	getenv func(string) string
	home   string
	sdks   func() []string
	// run runs the emulator (-list-avds) and returns its output.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// launch starts an AVD with the emulator, detached from Pinky, and
	// returns an error when it quits at once.
	launch func(emulator, avd string) error
	// kill asks a running emulator to quit, by its adb serial.
	kill func(ctx context.Context, serial string) error
	now  func() time.Time

	mu       sync.Mutex
	starting map[string]time.Time // AVDs Pinky started, by name
}

func newAVDLauncher() *avdLauncher {
	l := &avdLauncher{goos: runtime.GOOS, getenv: os.Getenv, sdks: adb.SDKs, run: runOutput,
		launch: launchEmulator, kill: killEmulator, now: time.Now}
	l.home, _ = os.UserHomeDir()
	return l
}

// avdTools is where the emulator is.
type avdTools struct {
	SDK      string // the Android SDK's folder; "" when there is none
	Emulator string // the emulator; "" when the SDK has none
}

// tools finds the first SDK that has the emulator, else the first SDK.
func (l *avdLauncher) tools() avdTools {
	name := "emulator"
	if l.goos == "windows" {
		name = "emulator.exe"
	}
	var first string
	for _, sdk := range l.sdks() {
		if fi, err := os.Stat(sdk); err != nil || !fi.IsDir() {
			continue
		}
		if first == "" {
			first = sdk
		}
		if p := filepath.Join(sdk, "emulator", name); isRegular(p) {
			return avdTools{SDK: sdk, Emulator: p}
		}
	}
	return avdTools{SDK: first}
}

func isRegular(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// avdHome is the folder of the AVDs, as the emulator finds it:
// ANDROID_AVD_HOME, else the avd folder in ANDROID_USER_HOME, in
// ANDROID_EMULATOR_HOME, in ANDROID_SDK_HOME's .android, else in
// ~/.android.
func (l *avdLauncher) avdHome() string {
	if v := l.getenv("ANDROID_AVD_HOME"); v != "" {
		return v
	}
	for _, k := range []string{"ANDROID_USER_HOME", "ANDROID_EMULATOR_HOME"} {
		if v := l.getenv(k); v != "" {
			return filepath.Join(v, "avd")
		}
	}
	if v := l.getenv("ANDROID_SDK_HOME"); v != "" {
		return filepath.Join(v, ".android", "avd")
	}
	return filepath.Join(l.home, ".android", "avd")
}

// avdNameRE is an AVD's name, as avdmanager allows it.
var avdNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// parseAVDList reads `emulator -list-avds`: one name a line, among the
// emulator's own INFO and WARNING lines.
func parseAVDList(out string) []string {
	var names []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if avdNameRE.MatchString(l) && !slices.Contains(names, l) {
			names = append(names, l)
		}
	}
	return names
}

// avd is what an AVD's files say of it.
type avd struct {
	Name    string // its name, the emulator's -avd
	Display string // the name Android Studio shows
	Model   string // the device it is made as: "Pixel 6"
	OS      string // the Android version: "16"
	SysDir  string // its system image, in the SDK: "system-images/android-36/google_apis/arm64-v8a/"
}

// readAVD reads <home>/<name>.ini and the config.ini in the folder it
// points at. Missing files leave fields empty.
func readAVD(home, name string) avd {
	a := avd{Name: name}
	ini := readINI(filepath.Join(home, name+".ini"))
	dir := ini["path"]
	if dir == "" || !isDir(dir) {
		if rel := ini["path.rel"]; rel != "" {
			dir = filepath.Join(filepath.Dir(home), filepath.FromSlash(rel))
		}
	}
	if dir == "" {
		dir = filepath.Join(home, name+".avd")
	}
	cfg := readINI(filepath.Join(dir, "config.ini"))
	a.Display = cfg["avd.ini.displayname"]
	if a.Display == "" {
		a.Display = strings.ReplaceAll(name, "_", " ")
	}
	a.Model = deviceModel(cfg["hw.device.name"])
	a.SysDir = cfg["image.sysdir.1"]
	target := cfg["target"]
	if target == "" {
		target = ini["target"]
	}
	if target == "" {
		// system-images/android-36/google_apis/arm64-v8a/
		if parts := strings.Split(filepath.ToSlash(a.SysDir), "/"); len(parts) > 1 {
			target = parts[1]
		}
	}
	a.OS = androidVersion(target)
	return a
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// readINI reads an AVD's key=value file; nil when it cannot.
func readINI(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseINI(string(b))
}

func parseINI(s string) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if k = strings.TrimSpace(k); !ok || k == "" || strings.HasPrefix(k, "#") {
			continue
		}
		m[k] = strings.TrimSpace(v)
	}
	return m
}

// deviceModel puts an AVD's hw.device.name into words: "pixel_9_pro" is
// "Pixel 9 Pro", "medium_phone" "Medium Phone"; a name with capitals
// ("Nexus 5X") stays.
func deviceModel(hw string) string {
	if hw == "" || strings.ToLower(hw) != hw {
		return hw
	}
	words := strings.FieldsFunc(hw, func(r rune) bool { return r == '_' || r == ' ' || r == '-' })
	for i, w := range words {
		switch w {
		case "xl", "tv", "xr":
			words[i] = strings.ToUpper(w)
		default:
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// androidVersions maps API levels to the Android versions people know.
var androidVersions = map[int]string{
	21: "5.0", 22: "5.1", 23: "6", 24: "7.0", 25: "7.1", 26: "8.0", 27: "8.1", 28: "9",
	29: "10", 30: "11", 31: "12", 32: "12L", 33: "13", 34: "14", 35: "15", 36: "16", 37: "17",
}

// androidVersion is the Android version of an AVD's target, "android-36"
// or "android-37.0"; a target past this table, or a preview's code name,
// stays as it is.
func androidVersion(target string) string {
	t, ok := strings.CutPrefix(target, "android-")
	if !ok || t == "" {
		return ""
	}
	major, _, _ := strings.Cut(t, ".")
	if n, err := strconv.Atoi(major); err == nil {
		if v := androidVersions[n]; v != "" {
			return v
		}
		return "API " + t
	}
	return t
}

// emulatorSerial reports whether an adb serial is an emulator's.
func emulatorSerial(serial string) bool { return strings.HasPrefix(serial, "emulator-") }

// avdSerials maps the running AVDs to their emulators' adb serials, from
// the property each emulator reads its AVD's name into (deviceProps). An
// AVD started twice, read-only, has the lowest serial.
func (p *Provider) avdSerials(ctx context.Context) (map[string]string, error) {
	entries, err := p.cfg.Client.Devices(ctx)
	if err != nil {
		return nil, wrapErr(err)
	}
	slices.SortFunc(entries, func(a, b adb.DeviceEntry) int { return strings.Compare(a.Serial, b.Serial) })
	out := map[string]string{}
	for _, e := range entries {
		if !emulatorSerial(e.Serial) || e.State != adb.StateDevice {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		pr, err := p.deviceProps(pctx, e)
		cancel()
		if err == nil && pr.AVD != "" && out[pr.AVD] == "" {
			out[pr.AVD] = e.Serial
		}
	}
	return out, nil
}

// avdList is what VirtualPhones finds, for StartVirtual too.
func (p *Provider) avdList(ctx context.Context) (driver.VirtualPhones, avdTools, error) {
	l := p.avd
	vp := driver.VirtualPhones{Platform: driver.Android}
	t := l.tools()
	vp.Where = t.SDK
	switch {
	case t.SDK == "":
		vp.Missing = "android_sdk"
		vp.Detail = "Android emulators need the Android SDK, which Android Studio installs; Pinky found none (ANDROID_HOME, ANDROID_SDK_ROOT, or where Android Studio puts it)."
		return vp, t, nil
	case t.Emulator == "":
		vp.Missing = "emulator"
		vp.Detail = "The Android SDK in " + t.SDK + " has no emulator; install it in Android Studio's SDK Manager (Android Emulator)."
		return vp, t, nil
	}
	lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	out, err := l.run(lctx, t.Emulator, "-list-avds")
	cancel()
	if err != nil {
		return vp, t, fmt.Errorf("list the Android virtual devices: %w", err)
	}
	running, err := p.avdSerials(ctx)
	if err != nil {
		// No adb server: none runs that Pinky can use.
		p.cfg.logf("android: running emulators: %v", err)
	}
	home := l.avdHome()
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, name := range parseAVDList(string(out)) {
		a := readAVD(home, name)
		v := driver.VirtualPhone{ID: name, Name: a.Display, Model: a.Model, OSVersion: a.OS}
		if s := running[name]; s != "" {
			v.Running, v.DeviceID = true, s
			delete(l.starting, name)
		} else if at, ok := l.starting[name]; ok {
			if now.Sub(at) < avdStarting {
				v.Starting = true
			} else {
				delete(l.starting, name)
			}
		}
		if a.SysDir != "" && !isDir(filepath.Join(t.SDK, filepath.FromSlash(a.SysDir))) {
			v.Unavailable = "its system image is not installed (" + strings.TrimSuffix(filepath.ToSlash(a.SysDir), "/") + ")"
		}
		vp.Phones = append(vp.Phones, v)
	}
	slices.SortStableFunc(vp.Phones, func(a, b driver.VirtualPhone) int {
		if a.Running != b.Running {
			if a.Running {
				return -1
			}
			return 1
		}
		return 0
	})
	return vp, t, nil
}

// VirtualPhones implements driver.VirtualLauncher: the AVDs the SDK's
// emulator lists, those that run first.
func (p *Provider) VirtualPhones(ctx context.Context) (driver.VirtualPhones, error) {
	vp, _, err := p.avdList(ctx)
	return vp, err
}

// StartVirtual implements driver.VirtualLauncher: it starts the AVD with
// the emulator, in a window of its own that stays when Pinky quits. One
// that runs, or that Pinky is starting, is left as it is.
func (p *Provider) StartVirtual(ctx context.Context, id string) error {
	vp, t, err := p.avdList(ctx)
	if err != nil {
		return err
	}
	if vp.Missing != "" {
		return errors.New(vp.Detail)
	}
	i := slices.IndexFunc(vp.Phones, func(v driver.VirtualPhone) bool { return v.ID == id })
	if i < 0 {
		return fmt.Errorf("Android virtual device %q: %w", id, driver.ErrNotFound)
	}
	v := vp.Phones[i]
	if v.Running || v.Starting {
		return nil
	}
	if v.Unavailable != "" {
		return fmt.Errorf("%s cannot start: %s", v.Name, v.Unavailable)
	}
	if err := p.avd.launch(t.Emulator, id); err != nil {
		return fmt.Errorf("start %s: %w", v.Name, err)
	}
	p.cfg.logf("android: started the emulator for %s", id)
	p.avd.mu.Lock()
	if p.avd.starting == nil {
		p.avd.starting = map[string]time.Time{}
	}
	p.avd.starting[id] = p.avd.now()
	p.avd.mu.Unlock()
	return nil
}

// StopVirtual implements driver.VirtualLauncher: it asks the AVD's
// emulator to quit, as closing its window does.
func (p *Provider) StopVirtual(ctx context.Context, id string) error {
	running, err := p.avdSerials(ctx)
	if err != nil {
		return err
	}
	serial := running[id]
	if serial == "" {
		return nil
	}
	p.avd.mu.Lock()
	delete(p.avd.starting, id)
	p.avd.mu.Unlock()
	return p.avd.kill(ctx, serial)
}

func runOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
	}
	return out, err
}

// launchEmulator starts `emulator -avd <name>` in a session of its own,
// with its output in a log file in the temporary folder, and waits a few
// seconds for it to quit at once, as when the AVD runs already or its
// system image is missing.
func launchEmulator(emulator, name string) error {
	logPath := filepath.Join(os.TempDir(), "pinky-emulator-"+name+".log")
	f, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command(emulator, "-avd", name)
	cmd.Dir = filepath.Dir(emulator)
	cmd.Stdout, cmd.Stderr = f, f
	detachEmulator(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }() // also reaps it when it quits later
	select {
	case err := <-exited:
		b, _ := os.ReadFile(logPath)
		msg := lastLine(string(b))
		if err == nil {
			err = errors.New("the emulator quit")
		}
		if msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return err
	case <-time.After(3 * time.Second):
		return nil
	}
}

// lastLine is the emulator's last error line, else its last line.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r", "")), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "ERROR") || strings.Contains(lines[i], "FATAL") {
			return strings.TrimSpace(lines[i])
		}
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

// killEmulator asks the emulator with the adb serial "emulator-<port>"
// to quit through its console on that port, as `adb emu kill` does.
func killEmulator(ctx context.Context, serial string) error {
	home, _ := os.UserHomeDir()
	return consoleKill(ctx, serial, filepath.Join(home, ".emulator_console_auth_token"))
}

// consoleKill speaks the emulator console's protocol: a banner ending in
// "OK", "auth <token>" when the banner asks for it (the token is in
// tokenFile), then "kill".
func consoleKill(ctx context.Context, serial, tokenFile string) error {
	port, ok := strings.CutPrefix(serial, "emulator-")
	if !ok {
		return fmt.Errorf("%s is not an emulator", serial)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return fmt.Errorf("the emulator's console: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(10 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	conn.SetDeadline(deadline)
	r := bufio.NewReader(conn)
	// reply reads up to the console's "OK" or "KO: ..." line.
	reply := func() (string, error) {
		var b strings.Builder
		for {
			line, err := r.ReadString('\n')
			b.WriteString(line)
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "OK") {
				return b.String(), nil
			}
			if strings.HasPrefix(line, "KO") {
				return b.String(), fmt.Errorf("the emulator's console: %s", line)
			}
			if err != nil {
				return b.String(), err
			}
		}
	}
	banner, err := reply()
	if err != nil {
		return fmt.Errorf("the emulator's console: %w", err)
	}
	if strings.Contains(banner, "Authentication required") {
		token, err := os.ReadFile(tokenFile)
		if err != nil {
			return fmt.Errorf("the emulator's console token: %w", err)
		}
		if _, err := fmt.Fprintf(conn, "auth %s\r\n", strings.TrimSpace(string(token))); err != nil {
			return err
		}
		if _, err := reply(); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(conn, "kill\r\n"); err != nil {
		return err
	}
	// It says "OK: killing emulator, bye bye" and hangs up; either will do.
	if _, err := reply(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
		return err
	}
	return nil
}
