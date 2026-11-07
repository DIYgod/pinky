package ui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/update"
)

// Updates. Pinky looks for a new version at launch and every few hours
// (Options.Updates), quietly when that fails. A card above the sidebar's
// footer offers a newer version with its notes, Update now and Later.
// With Settings › Advanced › Update automatically on, Pinky also installs
// it by itself once nobody has used Pinky for a while and no agent works
// (update.Idle), and updates Pinky Companion on Android phones that are
// free. After an update a card offers What's new: the changelog entries
// newer than the version that ran before, and the companion updates.

// companionUpdater updates a phone's companion app without asking
// (Gateway.UpdateCompanion).
type companionUpdater interface {
	UpdateCompanion(ctx context.Context, deviceID string) (driver.CompanionUpdate, error)
}

type updPhase int

const (
	updIdle        updPhase = iota
	updDownloading          // downloading and checking the release
	updReady                // staged; waiting for a quiet moment to restart
	updRestarting           // the helper waits for Pinky to quit
	updFailed
)

// updateState is the client's side of updates.
type updateState struct {
	started  bool
	rel      *update.Release // a newer version, from the last check
	checking bool
	asked    bool   // the person asked for the check under way
	checkErr string // why the check the person asked for failed

	phase       updPhase
	auto        bool // the install under way started by itself
	done, total int64
	err         string
	cancel      context.CancelFunc
	prepared    *update.Prepared
	file        string // a release downloaded for the person to install

	notesOpen bool // the release notes of rel
	newsOpen  bool // What's new
	newsAll   bool // What's new shows this version's notes, from Settings
	confirm   bool // restart while something runs?

	lastInput time.Time // the person's last input in Pinky's windows
	comp      map[string]*compAuto

	// Tests replace these.
	quit     func()
	idleTime func() time.Duration
	first    time.Duration // the wait before the first check
}

// compAuto is the automatic companion update of one phone.
type compAuto struct {
	running  bool
	needsYou bool      // the phone would ask its owner: the card's Update button
	failedAt time.Time // the last failure; tried again hours later
}

var (
	icUpdate  = icon(`<path d="M12 3v12"/><path d="m7 10 5 5 5-5"/><path d="M5 15v4a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-4"/>`)
	icSparkle = icon(`<path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/><path d="M19 17v4"/><path d="M17 19h4"/>`)
)

// startUpdates begins checking for updates once the main window exists.
func (a *app) startUpdates() {
	s := &a.upd
	if a.opt.Updates == nil || s.started {
		return
	}
	s.started = true
	if s.quit == nil {
		s.quit = func() { mygo.App.Quit() }
	}
	if s.idleTime == nil {
		s.idleTime = mygo.Power.IdleTime
	}
	if s.first == 0 {
		s.first = 10 * time.Second
	}
	if a.main != nil {
		// The person was using Pinky until its window lost the keyboard,
		// and is again when it gets it back.
		a.main.OnBlur(func() { a.upd.lastInput = time.Now() })
		a.main.OnFocus(func() { a.upd.lastInput = time.Now() })
	}
	go a.updateLoop()
}

func (a *app) updateLoop() {
	first := time.NewTimer(a.upd.first)
	defer first.Stop()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	next := first.C
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-next:
			a.post(func() { a.checkUpdate(false) })
			next = time.After(update.CheckEvery)
		case <-tick.C:
			a.post(func() { a.updateTick(time.Now()) })
		}
	}
}

// checkUpdate asks the feed for a newer version; manual is the person's
// Check now, which reports the answer.
func (a *app) checkUpdate(manual bool) {
	u, s := a.opt.Updates, &a.upd
	if u == nil {
		return
	}
	if manual {
		s.asked, s.checkErr = true, ""
	}
	if s.checking {
		return
	}
	s.checking = true
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		r, err := u.Check(ctx)
		a.post(func() { a.checked(r, err) })
	}()
}

func (a *app) checked(r *update.Release, err error) {
	s := &a.upd
	s.checking = false
	asked := s.asked
	s.asked = false
	switch {
	case errors.Is(err, update.ErrDevelopment):
		if asked {
			s.checkErr = "This is a development build, which doesn't update itself."
		}
	case err != nil:
		log.Printf("pinky: update check: %v", err)
		if asked {
			s.checkErr = "Couldn't check for updates: " + updateError(err)
		}
	default:
		if s.phase == updReady && (r == nil || s.prepared == nil || r.Version != s.prepared.Release.Version) {
			// A newer release (or none) replaces the one waiting to restart.
			if s.prepared != nil && s.prepared.Staged != nil {
				s.prepared.Staged.Discard()
			}
			s.prepared, s.phase = nil, updIdle
		}
		if s.phase == updIdle || s.phase == updFailed {
			s.rel = r
		}
		if r == nil && asked {
			a.toasts = append(a.toasts, "Pinky is up to date")
		}
	}
}

// updateTick runs every minute: it notes the person's input and, with
// automatic updates on, installs a release or updates companions once
// everything is quiet.
func (a *app) updateTick(now time.Time) {
	s := &a.upd
	if a.focused && s.idleTime != nil {
		if at := now.Add(-s.idleTime()); at.After(s.lastInput) {
			s.lastInput = at
		}
	}
	if !a.settings.AutoUpdate || a.opt.Updates == nil {
		return
	}
	a.companionTick(now)
	ok, why := update.Idle(now, a.activity(now))
	switch {
	case s.phase == updReady && ok:
		a.restartForUpdate()
	case s.phase == updIdle && s.rel != nil && a.opt.Updates.Where().CanReplace():
		if st := a.opt.Updates.State(); update.Compare(st.Failed, s.rel.Version) == 0 {
			return // failed before: only the person's Update now tries again
		}
		if !ok {
			logf("pinky: update %s waits: %s", s.rel.Version, why)
			return
		}
		a.installUpdate(true)
	}
}

// activity is what keeps Pinky from restarting by itself.
func (a *app) activity(now time.Time) update.Activity {
	act := update.Activity{Needs: len(a.needs), LastInput: a.upd.lastInput}
	for _, t := range a.tasks {
		end := t.Ended
		if running(t.State) {
			act.Tasks++
			end = now
		}
		if end.After(act.LastTask) {
			act.LastTask = end
		}
	}
	for id, on := range a.pin.unlocking {
		if on {
			act.Busy = append(act.Busy, "unlocking "+a.deviceName(id))
		}
	}
	for id := range a.comp.installing {
		act.Busy = append(act.Busy, "installing Pinky Companion on "+a.deviceName(id))
	}
	for _, d := range a.devices {
		if d.Status == gateway.DeviceInControl {
			act.Busy = append(act.Busy, "you control "+d.Info.Name)
		}
	}
	if a.add.pair.cancel != nil {
		act.Busy = append(act.Busy, "pairing a phone")
	}
	if a.rec.pl != nil {
		act.Busy = append(act.Busy, "playing a recording")
	}
	return act
}

func (a *app) deviceName(id string) string {
	if d := a.device(id); d != nil && d.Info.Name != "" {
		return d.Info.Name
	}
	return id
}

// installUpdate downloads and checks the release; auto is an update Pinky
// started by itself, which restarts only while things stay quiet.
func (a *app) installUpdate(auto bool) {
	u, s := a.opt.Updates, &a.upd
	if u == nil || s.rel == nil || s.phase == updDownloading || s.phase == updRestarting {
		return
	}
	if s.phase == updReady && s.prepared != nil {
		s.auto = auto
		a.restartForUpdate()
		return
	}
	r := *s.rel
	ctx, cancel := context.WithCancel(a.ctx)
	s.phase, s.auto, s.done, s.total, s.err, s.cancel, s.file = updDownloading, auto, 0, r.Asset.Size, "", cancel, ""
	log.Printf("pinky: updating to %s (automatic: %v)", r.Version, auto)
	go func() {
		p, err := u.Prepare(ctx, r, func(done, total int64) {
			a.post(func() { s.done, s.total = done, total })
		})
		a.post(func() { a.prepared(p, err) })
	}()
}

func (a *app) prepared(p *update.Prepared, err error) {
	s := &a.upd
	s.cancel = nil
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.phase = updIdle
			return
		}
		log.Printf("pinky: update: %v", err)
		s.phase, s.err = updFailed, updateError(err)
		if s.auto && s.rel != nil {
			a.opt.Updates.Failed(s.rel.Version, err)
		}
		return
	}
	switch p.Method {
	case update.MethodSelf:
		s.prepared, s.phase = p, updReady
		if !s.auto {
			a.restartForUpdate()
			return
		}
		// Downloading took a while: restart only if it is still quiet,
		// else at a later tick, or when the person clicks Restart.
		if ok, _ := update.Idle(time.Now(), a.activity(time.Now())); ok {
			a.restartForUpdate()
		}
	case update.MethodInstaller:
		// The installer replaces Pinky; it must not be running then.
		if err := mygo.Shell.OpenPath(p.File); err != nil {
			s.phase, s.err = updFailed, "Couldn't open the installer: "+err.Error()
			return
		}
		s.phase = updRestarting
		s.quit()
	default:
		s.phase, s.file = updIdle, p.File
		a.reveal(p.File)
		a.toasts = append(a.toasts, "Downloaded Pinky "+p.Release.Version)
	}
}

// restartForUpdate swaps the staged version in once Pinky quits, and
// quits. An automatic update brings the window back only if it was open.
func (a *app) restartForUpdate() {
	s := &a.upd
	if s.prepared == nil || s.prepared.Staged == nil {
		return
	}
	hidden := a.main == nil || !a.main.IsVisible()
	opt := update.StartOptions{
		Background: s.auto,
		Args:       relaunchArgs(os.Args[1:], s.auto && hidden),
		Env:        relaunchEnv(os.Environ()),
	}
	if err := s.prepared.Staged.Swap(opt); err != nil {
		log.Printf("pinky: update: %v", err)
		s.prepared.Staged.Discard()
		s.prepared, s.phase, s.err = nil, updFailed, updateError(err)
		return
	}
	s.phase = updRestarting
	log.Printf("pinky: restarting for version %s", s.prepared.Release.Version)
	s.quit()
}

// relaunchArgs are the arguments the new version starts with: those this
// one had, with --background when its window should stay closed.
func relaunchArgs(args []string, background bool) []string {
	var out []string
	for _, arg := range args {
		switch strings.TrimLeft(arg, "-") {
		case "background", "background=true", "background=false":
			continue
		}
		out = append(out, arg)
	}
	if len(out) > 0 && out[0] == "app" {
		out = out[1:]
	}
	if background {
		out = append(out, "--background")
	}
	return out
}

// relaunchEnv passes Pinky's own variables to the new version (PINKY_HOME,
// a test feed), which macOS's open would drop.
func relaunchEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		if strings.HasPrefix(kv, "PINKY_") || strings.HasPrefix(kv, "MYGO_AUTOMATION=") {
			out = append(out, kv)
		}
	}
	return out
}

// updateError words an update failure for the card.
func updateError(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, update.ErrChecksum):
		return "The download was damaged. Try again."
	case errors.Is(err, update.ErrSignature):
		return "The update isn't signed by Pinky's developer, so Pinky didn't install it."
	case errors.Is(err, update.ErrNoAsset):
		return "There's no build of this version for this computer yet."
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne):
		return "Couldn't reach the update server. Check the connection and try again."
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}

func (a *app) laterUpdate() {
	s := &a.upd
	if s.rel == nil {
		return
	}
	v := s.rel.Version
	a.opt.Updates.Change(func(st *update.State) {
		st.Later, st.LaterUntil = v, time.Now().Add(24*time.Hour)
	})
	if s.phase == updFailed {
		s.phase, s.err = updIdle, ""
	}
}

// updateNow is the card's Update now: with an agent at work, it asks first.
func (a *app) updateNow() {
	if act := a.activity(time.Now()); act.Tasks > 0 || len(act.Busy) > 0 {
		a.upd.confirm = true
		return
	}
	a.installUpdate(false)
}

// --- the sidebar cards ---

// updateCards are the update and What's new cards above the sidebar's
// footer.
func (a *app) updateCards(c *mg.Context) {
	if a.opt.Updates == nil {
		return
	}
	a.newsCard(c)
	a.updateCard(c)
}

func sideCard(c *mg.Context, key, label string, fn func()) {
	p := pal(c)
	mg.Column(c.Key(key)).Padding(12).Radius(10).Background(p.panel).Border(1, p.line).Gap(6).Margin(0, 0, 8, 0).
		Label(label).Children(fn)
}

func (a *app) updateCard(c *mg.Context) {
	s := &a.upd
	u := a.opt.Updates
	if s.rel == nil && s.phase != updRestarting {
		return
	}
	if s.phase == updIdle && u.State().Snoozed(s.rel.Version, time.Now()) {
		return
	}
	p := pal(c)
	v := ""
	if s.rel != nil {
		v = s.rel.Version
	}
	sideCard(c, "update", "Update", func() {
		head := func(title string) {
			mg.Row(c).Gap(7).AlignItems(mg.Center).Children(func() {
				mg.Icon(c, icUpdate).FontSize(15).TextColor(p.accentInk)
				mg.Text(c, title).FontSize(13.5).FontWeight(650).Grow(1).MinWidth(0)
			})
		}
		switch s.phase {
		case updDownloading:
			head("Downloading Pinky " + v)
			frac := 0.0
			if s.total > 0 {
				frac = float64(s.done) / float64(s.total)
			}
			mg.Progress(c, frac).FillWidth().Label("Download")
			mg.Row(c).AlignItems(mg.Center).Children(func() {
				mg.Text(c, fmt.Sprintf("%s of %s", mb(s.done), mb(s.total))).FontSize(12).TextColor(p.ink2).Grow(1)
				if quietButton(c, "Cancel").Clicked() && s.cancel != nil {
					s.cancel()
				}
			})
			c.After(250 * time.Millisecond)
		case updReady:
			head("Pinky " + v + " is ready")
			mg.Text(c, "Pinky restarts to finish the update.").FontSize(12.5).TextColor(p.ink2)
			mg.Row(c).Gap(10).AlignItems(mg.Center).Children(func() {
				if button(c, "Restart now", btnOpt{kind: btnPrimary, small: true}).Clicked() {
					a.upd.auto = false
					a.updateNow()
				}
			})
		case updRestarting:
			head("Restarting Pinky…")
			mg.Progress(c, -1).FillWidth().Label("Restarting")
		case updFailed:
			head("Couldn't update")
			mg.Text(c, s.err).FontSize(12.5).TextColor(p.red).Role(mg.RoleStatus)
			mg.Row(c).Gap(10).AlignItems(mg.Center).Children(func() {
				if button(c, "Try again", btnOpt{small: true}).Clicked() {
					s.phase, s.err = updIdle, ""
					a.updateNow()
				}
				if quietButton(c, "Later").Clicked() {
					a.laterUpdate()
				}
			})
		default:
			head("Pinky " + v + " is available")
			if s.rel.Notes != "" {
				if linkButton(c, "What's new").Label("What's new in Pinky " + v).Clicked() {
					s.notesOpen = true
				}
			}
			a.updateActions(c)
		}
	})
}

// updateActions are the card's buttons for the way this installation
// updates.
func (a *app) updateActions(c *mg.Context) {
	p := pal(c)
	s := &a.upd
	loc := a.opt.Updates.Where()
	switch {
	case loc.Method == update.MethodManager:
		mg.Text(c, loc.Manager+" installed Pinky; update it there:").FontSize(12.5).TextColor(p.ink2)
		mg.Row(c).Gap(6).AlignItems(mg.Center).Children(func() {
			mg.Text(c, loc.Command).FontSize(12).Font("monospace").Grow(1).MinWidth(0).Selectable()
			b := mg.ButtonBase(c).Size(24, 24).Radius(6).Center().Label("Copy the command").Tooltip("Copy")
			if b.Hovered() {
				b.Background(p.bg3)
			}
			if b.Clicked() {
				mygo.Clipboard.WriteText(loc.Command)
				a.toasts = append(a.toasts, "Copied "+loc.Command)
			}
			b.TextColor(p.ink2).Children(func() { mg.Icon(c, icCopy).FontSize(13) })
		})
		if quietButton(c, "Later").Clicked() {
			a.laterUpdate()
		}
		return
	case loc.CanReplace(), loc.Method == update.MethodInstaller:
	default:
		if loc.Problem != "" {
			mg.Text(c, loc.Problem).FontSize(12).TextColor(p.ink2)
		}
		if s.file != "" {
			mg.Text(c, "Downloaded. Open it to install.").FontSize(12).TextColor(p.ink2)
		}
	}
	label := "Update now"
	switch {
	case loc.Method == update.MethodInstaller:
		label = "Download and install"
	case !loc.CanReplace():
		label = "Download"
		if s.file != "" {
			label = "Show download"
		}
	}
	mg.Row(c).Gap(10).AlignItems(mg.Center).Margin(2, 0, 0, 0).Children(func() {
		if button(c, label, btnOpt{kind: btnPrimary, small: true}).Label(label + " Pinky " + s.rel.Version).Clicked() {
			if s.file != "" && !loc.CanReplace() {
				a.reveal(s.file)
			} else {
				a.updateNow()
			}
		}
		if quietButton(c, "Later").Clicked() {
			a.laterUpdate()
		}
	})
}

// news is what What's new shows: a title, a line for the card, and the
// Markdown of the dialog.
func (a *app) news(all bool) (title, line, md string, show bool) {
	u := a.opt.Updates
	st := u.State()
	from := st.NotesFrom
	if all && !update.IsRelease(from) {
		from = ""
	}
	var parts []string
	if update.IsRelease(from) || all {
		entries := update.Since(update.ParseChangelog(a.opt.Changelog), from, a.opt.Version)
		if md := update.Markdown(entries); md != "" {
			parts = append(parts, md)
		}
	}
	appUpdated := update.IsRelease(st.NotesFrom)
	if len(st.Changes) > 0 && !all || len(st.Changes) > 0 && appUpdated {
		var b strings.Builder
		b.WriteString("### Pinky Companion\n")
		for _, ch := range st.Changes {
			fmt.Fprintf(&b, "\n- Updated on %s to %s", ch.Device, ch.To)
			if !ch.At.IsZero() {
				b.WriteString(", " + ch.At.Local().Format("Jan 2 at 15:04"))
			}
		}
		parts = append(parts, b.String())
	}
	md = strings.Join(parts, "\n\n")
	switch {
	case all:
		title, line = "What's new in Pinky "+a.opt.Version, ""
		if md == "" {
			md = "The changelog has no notes for this version."
		}
		return title, line, md, true
	case appUpdated:
		if md == "" {
			md = "Pinky was updated to " + a.opt.Version + "."
		}
		return "What's new in Pinky " + a.opt.Version, "Now " + a.opt.Version, md, true
	case len(st.Changes) > 0:
		last := st.Changes[len(st.Changes)-1]
		line = "On " + last.Device
		if n := len(st.Changes); n > 1 {
			line = fmt.Sprintf("On %d phones", n)
		}
		return "Pinky Companion was updated", line, md, true
	}
	return "", "", "", false
}

// newsCard is What's new after an update, until the person dismisses it.
func (a *app) newsCard(c *mg.Context) {
	title, line, _, show := a.news(false)
	if !show {
		return
	}
	p := pal(c)
	card := "Pinky was updated"
	if !update.IsRelease(a.opt.Updates.State().NotesFrom) {
		card = title
	}
	sideCard(c, "news", "What's new", func() {
		mg.Row(c).Gap(7).AlignItems(mg.Center).Children(func() {
			mg.Icon(c, icSparkle).FontSize(15).TextColor(p.accentInk)
			mg.Text(c, card).FontSize(13.5).FontWeight(650).Grow(1).MinWidth(0)
			x := mg.ButtonBase(c).Size(22, 22).Radius(6).Center().Label("Dismiss What's new").Tooltip("Dismiss")
			if x.Hovered() {
				x.Background(p.bg3)
			}
			if x.Clicked() {
				a.dismissNews()
			}
			x.TextColor(p.ink3).Children(func() { mg.Icon(c, icX).FontSize(12) })
		})
		if line != "" {
			mg.Text(c, line).FontSize(12.5).TextColor(p.ink2)
		}
		if linkButton(c, "See what's new").Clicked() {
			a.upd.newsOpen, a.upd.newsAll = true, false
		}
	})
}

func (a *app) dismissNews() {
	a.opt.Updates.Change(func(st *update.State) { st.NotesFrom, st.Changes = "", nil })
}

// updateDialogs are the release notes and What's new, over the window.
func (a *app) updateDialogs(c *mg.Context) {
	s := &a.upd
	if a.opt.Updates == nil {
		return
	}
	if s.notesOpen && s.rel != nil {
		r := s.rel
		mg.Modal(c.Key("release-notes"), &s.notesOpen, func() {
			a.notesDialog(c, "Pinky "+r.Version, r.Date, r.Notes, func() {
				if s.phase == updIdle || s.phase == updFailed {
					if button(c, "Update now", btnOpt{kind: btnPrimary}).Clicked() {
						s.notesOpen = false
						a.updateNow()
					}
				}
			}, func() { s.notesOpen = false })
		})
	}
	if s.newsOpen {
		title, _, md, show := a.news(s.newsAll)
		if !show {
			s.newsOpen = false
		} else {
			all := s.newsAll
			if mg.Modal(c.Key("whats-new"), &s.newsOpen, func() {
				a.notesDialog(c, title, time.Time{}, md, nil, func() {
					s.newsOpen = false
					if !all {
						a.dismissNews()
					}
				})
			}).Dismissed() && !all {
				a.dismissNews()
			}
		}
	}
	if s.confirm {
		msg := "Pinky restarts to update."
		if act := a.activity(time.Now()); act.Tasks > 0 {
			msg += " The agent task that runs now stops."
		} else if len(act.Busy) > 0 {
			msg += " This stops " + strings.Join(act.Busy, ", ") + "."
		}
		if mg.AlertDialog(c.Key("update-confirm"), &s.confirm, "Update Pinky now?", msg, "Cancel", "Update now") == 1 {
			a.installUpdate(false)
		}
	}
}

func (a *app) notesDialog(c *mg.Context, title string, date time.Time, md string, extra func(), done func()) {
	p := pal(c)
	mg.Column(c).Width(520).MaxWidth(640).Gap(14).Children(func() {
		mg.Column(c).Gap(2).Children(func() {
			mg.Text(c, title).FontSize(18).FontWeight(650).Role(mg.RoleHeading)
			if !date.IsZero() {
				mg.Text(c, date.Local().Format("January 2, 2006")).FontSize(13).TextColor(p.ink2)
			}
		})
		mg.Scroll(c).MaxHeight(420).Children(func() { markdown.View(c, md, nil) })
		mg.Row(c).Gap(10).Justify(mg.End).AlignItems(mg.Center).Children(func() {
			if extra != nil {
				extra()
			}
			if button(c, "Close", btnOpt{}).Clicked() {
				done()
			}
		})
	})
}

func mb(n int64) string {
	switch {
	case n >= 10<<20:
		return fmt.Sprintf("%d MB", n>>20)
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", (n+1023)>>10)
}

// --- Settings › Advanced ---

func (a *app) settingsUpdates(c *mg.Context) {
	p := pal(c)
	u := a.opt.Updates
	if u == nil {
		if a.opt.Version != "" {
			mg.Text(c, "Pinky "+a.opt.Version).FontSize(12.5).TextColor(p.ink3)
		}
		return
	}
	s := &a.upd
	mg.Column(c).Label("Updates").Children(func() {
		mg.Text(c, "Updates").FontSize(15).FontWeight(650).Margin(0, 0, 4, 0)
		a.toggleRow(c, "auto-update", "Update automatically",
			"Pinky, and Pinky Companion on your Android phones, update when no one is using them.", false, &a.settings.AutoUpdate)
		settingRow(c, "Pinky "+nonEmpty(a.opt.Version, update.DevVersion), a.updateLine(), false, func() {
			b := button(c, "Check now", btnOpt{})
			if s.checking {
				b.Disabled(true)
			} else if b.Clicked() {
				a.checkUpdate(true)
			}
		})
		settingRow(c, "What's new", "The changes in this version", true, func() {
			if button(c, "Show", btnOpt{}).Label("Show what's new").Clicked() {
				s.newsOpen, s.newsAll = true, true
			}
		})
	})
}

// updateLine says where updates stand, for Settings.
func (a *app) updateLine() string {
	s := &a.upd
	u := a.opt.Updates
	st := u.State()
	var line string
	switch {
	case !update.IsRelease(a.opt.Version):
		return "Development builds don't update themselves."
	case s.checking:
		line = "Checking for updates…"
	case s.checkErr != "":
		line = s.checkErr
	case s.rel != nil:
		line = "Pinky " + s.rel.Version + " is available."
	case !st.Checked.IsZero():
		line = "Up to date. Checked " + agoLine(st.Checked, time.Now()) + "."
	default:
		line = "Pinky looks for updates every few hours."
	}
	if loc := u.Where(); !loc.CanReplace() {
		switch {
		case loc.Method == update.MethodManager:
			line += " " + loc.Manager + " updates this copy: " + loc.Command + "."
		case loc.Problem != "":
			line += " " + loc.Problem
		}
	}
	if st.Failed != "" && s.rel != nil && update.Compare(st.Failed, s.rel.Version) == 0 && st.FailedWhy != "" {
		line += " The last try failed: " + st.FailedWhy
	}
	return line
}

func agoLine(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		if m := int(d / time.Minute); m > 1 {
			return fmt.Sprintf("%d minutes ago", m)
		}
		return "a minute ago"
	case d < 24*time.Hour:
		if h := int(d / time.Hour); h > 1 {
			return fmt.Sprintf("%d hours ago", h)
		}
		return "an hour ago"
	}
	return "on " + t.Local().Format("Jan 2")
}

// --- the companion app ---

// companionTick updates the companion app on Android phones that have an
// older one and are free: no task, request or person at them, and none
// for a while, so the driver's own use of the app has ended.
func (a *app) companionTick(now time.Time) {
	cu, ok := a.api.(companionUpdater)
	if !ok {
		return
	}
	if a.upd.comp == nil {
		a.upd.comp = map[string]*compAuto{}
	}
	for _, d := range a.devices {
		id := d.Info.ID
		if !d.Features.Companion || d.Status == gateway.DeviceOffline || d.Info.State != driver.StateOnline {
			continue
		}
		ca := a.upd.comp[id]
		if ca == nil {
			ca = &compAuto{}
			a.upd.comp[id] = ca
		}
		if ca.running || ca.needsYou || now.Sub(ca.failedAt) < 6*time.Hour || !a.comp.installing[id].IsZero() {
			continue
		}
		if time.Since(a.comp.checked[id]) > 10*time.Minute {
			a.companionFor(d) // asks in the background; a later tick acts on it
			continue
		}
		if a.comp.state[id] != driver.CompanionOutdated || !a.phoneFree(d, now) {
			continue
		}
		a.updateCompanion(cu, d, ca)
	}
}

// phoneFree reports whether a phone has nothing going on that a companion
// update could disturb.
func (a *app) phoneFree(d gateway.DeviceView, now time.Time) bool {
	switch d.Status {
	case gateway.DeviceWorking, gateway.DeviceNeedsYou, gateway.DeviceInControl:
		return false
	}
	if len(a.needsOf(d.Info.ID)) > 0 || a.pin.unlocking[d.Info.ID] {
		return false
	}
	if t := a.taskOf(d.Info.ID); t != nil && (running(t.State) || now.Sub(t.Ended) < 2*time.Minute) {
		return false
	}
	return true
}

func (a *app) updateCompanion(cu companionUpdater, d gateway.DeviceView, ca *compAuto) {
	ci, _ := a.api.(companionInstaller)
	id, name := d.Info.ID, d.Info.Name
	ca.running = true
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 3*time.Minute)
		defer cancel()
		res, err := cu.UpdateCompanion(ctx, id)
		var cs driver.CompanionState
		serr := errors.New("no installer")
		if ci != nil {
			cs, serr = ci.CompanionState(ctx, id)
		}
		a.post(func() {
			ca.running = false
			if serr == nil {
				a.comp.state[id], a.comp.checked[id] = cs, time.Now()
			}
			switch {
			case errors.Is(err, driver.ErrNeedsPerson):
				log.Printf("pinky: companion on %s: %v", name, err)
				ca.needsYou = true
			case err != nil:
				log.Printf("pinky: update the companion on %s: %v", name, err)
				ca.failedAt = time.Now()
			case res.Updated:
				log.Printf("pinky: updated the companion on %s: %s -> %s", name, res.From, res.To)
				a.toasts = append(a.toasts, "Updated Pinky Companion on "+name)
				a.opt.Updates.Change(func(st *update.State) {
					st.Changes = append(st.Changes, update.Change{Kind: "companion", Device: name, From: res.From, To: res.To, At: time.Now()})
				})
			}
		})
	}()
}

// companionUpdating reports whether Pinky is updating a phone's companion
// by itself, for its card.
func (a *app) companionUpdating(id string) bool {
	ca := a.upd.comp[id]
	return ca != nil && ca.running
}

// companionAutoLine is the outdated companion card's extra line.
func (a *app) companionAutoLine(id string) string {
	if a.opt.Updates == nil || !a.settings.AutoUpdate {
		return ""
	}
	if ca := a.upd.comp[id]; ca != nil && ca.needsYou {
		return "The phone asks before apps are installed over USB, so this update needs you."
	}
	if _, ok := a.api.(companionUpdater); !ok {
		return ""
	}
	return "Pinky updates it by itself when the phone is free."
}
