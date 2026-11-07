package ios

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	goios "github.com/danielpaulus/go-ios/ios"
	"howett.net/plist"
)

// writeDiskImage makes a developer disk image folder as Xcode installs it:
// iOS_DDI/version.plist and Restore with a manifest that has a personalized
// identity and, when version is set, a cryptex one.
func writeDiskImage(t *testing.T, dir, build, version string, skip ...string) string {
	t.Helper()
	restore := filepath.Join(dir, "iOS_DDI", "Restore")
	if err := os.MkdirAll(filepath.Join(restore, "Firmware"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, v any) {
		for _, s := range skip {
			if s == name {
				return
			}
		}
		p := filepath.Join(restore, name)
		var b []byte
		switch v := v.(type) {
		case string:
			b = []byte(v)
		default:
			var err error
			if b, err = plist.Marshal(v, plist.XMLFormat); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("../version.plist", map[string]any{"ProductBuildVersion": build})
	entry := func(path string) map[string]any {
		return map[string]any{"Digest": []byte{1}, "Info": map[string]any{"Path": path, "Personalize": true}}
	}
	identities := []any{map[string]any{
		"ApBoardID": "0x0C", "ApChipID": "0x8010",
		"Manifest": map[string]any{
			"PersonalizedDMG":    entry("b.dmg"),
			"LoadableTrustCache": entry("Firmware/b.dmg.trustcache"),
		},
	}}
	write("b.dmg", "image")
	write("Firmware/b.dmg.trustcache", "trust")
	if version != "" {
		identities = append(identities, map[string]any{
			"Cryptex1,ChipID": "0xFF10", "Cryptex1,Type": "0x0",
			"Manifest": map[string]any{
				"Cryptex1,GenericDmg":        entry("a.dmg"),
				"Cryptex1,CryptexInfoPlist":  entry("Firmware/a.dmg.cryptex_info"),
				"Cryptex1,GenericTrustCache": entry("Firmware/a.dmg.trustcache"),
			},
		})
		write("a.dmg", "cryptex")
		write("Firmware/a.dmg.trustcache", "trust")
		write("Firmware/a.dmg.cryptex_info", map[string]any{"CFBundleIdentifier": ddiCryptex, "CFBundleVersion": version})
	}
	write("BuildManifest.plist", map[string]any{"BuildIdentities": identities})
	return filepath.Join(dir, "iOS_DDI")
}

func TestReadDiskImage(t *testing.T) {
	dir := writeDiskImage(t, t.TempDir(), "27B52", "27.2.52.0")
	img, err := readDiskImage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if img.Dir != filepath.Join(dir, "Restore") || img.Build != "27B52" || img.Version != "27.2.52.0" {
		t.Fatalf("readDiskImage = %+v", img)
	}
	// The Restore folder itself works too.
	if img2, err := readDiskImage(filepath.Join(dir, "Restore")); err != nil || img2 != img {
		t.Fatalf("readDiskImage(Restore) = %+v, %v", img2, err)
	}
	// An image without a cryptex identity (Xcode 15) has no version.
	old := writeDiskImage(t, t.TempDir(), "15F31d", "")
	if img, err := readDiskImage(old); err != nil || img.Version != "" || img.Build != "15F31d" {
		t.Fatalf("an Xcode 15 image = %+v, %v", img, err)
	}
	// A folder missing a file the manifest names is not used.
	half := writeDiskImage(t, t.TempDir(), "27B52", "27.2.52.0", "a.dmg")
	if _, err := readDiskImage(half); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("a half-copied image: %v", err)
	}
	if _, err := readDiskImage(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an empty folder: %v", err)
	}
}

func TestFindDiskImage(t *testing.T) {
	own := t.TempDir()
	writeDiskImage(t, own, "99A1", "99.0.1.0")
	t.Setenv(ddiEnv, "")
	img, err := findDiskImage(own)
	if err != nil {
		t.Fatal(err)
	}
	// Pinky's folder wins over Xcode's (on a Mac with Xcode) by being newer.
	if img.Source != sourcePinky || img.Version != "99.0.1.0" {
		t.Fatalf("findDiskImage = %+v", img)
	}
	// PINKY_DDI is the only folder looked in, and a wrong one is reported.
	env := writeDiskImage(t, t.TempDir(), "26A1", "26.0.1.0")
	t.Setenv(ddiEnv, env)
	if img, err := findDiskImage(own); err != nil || img.Source != sourceEnv || img.Version != "26.0.1.0" {
		t.Fatalf("with PINKY_DDI: %+v, %v", img, err)
	}
	t.Setenv(ddiEnv, filepath.Join(env, "nope"))
	if _, err := findDiskImage(own); err == nil || !strings.Contains(err.Error(), ddiEnv) {
		t.Fatalf("with a wrong PINKY_DDI: %v", err)
	}
}

func TestCompareVersionsAndBuilds(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"27.2.52.0", "27.2.52.0", 0},
		{"27.2.52.0", "27.10.1.0", -1},
		{"28.0", "27.9.9.9", 1},
		{"", "17.0", -1},
		{"17.0", "", 1},
		{"27.2", "27.2.0.0", 0},
	} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"27B52", "27B52", 0},
		{"27B52", "27A324", 1},
		{"17E5179g", "27B52", -1},
		{"27C10", "27B99", 1},
		{"27B100", "27B99", 1},
	} {
		if got := compareBuilds(c.a, c.b); got != c.want {
			t.Errorf("compareBuilds(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestClassifyMountError(t *testing.T) {
	for _, c := range []struct {
		err  error
		want mountProblem
	}{
		{errors.New("MountImage: mount command failed: MountImage: device responded with error: DeviceLocked"), problemLocked},
		{errors.New("MountImage: device responded with error: DeveloperModeIsNotEnabled"), problemDevMode},
		{errors.New(`MountImage: failed to get Cryptex1 ticket from Apple: requestTicket: failed to send request: Post "https://gs.apple.com/TSS/controller?action=2": dial tcp: lookup gs.apple.com: no such host`), problemNetwork},
		{errors.New(`MountImage: failed to get signature from Apple: unexpected status in response 94: "This device isn't eligible for the requested build."`), problemRefused},
		{errors.New("MountImage: findCryptexIdentity: no Cryptex1 identity for chip 0xff10"), problemNotFor},
		{errors.Join(errors.New("as a cryptex: x"), errNewerImageMounted), problemNewer},
		{errNoDiskImage, problemNoImage},
		{fmt.Errorf("mount: %w", errPhoneLocked), problemLocked},
		{errors.New("something else"), problemOther},
	} {
		if got := classifyMountError(c.err); got != c.want {
			t.Errorf("classifyMountError(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

// fakePrep is a preparer whose mounts the test runs and ends.
type fakePrep struct {
	mu      sync.Mutex
	now     time.Time
	mounts  int
	started chan struct{}
	results chan error
	done    chan string
}

func newFakePrep(t *testing.T, pr *preparer) *fakePrep {
	f := &fakePrep{now: time.Unix(1000, 0), started: make(chan struct{}, 10), results: make(chan error), done: make(chan string, 10)}
	pr.now = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.now
	}
	pr.findFn = func(string) (diskImage, error) {
		return diskImage{Dir: "/x", Build: "27B52", Version: "27.2.52.0"}, nil
	}
	pr.mountFn = func(context.Context, goios.DeviceEntry, diskImage) (mountResult, error) {
		f.mu.Lock()
		f.mounts++
		f.mu.Unlock()
		f.started <- struct{}{}
		select {
		case err := <-f.results:
			return mountResult{How: mountCryptex}, err
		case <-time.After(5 * time.Second):
			t.Error("the test never ended the mount")
			return mountResult{}, errors.New("test timeout")
		}
	}
	return f
}

func (f *fakePrep) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func (f *fakePrep) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mounts
}

func TestPreparerMounts(t *testing.T) {
	var pr preparer
	f := newFakePrep(t, &pr)
	e := entry("phone", 1, "USB")
	ok := func(context.Context, goios.DeviceEntry) (bool, error) { return true, nil }
	done := func() { f.done <- "done" }
	state := func() mountJob { return pr.mountState(e, "", ok, done) }

	if j := state(); !j.running {
		t.Fatalf("first look: %+v, want a mount running", j)
	}
	<-f.started
	if j := state(); !j.running || f.count() > 1 {
		t.Fatalf("while it runs: %+v, %d mounts; want the same one", j, f.count())
	}
	// Locked: tried again a few seconds later.
	f.results <- errors.New("MountImage: device responded with error: DeviceLocked")
	<-f.done
	if j := state(); j.running || classifyMountError(j.err) != problemLocked {
		t.Fatalf("after a locked phone: %+v", j)
	}
	f.advance(4 * time.Second)
	if j := state(); !j.running {
		t.Fatalf("4 s later: %+v; want a second try", j)
	}
	<-f.started
	if f.count() != 2 {
		t.Fatalf("4 s later: %d mounts; want a second try", f.count())
	}
	f.results <- nil
	<-f.done
	if j := state(); j.running || j.err != nil {
		t.Fatalf("after mounting: %+v", j)
	}
	f.advance(10 * time.Second)
	if j := state(); j.running || f.count() != 2 {
		t.Fatalf("10 s later: %+v, %d mounts; a phone just mounted is not mounted again", j, f.count())
	}
	// Missing services again after a while (another tool took the image
	// off): mounted again.
	f.advance(time.Minute)
	if j := state(); !j.running {
		t.Fatalf("a minute later: %+v; want a new mount", j)
	}
	<-f.started
	f.results <- nil
	<-f.done
	// The phone restarted: a new attach mounts again at once.
	e = entry("phone", 2, "USB")
	if j := state(); !j.running {
		t.Fatalf("after a restart: %+v", j)
	}
	<-f.started
	if f.count() != 4 {
		t.Fatalf("after a restart: %d mounts, want 4", f.count())
	}
	f.results <- nil
	<-f.done
}

func TestPreparerServicesMissing(t *testing.T) {
	defer func(d time.Duration) { serviceWait = d }(serviceWait)
	serviceWait = 200 * time.Millisecond
	var pr preparer
	f := newFakePrep(t, &pr)
	e := entry("phone", 1, "USB")
	missing := func(context.Context, goios.DeviceEntry) (bool, error) { return false, nil }
	pr.mountState(e, "", missing, func() { f.done <- "done" })
	<-f.started
	f.results <- nil
	select {
	case <-f.done:
	case <-time.After(15 * time.Second):
		t.Fatal("the mount did not end")
	}
	j := pr.mountState(e, "", missing, nil)
	if j.running || !errors.Is(j.err, errServicesMissing) {
		t.Fatalf("services missing after a mount: %+v", j)
	}
	info := diskImageState(driver.Info{OSVersion: "28.0"}, j, "")
	if info.Setup != driver.SetupDiskImage || !strings.Contains(info.Detail, "too old for iOS 28.0") {
		t.Fatalf("an image older than the phone: %+v", info)
	}
	info = diskImageState(driver.Info{OSVersion: "27.0.1"}, j, "")
	if !strings.Contains(info.Detail, "Restart the iPhone") {
		t.Fatalf("services missing with a current image: %+v", info)
	}
}

func TestDiskImageState(t *testing.T) {
	img := diskImage{Build: "27B52", Version: "27.2.52.0"}
	for _, c := range []struct {
		job   mountJob
		setup string
		says  string
	}{
		{mountJob{running: true}, driver.SetupPreparing, "preparing"},
		{mountJob{img: img, err: errors.New("device responded with error: DeviceLocked")}, driver.SetupUnlock, "Unlock the iPhone"},
		{mountJob{err: errNoDiskImage}, driver.SetupNoDiskImage, "comes with Xcode"},
		{mountJob{img: img, err: errors.New("requestTicket: failed to send request: dial tcp: i/o timeout")}, driver.SetupDiskImage, "gs.apple.com"},
		{mountJob{img: img, err: errors.New(`unexpected status in response 94: "This device isn't eligible"`)}, driver.SetupDiskImage, "would not sign"},
		{mountJob{img: img, err: errors.New("findCryptexIdentity: no Cryptex1 identity")}, driver.SetupDiskImage, "Xcode 27B52"},
		{mountJob{img: img, err: errors.New("as a cryptex: boom\nas a personalized image: bang")}, driver.SetupDiskImage, "could not mount the developer disk image: as a personalized image: bang"},
	} {
		info := diskImageState(driver.Info{OSVersion: "27.0.1"}, c.job, "/data/DeveloperDiskImages")
		if info.State != driver.StateNeedsSetup || info.Setup != c.setup || !strings.Contains(info.Detail, c.says) {
			t.Errorf("job %+v: %+v, want %s saying %q", c.job, info, c.setup, c.says)
		}
	}
}

func TestDiskImageMissingForgetsWhenDone(t *testing.T) {
	p := NewProvider()
	f := newFakePrep(t, &p.prep)
	e := entry("phone", 1, "USB")
	p.known["phone"] = known{key: keyOf(e), info: driver.Info{ID: "phone", State: driver.StateNeedsSetup, Setup: driver.SetupPreparing}}
	p.ddi["phone"] = ddiCheck{}
	p.ddi["phone|display"] = ddiCheck{}
	p.ddi["phone2"] = ddiCheck{}
	ok := func(context.Context, goios.DeviceEntry) (bool, error) { return true, nil }
	info := p.diskImageMissing(e, driver.Info{ID: "phone"}, ok)
	if info.Setup != driver.SetupPreparing {
		t.Fatalf("diskImageMissing = %+v", info)
	}
	<-f.started
	f.results <- nil
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		_, k := p.known["phone"]
		_, d1 := p.ddi["phone"]
		_, d2 := p.ddi["phone|display"]
		_, other := p.ddi["phone2"]
		p.mu.Unlock()
		if !k && !d1 && !d2 {
			if !other {
				t.Fatal("another phone's check was forgotten too")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the provider still remembers the phone after its mount ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRevealDevModeOncePerAttach(t *testing.T) {
	p := NewProvider()
	var mu sync.Mutex
	reveals := 0
	p.prep.revealFn = func(goios.DeviceEntry) error {
		mu.Lock()
		reveals++
		mu.Unlock()
		return nil
	}
	count := func() int {
		time.Sleep(20 * time.Millisecond) // reveal runs in the background
		mu.Lock()
		defer mu.Unlock()
		return reveals
	}
	e := entry("phone", 1, "USB")
	info := p.devModeOff(e, driver.Info{})
	if info.State != driver.StateNeedsSetup || info.Setup != driver.SetupDeveloperMode || !strings.Contains(info.Detail, "Privacy & Security > Developer Mode") {
		t.Fatalf("devModeOff = %+v", info)
	}
	p.devModeOff(e, driver.Info{})
	if n := count(); n != 1 {
		t.Fatalf("%d reveals for one attach, want 1", n)
	}
	p.devModeOff(entry("phone", 2, "USB"), driver.Info{})
	if n := count(); n != 2 {
		t.Fatalf("%d reveals after a reconnect, want 2", n)
	}
	// A reveal that failed is sent again at the next look.
	p.prep.revealFn = func(goios.DeviceEntry) error {
		mu.Lock()
		reveals++
		mu.Unlock()
		return errors.New("amfi: connection refused")
	}
	e3 := entry("phone", 3, "USB")
	p.devModeOff(e3, driver.Info{})
	count()
	p.devModeOff(e3, driver.Info{})
	if n := count(); n != 4 {
		t.Fatalf("%d reveals after a failed one, want 4", n)
	}
}

func TestAskTrustThrottles(t *testing.T) {
	var pr preparer
	now := time.Unix(1000, 0)
	pr.now = func() time.Time { return now }
	asks := 0
	pr.pairFn = func(goios.DeviceEntry) error {
		asks++
		return errors.New("Please accept the PairingDialog on the device and run pairing again!")
	}
	e := entry("phone", 1, "USB")
	pr.askTrust(e)
	pr.askTrust(e)
	if asks != 1 {
		t.Fatalf("%d pairing requests at once, want 1", asks)
	}
	now = now.Add(5 * time.Second)
	pr.askTrust(e)
	if asks != 2 {
		t.Fatalf("%d pairing requests 5 s later, want 2", asks)
	}
}

func TestTrustState(t *testing.T) {
	info := trustState(driver.Info{}, errors.New("lockdown: InvalidHostID"))
	if info.State != driver.StateUnauthorized || info.Setup != driver.SetupTrust || !strings.Contains(info.Detail, "tap Trust") {
		t.Fatalf("trustState = %+v", info)
	}
	if info := trustState(driver.Info{}, errors.New("Lockdown error: UserDeniedPairing")); !strings.Contains(info.Detail, "plug it in again") {
		t.Fatalf("after Don't Trust: %+v", info)
	}
}

func TestRecheckFor(t *testing.T) {
	for _, c := range []struct {
		info driver.Info
		want time.Duration
	}{
		{driver.Info{State: driver.StateUnauthorized}, 2 * time.Second},
		{driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupPreparing}, 3 * time.Second},
		{driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupUnlock}, 3 * time.Second},
		{driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDeveloperMode}, recheckOffline},
		{driver.Info{State: driver.StateOffline}, recheckOffline},
	} {
		if got := recheckFor(c.info); got != c.want {
			t.Errorf("recheckFor(%+v) = %v, want %v", c.info, got, c.want)
		}
	}
}

func TestHostCheck(t *testing.T) {
	err := errors.New("dial unix /var/run/usbmuxd: connect: no such file or directory")
	for _, c := range []struct {
		goos      string
		installed bool
		want      string
	}{
		{"windows", false, "apple_devices"},
		{"linux", false, "usbmuxd"},
		{"linux", true, "usbmuxd_stopped"},
		{"darwin", false, "usbmuxd"},
	} {
		if got := hostCheck(c.goos, c.installed, err); got.Missing != c.want || got.Detail == "" {
			t.Errorf("hostCheck(%s, %v) = %+v, want %s", c.goos, c.installed, got, c.want)
		}
	}
}

func TestListWithoutUsbmuxd(t *testing.T) {
	p := NewProvider()
	p.inspectFn = func(_ context.Context, e goios.DeviceEntry) driver.Info {
		return driver.Info{ID: e.Properties.SerialNumber, State: driver.StateOnline}
	}
	down := errors.New("dial unix /var/run/usbmuxd: connect: no such file or directory")
	var answer error
	phones := []goios.DeviceEntry{entry("phone", 1, "USB")}
	p.listFn = func() (goios.DeviceList, error) {
		if answer != nil {
			return goios.DeviceList{}, answer
		}
		return goios.DeviceList{DeviceList: phones}, nil
	}
	ctx := context.Background()
	// Never answered: no phones and no error, and CheckHost says why.
	answer = down
	if got, err := p.List(ctx); err != nil || len(got) != 0 {
		t.Fatalf("without usbmuxd: %v, %v", got, err)
	}
	if c := p.CheckHost(); c.Missing == "" {
		t.Fatalf("CheckHost without usbmuxd = %+v", c)
	}
	// It answered with a phone; a hiccup is an error twice (the gateway
	// keeps the phone), then the phone is gone.
	answer = nil
	if got, err := p.List(ctx); err != nil || len(got) != 1 {
		t.Fatalf("with usbmuxd: %v, %v", got, err)
	}
	if c := p.CheckHost(); c.Missing != "" {
		t.Fatalf("CheckHost with usbmuxd = %+v", c)
	}
	answer = down
	for i := 0; i < 2; i++ {
		if _, err := p.List(ctx); err == nil {
			t.Fatalf("failure %d after phones: no error", i+1)
		}
	}
	if got, err := p.List(ctx); err != nil || len(got) != 0 {
		t.Fatalf("third failure: %v, %v; want no phones", got, err)
	}
}

func TestMountWay(t *testing.T) {
	cryptex := diskImage{Version: "27.2.52.0"}
	old := diskImage{} // Xcode 15: no cryptex
	for _, c := range []struct {
		major    int
		img      diskImage
		cryptexd bool
		want     string
		err      error
	}{
		{27, cryptex, true, mountCryptex, nil},
		{28, cryptex, true, mountCryptex, nil},
		{27, old, true, "", errNoCryptex},
		{27, cryptex, false, "", nil}, // an error, not the other way
		{26, cryptex, true, mountPersonalized, nil},
		{18, old, false, mountPersonalized, nil},
	} {
		got, err := mountWay(c.major, c.img, c.cryptexd)
		if got != c.want || (c.err != nil && !errors.Is(err, c.err)) || (c.want == "" && err == nil) {
			t.Errorf("mountWay(%d, %+v, %v) = %q, %v; want %q", c.major, c.img, c.cryptexd, got, err, c.want)
		}
	}
	if classifyMountError(errNoCryptex) != problemNotFor {
		t.Error("an image without a cryptex should read as not for this iPhone")
	}
}
