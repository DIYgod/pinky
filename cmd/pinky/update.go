package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/update"
)

// newUpdater is the updater for this installation of Pinky: its state in
// update.json next to the settings, downloads in the data folder (or the
// person's Downloads, where Pinky cannot replace itself) and the helper's
// log in update.log.
func newUpdater(dirs api.Dirs, log *slog.Logger) *update.Updater {
	return &update.Updater{
		Version:   version,
		Cache:     filepath.Join(dirs.Data, "updates"),
		Downloads: downloadsDir(),
		StatePath: filepath.Join(dirs.Config, "update.json"),
		HelperLog: filepath.Join(dirs.Data, "update.log"),
		Log:       log,
	}
}

func downloadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, "Downloads")
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return dir
	}
	return ""
}

// startedUpdate is what the app and the daemon do once they hold the
// gateway's lock: tell a swap that just happened that this version
// launched, remember the version (What's new), and note an update that did
// not start so that it is not installed again by itself. Confirm the
// returned swap once the new version is up.
func startedUpdate(u *update.Updater, log *slog.Logger) *update.Swapped {
	sw := update.CheckSwap(version)
	switch {
	case sw == nil:
	case sw.Failed:
		log.Warn("update: the new version did not start; this is the old one again", "version", sw.To, "running", version)
		u.Failed(sw.To, fmt.Errorf("Pinky %s didn't start, so Pinky went back to %s", sw.To, version))
	default:
		log.Info("update: running the new version", "from", sw.From, "to", sw.To)
	}
	u.Change(func(s *update.State) { s.Launched(version) })
	return sw
}

// daemonUpdates checks for updates while `pinky daemon` runs: at start
// and every few hours, quietly on failure. A daemon may run under a
// service manager or from a package, so it does not replace itself; it
// says what to do.
func daemonUpdates(ctx context.Context, u *update.Updater, log *slog.Logger) {
	if !update.IsRelease(version) {
		return
	}
	said := ""
	check := func() {
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		r, err := u.Check(cctx)
		switch {
		case err != nil:
			log.Debug("update check", "err", err)
		case r != nil && r.Version != said:
			said = r.Version
			log.Info("update: a new version of Pinky is available: run `pinky update` after `pinky quit`, or update from the app",
				"version", r.Version, "running", version, "how", howToUpdate(u.Where()))
		}
	}
	check()
	t := time.NewTicker(update.CheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}

func howToUpdate(l update.Location) string {
	switch {
	case l.Method == update.MethodManager:
		return l.Command
	case l.CanReplace():
		return "pinky update"
	case l.Problem != "":
		return l.Problem
	}
	return "download it from " + update.ReleasePage("")
}

// runUpdate is `pinky update [--check]`: it says whether a newer version
// is out and, unless --check, installs it while Pinky is not running.
func runUpdate(args []string) error {
	fs := flag.NewFlagSet("pinky update", flag.ContinueOnError)
	checkOnly := fs.Bool("check", false, "only say whether a newer version is out")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !update.IsRelease(version) {
		return fmt.Errorf("this is a development build (%s), which doesn't update itself", version)
	}
	dirs, err := api.DefaultDirs()
	if err != nil {
		return err
	}
	if err := dirs.Ensure(); err != nil {
		return err
	}
	u := newUpdater(dirs, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	r, err := u.Check(ctx)
	if err != nil {
		return err
	}
	if r == nil {
		fmt.Printf("Pinky %s is up to date.\n", version)
		return nil
	}
	fmt.Printf("Pinky %s is available (this is %s).\n", r.Version, version)
	if *checkOnly {
		return nil
	}
	loc := u.Where()
	if loc.Method == update.MethodManager {
		return fmt.Errorf("%s installed Pinky; update it with: %s", loc.Manager, loc.Command)
	}
	if ctl, err := api.DialControl(dirs); err == nil {
		ctl.Close()
		return errors.New("Pinky is running: it installs updates itself (Settings › Advanced), or quit it first with `pinky quit`")
	}
	last := time.Time{}
	p, err := u.Prepare(ctx, *r, func(done, total int64) {
		if time.Since(last) > time.Second || done == total {
			last = time.Now()
			fmt.Fprintf(os.Stderr, "\rDownloading %d of %d MB", done>>20, total>>20)
		}
	})
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return err
	}
	if p.Staged == nil {
		if p.Method == update.MethodInstaller {
			fmt.Printf("Downloaded the installer to %s; run it to update Pinky.\n", p.File)
		} else {
			fmt.Printf("Downloaded %s. %s\n", p.File, loc.Problem)
		}
		return nil
	}
	sn, ok := p.Staged.(update.SwapNower)
	if !ok {
		p.Staged.Discard()
		return errors.New("this installation cannot be updated from the command line")
	}
	if err := sn.SwapNow(); err != nil {
		return err
	}
	fmt.Printf("Installed Pinky %s at %s.\n", r.Version, loc.App)
	return nil
}
