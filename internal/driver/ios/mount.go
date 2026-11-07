package ios

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/imagemounter"
)

// Mounting the developer disk image, the step Xcode takes when it prepares
// a phone for development. Either way the image is personalized for the
// phone: Apple's signing server (gs.apple.com) signs its digests together
// with a nonce the phone made, and the phone checks that signature before it
// mounts anything, so only Apple's own image mounts. go-ios does the work;
// this file picks the way and keeps an image that is already there.

// cryptexService is cryptexd's RemoteXPC service. Xcode 16 and later
// install the developer disk image through it, as a cryptex.
const cryptexService = "com.apple.security.cryptexd.remote"

// ddiCryptex is the identifier the phone reports for an installed developer
// disk image cryptex (CFBundleIdentifier in its cryptex_info).
const ddiCryptex = "com.apple.MobileAsset.DDI"

// Ways a disk image is mounted.
const (
	mountCryptex      = "cryptex"
	mountPersonalized = "personalized"
)

// mountResult says what mountDiskImage did.
type mountResult struct {
	How      string // mountCryptex or mountPersonalized
	Already  bool   // the image was mounted already; nothing was done
	Replaced string // the version of an older image it took off first, if any
}

// errNewerImageMounted means the phone has a newer developer disk image
// than the one Pinky found, which Pinky leaves alone.
var errNewerImageMounted = errors.New("the iPhone has a newer developer disk image than this computer")

// errPhoneLocked means the phone is locked: it mounts no image until it is
// unlocked (CoreDevice refuses then too, "the device was still locked").
var errPhoneLocked = errors.New("the iPhone is locked")

// mountTimeout bounds one mount: the signing request (up to a minute in
// go-ios) and sending about 17 MB to the phone, which takes seconds over
// USB. A phone that locks while its image is installed as a cryptex makes
// the install wait rather than fail, so this is what ends it.
const mountTimeout = 90 * time.Second

// mountDiskImage mounts img on the phone behind l. It installs the image
// as a cryptex when the phone offers cryptexd and the image has a cryptex
// identity (Xcode 16 and later), and otherwise mounts it with the image
// mounter as a personalized image (Xcode 15's way, which iOS 17 and later
// still take). An image of the same version already on the phone is kept;
// an older one is replaced; a newer one is left alone (errNewerImageMounted).
func mountDiskImage(ctx context.Context, l *link, img diskImage) (mountResult, error) {
	ctx, cancel := context.WithTimeout(ctx, mountTimeout)
	defer cancel()
	type result struct {
		r   mountResult
		err error
	}
	ch := make(chan result, 1)
	// go-ios takes no context; the call goes on in the background if ctx
	// ends first, and its connections close with the link.
	go func() {
		r, err := mountWith(ctx, l, img)
		ch <- result{r, err}
	}()
	select {
	case r := <-ch:
		return r.r, r.err
	case <-ctx.Done():
		return mountResult{}, fmt.Errorf("mounting the developer disk image: %w", ctx.Err())
	}
}

func mountWith(ctx context.Context, l *link, img diskImage) (mountResult, error) {
	var errs []error
	if img.Version != "" && l.has(cryptexService) {
		r, err := mountAsCryptex(l, img)
		if err == nil || errors.Is(err, errNewerImageMounted) {
			return r, err
		}
		errs = append(errs, fmt.Errorf("as a cryptex: %w", err))
		if ctx.Err() != nil {
			// mountDiskImage gave up on this mount; do not start
			// another way in the background.
			return mountResult{}, errors.Join(errs...)
		}
	}
	r, err := mountPersonalizedImage(l, img)
	if err == nil {
		return r, nil
	}
	errs = append(errs, fmt.Errorf("as a personalized image: %w", err))
	return mountResult{}, errors.Join(errs...)
}

// mountAsCryptex installs img through cryptexd, the way CoreDevice does.
func mountAsCryptex(l *link, img diskImage) (mountResult, error) {
	r := mountResult{How: mountCryptex}
	m, err := imagemounter.NewCryptexDeveloperDiskImageMounter(l.entry)
	if err != nil {
		return r, err
	}
	installed, err := m.ListCryptexes()
	if err != nil {
		return r, err
	}
	for _, c := range installed {
		if c.Identifier != ddiCryptex {
			continue
		}
		switch cmp := compareVersions(c.Version, img.Version); {
		case cmp == 0:
			r.Already = true
			return r, nil
		case cmp > 0:
			return r, fmt.Errorf("%w (%s on the iPhone, %s here)", errNewerImageMounted, c.Version, img.Version)
		}
		r.Replaced = c.Version
	}
	if r.Replaced != "" {
		if err := m.UnmountImage(); err != nil {
			return r, fmt.Errorf("taking off the older image %s: %w", r.Replaced, err)
		}
	}
	return r, m.MountImage(img.Dir)
}

// mountPersonalizedImage mounts img with the image mounter. An image
// already mounted this way is kept when it is img, and replaced otherwise:
// Pinky gets here only when the phone lacks the services it needs.
func mountPersonalizedImage(l *link, img diskImage) (mountResult, error) {
	r := mountResult{How: mountPersonalized}
	v, err := goios.GetProductVersion(l.base)
	if err != nil {
		return r, err
	}
	m, err := imagemounter.NewPersonalizedDeveloperDiskImageMounter(l.base, v)
	if err != nil {
		return r, err
	}
	defer m.Close()
	images, err := m.ListImages()
	if err != nil {
		return r, err
	}
	if len(images) > 0 {
		mounted, err := m.IsImageMounted(img.Dir)
		if err != nil {
			return r, err
		}
		if mounted {
			r.Already = true
			return r, nil
		}
		if err := m.UnmountImage(); err != nil {
			return r, fmt.Errorf("taking off the image that is mounted: %w", err)
		}
		r.Replaced = "personalized"
	}
	return r, m.MountImage(img.Dir)
}

// mountProblem sorts a mount error into what the person can do about it.
type mountProblem int

const (
	problemOther   mountProblem = iota
	problemLocked               // unlock the phone
	problemDevMode              // Developer Mode is off
	problemNetwork              // Apple's signing server could not be reached
	problemNotFor               // the image does not support this phone
	problemNewer                // the phone has a newer image than this computer
	problemNoImage              // no image on this computer
	problemRefused              // Apple's server would not sign the image for this phone
)

func classifyMountError(err error) mountProblem {
	if err == nil {
		return problemOther
	}
	switch {
	case errors.Is(err, errNoDiskImage):
		return problemNoImage
	case errors.Is(err, errNewerImageMounted):
		return problemNewer
	case errors.Is(err, errPhoneLocked):
		return problemLocked
	}
	m := strings.ToLower(err.Error())
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(m, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("devicelocked", "device is locked", "device locked", "passwordprotected", "passcode"):
		return problemLocked
	case has("developermode", "developer mode"):
		return problemDevMode
	case has("no cryptex1 identity", "failed to find identity"):
		return problemNotFor
	// go-ios's signing request: "unexpected status in response 94: ..."
	// is Apple's server saying no; the others are the way there.
	case has("unexpected status in response"):
		return problemRefused
	case has("failed to send request", "unexpected response status", "gs.apple.com", "no such host", "network is unreachable", "i/o timeout"):
		return problemNetwork
	}
	return problemOther
}
