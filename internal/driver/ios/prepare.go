package ios

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/amfi"
)

// Preparing a phone for Pinky: the steps Xcode used to take. Pinky shows
// the Developer Mode switch in Settings while Developer Mode is off, mounts
// the developer disk image when the developer services are missing (after
// every restart, since the phone drops the image when it restarts), and on
// Windows asks the phone to trust this computer. Mounting runs in the
// background, so List says where a phone stands without waiting for it.

// devModeOff is a phone with Developer Mode off: Pinky shows the switch in
// Settings and says how to turn it on.
func (p *Provider) devModeOff(e goios.DeviceEntry, info driver.Info) driver.Info {
	p.prep.revealDevMode(e)
	info.State = driver.StateNeedsSetup
	info.Setup = driver.SetupDeveloperMode
	info.Detail = devModeDetail
	return info
}

// diskImageMissing is a phone without the developer services check looks
// for: Pinky mounts the developer disk image in the background and says how
// that goes. When a mount ends, List looks at the phone again at once.
func (p *Provider) diskImageMissing(e goios.DeviceEntry, info driver.Info, check func(context.Context, goios.DeviceEntry) (bool, error)) driver.Info {
	id := e.Properties.SerialNumber
	j := p.prep.mountState(e, p.DiskImageDir, check, func() { p.forgetPhone(id) })
	return diskImageState(info, j, p.DiskImageDir)
}

// forgetPhone drops what List knows about a phone and its services, so the
// next List inspects it again.
func (p *Provider) forgetPhone(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.known, id)
	for k := range p.ddi {
		if k == id || strings.HasPrefix(k, id+"|") {
			delete(p.ddi, k)
		}
	}
}

// recheckFor is how long List keeps what it found about a phone that is not
// online before asking it again: briefly while the person is doing a step
// on the phone (tapping Trust, unlocking) or Pinky is preparing it, so the
// next step shows soon after.
func recheckFor(info driver.Info) time.Duration {
	switch {
	case info.State == driver.StateUnauthorized:
		return 2 * time.Second
	case info.Setup == driver.SetupPreparing, info.Setup == driver.SetupUnlock:
		return 3 * time.Second
	}
	return recheckOffline
}

// preparer holds the provider's preparation jobs. Its zero value is ready.
type preparer struct {
	mu       sync.Mutex
	jobs     map[string]*mountJob // by UDID
	revealed map[string]entryKey  // the attach the Developer Mode switch was shown for
	asked    map[string]time.Time // the last pairing request (Windows), by UDID

	// For tests.
	findFn   func(ownDir string) (diskImage, error)
	mountFn  func(ctx context.Context, e goios.DeviceEntry, img diskImage) (mountResult, error)
	revealFn func(e goios.DeviceEntry) error
	pairFn   func(e goios.DeviceEntry) error
	now      func() time.Time
}

// mountJob is one phone's mount, running or done.
type mountJob struct {
	key     entryKey
	running bool
	ended   time.Time
	img     diskImage
	res     mountResult
	err     error
}

// errServicesMissing means the image mounted but the phone still does not
// offer the services Pinky checks for.
var errServicesMissing = errors.New("the developer disk image is mounted, but the iPhone does not offer the developer services Pinky needs")

func (pr *preparer) clock() time.Time {
	if pr.now != nil {
		return pr.now()
	}
	return time.Now()
}

// mountState returns the phone's mount job, starting one when none ran for
// this attach or when the last one failed long enough ago. check tells
// whether the services Pinky needs are there; done runs when a job ends.
func (pr *preparer) mountState(e goios.DeviceEntry, ownDir string, check func(context.Context, goios.DeviceEntry) (bool, error), done func()) mountJob {
	id, key := e.Properties.SerialNumber, keyOf(e)
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if pr.jobs == nil {
		pr.jobs = map[string]*mountJob{}
	}
	j := pr.jobs[id]
	if j != nil && j.key == key && (j.running || pr.clock().Sub(j.ended) < retryAfter(j.err)) {
		return *j
	}
	j = &mountJob{key: key, running: true}
	pr.jobs[id] = j
	go pr.run(j, e, ownDir, check, done)
	return *j
}

func (pr *preparer) run(j *mountJob, e goios.DeviceEntry, ownDir string, check func(context.Context, goios.DeviceEntry) (bool, error), done func()) {
	find, mount := pr.findFn, pr.mountFn
	if find == nil {
		find = findDiskImage
	}
	if mount == nil {
		mount = mountOn
	}
	ctx, cancel := context.WithTimeout(context.Background(), mountTimeout+time.Minute)
	defer cancel()
	img, err := find(ownDir)
	var res mountResult
	if err == nil {
		res, err = mount(ctx, e, img)
	}
	if err == nil {
		err = waitForServices(ctx, e, check)
	}
	pr.mu.Lock()
	j.running, j.ended, j.img, j.res, j.err = false, pr.clock(), img, res, err
	pr.mu.Unlock()
	if done != nil {
		done()
	}
}

// mountOn mounts img on the phone behind e through a tunnel of its own,
// once the phone is unlocked.
func mountOn(ctx context.Context, e goios.DeviceEntry, img diskImage) (mountResult, error) {
	if locked, err := lockedEntry(ctx, e); err == nil && locked {
		return mountResult{}, errPhoneLocked
	}
	l, err := dialLinkEntry(ctx, e)
	if err != nil {
		return mountResult{}, err
	}
	defer l.Close()
	return mountDiskImage(ctx, l, img)
}

// serviceWait is how long waitForServices waits.
var serviceWait = 10 * time.Second

// waitForServices waits up to serviceWait for the phone to publish what
// check looks for: the services come up a moment after the image mounts.
func waitForServices(ctx context.Context, e goios.DeviceEntry, check func(context.Context, goios.DeviceEntry) (bool, error)) error {
	if check == nil {
		return nil
	}
	deadline := time.Now().Add(serviceWait)
	for {
		ok, err := check(ctx, e)
		if err == nil && ok {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			if err != nil {
				return fmt.Errorf("%w: %v", errServicesMissing, err)
			}
			return errServicesMissing
		}
		select {
		case <-ctx.Done():
		case <-time.After(serviceWait / 10):
		}
	}
}

// retryAfter is how long a mount waits before the next try, by what went
// wrong: soon for what the person fixes in a moment (unlocking), later for
// what needs more (the network, installing Xcode), and 10 minutes or until
// the phone reconnects for what trying again cannot fix. A mount that
// worked is tried again only if the services go missing again (another tool
// took the image off).
func retryAfter(err error) time.Duration {
	if err == nil {
		return 30 * time.Second
	}
	switch {
	case errors.Is(err, errServicesMissing):
		return 10 * time.Minute
	}
	switch classifyMountError(err) {
	case problemLocked, problemDevMode:
		return 3 * time.Second
	case problemNoImage:
		return 10 * time.Second
	case problemNetwork:
		return 20 * time.Second
	case problemNewer, problemNotFor, problemRefused:
		return 10 * time.Minute
	}
	return 30 * time.Second
}

// forget drops a phone's job, so the next mountState starts a new one.
func (pr *preparer) forget(id string) {
	pr.mu.Lock()
	delete(pr.jobs, id)
	pr.mu.Unlock()
}

// revealDevMode shows the Developer Mode switch in the phone's Settings ›
// Privacy & Security, as Xcode does: iOS hides it until a developer tool
// has seen the phone. Once per attach, in the background.
func (pr *preparer) revealDevMode(e goios.DeviceEntry) {
	id, key := e.Properties.SerialNumber, keyOf(e)
	pr.mu.Lock()
	if pr.revealed == nil {
		pr.revealed = map[string]entryKey{}
	}
	if k, ok := pr.revealed[id]; ok && k == key {
		pr.mu.Unlock()
		return
	}
	pr.revealed[id] = key
	reveal := pr.revealFn
	pr.mu.Unlock()
	if reveal == nil {
		reveal = revealDevMode
	}
	go func() {
		// The person's step is the same either way; a failed reveal is
		// sent again at the next look.
		if err := reveal(e); err != nil {
			pr.mu.Lock()
			if pr.revealed[id] == key {
				delete(pr.revealed, id)
			}
			pr.mu.Unlock()
		}
	}()
}

func revealDevMode(e goios.DeviceEntry) error {
	c, err := amfi.New(e)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.RevealDevMode()
}

// askTrust makes a phone that has never trusted this computer show "Trust
// This Computer?", on Windows only: macOS and Linux's usbmuxd ask by
// themselves when a phone is plugged in, and a second pairing there could
// race theirs. At most every 4 s while the phone waits; a pairing that the
// person accepted saves its record through usbmuxd, as Apple's tools do.
func (pr *preparer) askTrust(e goios.DeviceEntry) error {
	pair := pr.pairFn
	if pair == nil {
		if runtime.GOOS != "windows" {
			return nil
		}
		if _, err := goios.ReadPairRecord(e.Properties.SerialNumber); err == nil {
			return nil // paired; lockdown says why it still refuses
		}
		pair = goios.Pair
	}
	id := e.Properties.SerialNumber
	pr.mu.Lock()
	if pr.asked == nil {
		pr.asked = map[string]time.Time{}
	}
	if t, ok := pr.asked[id]; ok && pr.clock().Sub(t) < 4*time.Second {
		pr.mu.Unlock()
		return nil
	}
	pr.asked[id] = pr.clock()
	pr.mu.Unlock()
	return pair(e)
}

// Sentences for the steps, in driver.Info.Detail.
const (
	trustDetail       = "Unlock the iPhone and tap Trust on the \"Trust This Computer?\" prompt, then enter its passcode."
	trustDeniedDetail = "The iPhone was told not to trust this computer. Unplug it and plug it in again, then tap Trust."
	devModeDetail     = "Turn on Developer Mode: on the iPhone, open Settings > Privacy & Security > Developer Mode, turn it on and tap Restart. After the restart, unlock the iPhone, tap Turn On and enter its passcode."
	preparingDetail   = "Pinky is preparing the iPhone: it mounts Apple's developer disk image, which takes a few seconds."
	unlockDetail      = "Unlock the iPhone so Pinky can finish preparing it."
)

// trustState fills in a phone that does not let this computer read it yet.
func trustState(info driver.Info, err error) driver.Info {
	info.State = driver.StateUnauthorized
	info.Setup = driver.SetupTrust
	info.Detail = trustDetail
	if m := strings.ToLower(err.Error()); strings.Contains(m, "userdenied") {
		info.Detail = trustDeniedDetail
	}
	return info
}

// mountState fills in a phone whose developer disk image is being mounted
// or did not mount. version is the phone's iOS version.
func diskImageState(info driver.Info, j mountJob, ownDir string) driver.Info {
	info.State = driver.StateNeedsSetup
	info.Setup = driver.SetupDiskImage
	switch {
	case j.running:
		info.Setup, info.Detail = driver.SetupPreparing, preparingDetail
	case j.err == nil:
		// Mounted, and the services came up: List looks again.
		info.Setup, info.Detail = driver.SetupPreparing, preparingDetail
	case errors.Is(j.err, errServicesMissing):
		if v := parseVersion(info.OSVersion); j.img.Version != "" && parseVersion(j.img.Version).Major < v.Major {
			info.Detail = fmt.Sprintf("The developer disk image on this computer (from Xcode %s) is too old for iOS %s. Update Xcode to %d or later and open it once.", j.img.Build, info.OSVersion, v.Major)
		} else {
			info.Detail = "The developer disk image is mounted, but the iPhone does not offer the services Pinky needs. Restart the iPhone and connect it again."
		}
	default:
		switch classifyMountError(j.err) {
		case problemLocked:
			info.Setup, info.Detail = driver.SetupUnlock, unlockDetail
		case problemDevMode:
			info.Setup, info.Detail = driver.SetupDeveloperMode, devModeDetail
		case problemNoImage:
			info.Setup, info.Detail = driver.SetupNoDiskImage, noImageDetail(ownDir)
		case problemNetwork:
			info.Detail = "Pinky could not reach Apple's signing server (gs.apple.com), which signs the developer disk image for each iPhone. Check the internet connection."
		case problemRefused:
			info.Detail = "Apple's signing server would not sign the developer disk image for this iPhone: " + lastLine(j.err)
		case problemNotFor:
			info.Detail = fmt.Sprintf("The developer disk image on this computer (from Xcode %s) does not support this iPhone. Update Xcode and open it once.", j.img.Build)
		case problemNewer:
			info.Detail = "The iPhone has a newer developer disk image than this computer, and its developer services are missing. Restart the iPhone and connect it again."
		default:
			info.Detail = "Pinky could not mount the developer disk image: " + lastLine(j.err)
		}
	}
	return info
}

// noImageDetail says where to get a developer disk image.
func noImageDetail(ownDir string) string {
	folder := "Pinky's folder"
	if ownDir != "" {
		folder = filepath.Join(ownDir, "iOS_DDI")
	}
	if runtime.GOOS == "darwin" {
		return "Pinky needs Apple's developer disk image, which comes with Xcode. Install Xcode from the App Store and open it once (it installs the image; you can quit it then), or put an iOS_DDI folder in " + folder + "."
	}
	return "Pinky needs Apple's developer disk image, which comes with Xcode. Copy /Library/Developer/DeveloperDiskImages/iOS_DDI from a Mac where Xcode was opened once to " + folder + "."
}

// lastLine shortens a joined error to its last line, the last way tried.
func lastLine(err error) string {
	s := strings.TrimSpace(err.Error())
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// lockedEntry reports whether the phone behind e is locked: lockdown's
// PasswordProtected, as Device.Locked reads it.
func lockedEntry(ctx context.Context, e goios.DeviceEntry) (bool, error) {
	type result struct {
		v   bool
		err error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := goios.ConnectLockdownWithSession(e)
		if err != nil {
			ch <- result{err: err}
			return
		}
		defer conn.Close()
		v, err := conn.GetValue("PasswordProtected")
		if err != nil {
			ch <- result{err: err}
			return
		}
		b, ok := v.(bool)
		if !ok {
			ch <- result{err: fmt.Errorf("PasswordProtected is %T", v)}
			return
		}
		ch <- result{v: b}
	}()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
