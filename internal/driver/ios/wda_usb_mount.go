package ios

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	goios "github.com/danielpaulus/go-ios/ios"
)

// legacyImageMissing starts an iOS 16 mount outside List. A mount can outlive
// its caller because go-ios's image mounter has no context; the job stays
// running until the underlying call returns, preventing duplicate mounts.
func (p *Provider) legacyImageMissing(e goios.DeviceEntry, info driver.Info) driver.Info {
	id := e.Properties.SerialNumber
	find := p.legacyImageFind
	if find == nil {
		find = func(version string) string { return legacyImagePath(p.DiskImageDir, parseVersion(version)) }
	}
	prepare := p.legacyImagePrepare
	if prepare == nil {
		prepare = prepareLegacyImageBackground
	}
	j := p.prep.legacyMountState(e, info.OSVersion, p.DiskImageDir, find, prepare, func() { p.forgetPhone(id) }, p.logger())
	info.State = driver.StateNeedsSetup
	switch {
	case j.running, j.err == nil:
		info.Setup, info.Detail = driver.SetupPreparing, preparingDetail
	case classifyMountError(j.err) == problemLocked:
		info.Setup, info.Detail = driver.SetupUnlock, unlockDetail
	default:
		return wdaImageSetup(info, j.err)
	}
	return info
}

func (pr *preparer) legacyMountState(e goios.DeviceEntry, version, ownDir string,
	find func(string) string, prepare func(context.Context, goios.DeviceEntry, string, string) error,
	done func(), log *slog.Logger) mountJob {
	id, key := e.Properties.SerialNumber, keyOf(e)
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if pr.legacyJobs == nil {
		pr.legacyJobs = make(map[string]*mountJob)
	}
	j := pr.legacyJobs[id]
	if j != nil && j.key == key && (j.running || pr.clock().Sub(j.ended) < retryAfter(j.err)) {
		return *j
	}
	j = &mountJob{key: key, running: true}
	pr.legacyJobs[id] = j
	log = logTo(log)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), helperStartTimeout)
		defer cancel()
		start := time.Now()
		var err error
		if img := find(version); img == "" {
			err = fmt.Errorf("no compatible developer disk image for iOS %s: %w", version, errNoDiskImage)
		} else {
			log.Info("iphone: mounting the iOS 16 developer disk image", "device", id, "version", version, "image", img)
			err = prepare(ctx, e, version, ownDir)
		}
		// Logged before the job ends, as preparer.run does, so that whoever
		// sees the job ended finds its lines written.
		took := time.Since(start).Round(time.Millisecond)
		switch {
		case err == nil:
			log.Info("iphone: iOS 16 developer disk image ready", "device", id, "took", took)
		case errors.Is(err, errNoDiskImage):
			log.Warn("iphone: no iOS 16 developer disk image to mount", "device", id, "version", version, "retry_in", retryAfter(err))
		default:
			log.Warn("iphone: could not mount the iOS 16 developer disk image", "device", id, "version", version, "took", took, "err", err, "retry_in", retryAfter(err))
		}
		pr.mu.Lock()
		j.running, j.ended, j.err = false, pr.clock(), err
		pr.mu.Unlock()
		if done != nil {
			done()
		}
	}()
	return *j
}

// prepareLegacyImageBackground checks the lock first, then waits for the
// actual mount call. A timed-out bounded call would keep mounting while the
// next discovery pass starts another one.
func prepareLegacyImageBackground(ctx context.Context, e goios.DeviceEntry, version, ownDir string) error {
	state, err := phoneStateOf(ctx, e)
	if err != nil {
		return err
	}
	if state.locked {
		return errPhoneLocked
	}
	if probeLockdownXCTest(ctx, e) == nil {
		return nil
	}
	img := legacyImagePath(ownDir, parseVersion(version))
	if img == "" {
		return fmt.Errorf("no compatible developer disk image for iOS %s: %w", version, errNoDiskImage)
	}
	if err := mountLegacyImage(e, img); err != nil {
		if state, checkErr := phoneStateOf(context.Background(), e); checkErr == nil && state.locked {
			return errPhoneLocked
		}
		return fmt.Errorf("mounting the iOS 16 developer disk image: %w", err)
	}
	if err := probeLockdownXCTest(ctx, e); err != nil {
		if state, checkErr := phoneStateOf(context.Background(), e); checkErr == nil && state.locked {
			return errPhoneLocked
		}
		return fmt.Errorf("the iOS 16 developer disk image did not make XCTest available: %w", err)
	}
	return nil
}
