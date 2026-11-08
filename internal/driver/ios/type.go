package ios

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// Typing pastes: the person's pasteboard is saved, set to the text,
// Command-V goes through the virtual keyboard, and once the field shows the
// text the pasteboard gets its old contents back. Pasting enters any text
// whatever the active input method (a Pinyin keyboard turns typed letters
// into a composition), and is as fast for a page as for a word. What is
// saved stays in memory, for the length of one Type call, and is never
// logged or shown.
//
// Without the pasteboard service Type falls back to keys: printable ASCII
// only, through a Latin keyboard (see typeKeys).

// TypeStats says how the last Type call went, for tests and iosctl. It
// holds neither the text nor the pasteboard.
type TypeStats struct {
	Method    string // "paste" or "keys"
	Saved     string // what was saved: "full", "primary" (each item's first representation), "empty", "unreadable"
	Restore   string // "restored", "cleared", "left" (the pasteboard changed meanwhile), or why it failed
	Confirmed bool   // the field's value changed before the pasteboard was put back
	Save      time.Duration
	Paste     time.Duration // SET to the field showing the text
	Restored  time.Duration
	Total     time.Duration
	Trace     []string // the keys path: what the keyboard probes found, with times
}

// LastTypeStats returns how the last Type call went.
func (d *Device) LastTypeStats() TypeStats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.typeStats
}

// Timeouts for typing. The pasteboard daemon answers in milliseconds, but
// reading every representation of an item can stall it: it loads them one
// at a time, and an app can list a type it fails to produce (Notes and
// public.rtf: about 64 s, pymobiledevice3 found). A SET sent meanwhile
// waits for that.
const (
	pbQuick      = 3 * time.Second  // a request that should be quick
	pbFullPull   = 3 * time.Second  // reading every representation
	pbStalled    = 75 * time.Second // a SET queued behind a stalled read
	pasteConfirm = 1500 * time.Millisecond
	pasteBlind   = 500 * time.Millisecond // the wait when the field cannot be read
)

// errPasteUnavailable means the pasteboard service cannot be used; typing
// falls back to keys.
var errPasteUnavailable = errors.New("pasteboard service unavailable")

// Type implements driver.Device: it enters text into the focused field.
// Without Append it first clears the field with Command-A and Delete, as a
// person with a keyboard would. Submit presses Return afterwards.
func (d *Device) Type(ctx context.Context, text string, opt driver.TypeOptions) error {
	return d.gone(d.typeText(ctx, text, opt))
}

// typeText is Type without marking a dropped connection (gone).
func (d *Device) typeText(ctx context.Context, text string, opt driver.TypeOptions) error {
	start := time.Now()
	st := TypeStats{}
	defer func() {
		st.Total = time.Since(start)
		d.mu.Lock()
		d.typeStats = st
		d.mu.Unlock()
	}()
	d.inputMu.Lock()
	defer d.inputMu.Unlock()
	// Choose how to type before touching the field, so text that cannot be
	// typed leaves it as it was.
	var (
		pb     *pbConn
		saved  *pbSnapshot
		keys   []keyStroke
		method = "keys"
	)
	if text != "" {
		var pbErr error
		if pb = d.pasteboard(); pb != nil {
			t0 := time.Now()
			saved, st.Saved, pbErr = savePasteboard(ctx, pb)
			st.Save = time.Since(t0)
			switch {
			case pbErr == nil:
				method = "paste"
			case !errors.Is(pbErr, errPasteUnavailable):
				return pbErr
			}
		}
		if method == "keys" {
			var err error
			if keys, err = keyStrokesFor(text, pbErr); err != nil {
				return err
			}
		}
		st.Method = method
	}
	if !opt.Append {
		if err := d.pressKeys(ctx, []keyStroke{{usage: keyA, mod: keyLeftGUI}, {usage: keyBackspace}}); err != nil {
			return err
		}
	}
	if text != "" {
		var err error
		if method == "paste" {
			err = d.paste(ctx, pb, saved, text, &st)
		} else {
			err = d.typeKeys(ctx, keys, &st)
		}
		if err != nil {
			return err
		}
	}
	if opt.Submit {
		return d.pressKeys(ctx, []keyStroke{{usage: keyEnter}})
	}
	return nil
}

// keyStrokesFor is how Type types text without the pasteboard: as keys,
// which only printable ASCII can be. pbErr is why the pasteboard did not
// do: nil when the phone has no pasteboard service, so text that keys
// cannot type is unsupported; else the service is there and did not answer
// this time, which the error says, so that the caller tries again instead
// of concluding that the text cannot be typed.
func keyStrokesFor(text string, pbErr error) ([]keyStroke, error) {
	keys, err := asciiStrokes(text)
	if err != nil && pbErr != nil {
		return nil, fmt.Errorf("type: the phone's pasteboard did not answer, so nothing was typed; try again: %v", pbErr)
	}
	return keys, err
}

// pasteboard returns the pasteboard connection, or nil when the phone has
// no pasteboard service.
func (d *Device) pasteboard() *pbConn {
	if !d.l.has(pasteboardService) {
		return nil
	}
	d.svcMu.Lock()
	defer d.svcMu.Unlock()
	if d.pb == nil {
		d.pb = newPBConn(d.l.entry)
	}
	return d.pb
}

// paste types text through the pasteboard, whose old contents savePasteboard
// read into saved, and puts them back. Callers hold inputMu.
func (d *Device) paste(ctx context.Context, pb *pbConn, saved *pbSnapshot, text string, st *TypeStats) error {
	field, before := d.focusedValue(ctx)

	t1 := time.Now()
	sctx, cancel := context.WithTimeout(ctx, pbStalled)
	mine, err := pb.set(sctx, textItems(text))
	cancel()
	if err != nil {
		var ce *CoreDeviceError
		if errors.As(err, &ce) {
			return fmt.Errorf("type: the phone refused the text: %w", err) // nothing was written
		}
		// The SET may still land; put the pasteboard back after it.
		st.Restore = restorePasteboard(pb, saved, nil)
		return fmt.Errorf("type: %w", err)
	}
	// From here on the pasteboard is put back whatever happens.
	defer func() {
		t2 := time.Now()
		st.Restore = restorePasteboard(pb, saved, mine)
		st.Restored = time.Since(t2)
	}()
	if err := d.pressKeys(ctx, []keyStroke{{usage: keyV, mod: keyLeftGUI}}); err != nil {
		return err
	}
	// Put the pasteboard back only once the app has read it: restoring
	// first would paste the person's own clipboard into the field.
	st.Confirmed = d.waitValueChange(ctx, field, before)
	st.Paste = time.Since(t1)
	return nil
}

// savePasteboard reads the pasteboard so it can be put back. A pasteboard
// the phone does not let anyone read comes back nil with kind "unreadable";
// it is cleared after pasting rather than left holding the text. Other
// failures mean the service did not answer.
func savePasteboard(ctx context.Context, pb pbPuller) (*pbSnapshot, string, error) {
	pull := func() (*pbSnapshot, error) {
		c, cancel := context.WithTimeout(ctx, pbQuick)
		defer cancel()
		return pb.pull(c, pbPromiseSecondary)
	}
	snap, err := pull()
	if err != nil && !errors.Is(err, errPBRestricted) && ctx.Err() == nil {
		// The phone closes the service's connection after a while idle, and
		// the first request after that fails; the failure dropped it, so
		// this one dials again. A read changes nothing, so it can repeat.
		snap, err = pull()
	}
	switch {
	case errors.Is(err, errPBRestricted):
		return nil, "unreadable", nil
	case err != nil:
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fmt.Errorf("%w: %v", errPasteUnavailable, err)
	case len(snap.Items) == 0:
		return snap, "empty", nil
	case snap.complete():
		return snap, "full", nil
	}
	// Other representations are only promised: read them all, but not for
	// long. If the daemon is stuck on one an app cannot produce, keep the
	// first representation of each item, which it did give.
	c, cancel := context.WithTimeout(ctx, pbFullPull)
	full, err := pb.pull(c, pbAllResolved)
	cancel()
	if err == nil {
		return full, "full", nil
	}
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	return snap, "primary", nil
}

// pbPuller reads the pasteboard; pbConn is one, tests fake it.
type pbPuller interface {
	pull(ctx context.Context, policy string) (*pbSnapshot, error)
}

// restorePasteboard puts saved back (an empty pasteboard when saved is nil)
// unless the pasteboard changed since Pinky's own SET, whose result is
// mine (nil when unknown): then someone copied something newer, which
// wins. It uses its own deadline, so a cancelled Type still restores.
func restorePasteboard(pb *pbConn, saved, mine *pbSnapshot) string {
	ctx, cancel := context.WithTimeout(context.Background(), pbStalled)
	defer cancel()
	if mine != nil && mine.HasCount {
		c, cancel := context.WithTimeout(ctx, pbQuick)
		cur, err := pb.pull(c, pbAllPromised)
		cancel()
		switch {
		case errors.Is(err, errPBRestricted):
			return "left" // an item from another device arrived
		case err == nil && cur.HasCount && cur.ChangeCount != mine.ChangeCount:
			return "left"
		}
	}
	items := []any{}
	what := "cleared"
	if saved != nil && len(saved.Items) > 0 {
		items, what = saved.wireItems(), "restored"
	}
	if _, err := pb.set(ctx, items); err != nil {
		return "failed: " + err.Error()
	}
	return what
}

// focusedValue returns the frontmost app's focused element and its value,
// or nil when remoteAXService cannot tell.
func (d *Device) focusedValue(ctx context.Context) (*axpObject, string) {
	rax, err := d.raxNow()
	if err != nil {
		return nil, ""
	}
	// No deadline of its own: a request cut short marks the session broken,
	// and each request already gives up after raxRequestTimeout.
	c := context.WithoutCancel(ctx)
	el, err := rax.focusedElement(c)
	if err != nil || el == nil {
		return nil, ""
	}
	v, err := rax.stringAttribute(c, el, axpAttrValue)
	if err != nil {
		return nil, ""
	}
	return el, v
}

// waitValueChange waits until field's value differs from before. Without a
// field it waits a fixed time, which a responsive app needs far less of.
func (d *Device) waitValueChange(ctx context.Context, field *axpObject, before string) bool {
	rax, err := d.raxNow()
	if field == nil || err != nil {
		sleepCtx(ctx, pasteBlind)
		return false
	}
	deadline := time.Now().Add(pasteConfirm)
	for time.Now().Before(deadline) {
		if sleepCtx(ctx, 40*time.Millisecond) != nil {
			return false
		}
		v, err := rax.stringAttribute(context.WithoutCancel(ctx), field, axpAttrValue)
		if err != nil {
			sleepCtx(ctx, pasteBlind)
			return false
		}
		if v != before {
			return true
		}
	}
	return false
}

// raxNow returns the remoteAXService session if it is connected, without
// connecting: typing must not wait for a dial.
func (d *Device) raxNow() (*raxSession, error) {
	d.svcMu.Lock()
	defer d.svcMu.Unlock()
	if d.rax != nil && d.rax.alive() {
		return d.rax, nil
	}
	return nil, errRAXTimeout
}

// asciiStrokes turns printable ASCII into key strokes on a US layout.
func asciiStrokes(text string) ([]keyStroke, error) {
	strokes := make([]keyStroke, 0, len(text))
	for _, r := range text {
		u, shift, ok := asciiUsage(r)
		if !ok {
			return nil, fmt.Errorf("character %q: without the pasteboard service only printable ASCII can be typed on iPhone: %w", r, driver.ErrUnsupported)
		}
		ks := keyStroke{usage: u}
		if shift {
			ks.mod = keyLeftShift
		}
		strokes = append(strokes, ks)
	}
	return strokes, nil
}

// typeKeys types strokes on the virtual keyboard, the fallback when the
// phone has no pasteboard service. Keyboard reports carry key codes, not
// text, and the phone's input method turns them into its own text (Pinyin
// composes letters into Chinese), so a non-Latin keyboard is switched to the
// other one with Control-Space for the duration, and switched back after.
// Callers hold inputMu.
func (d *Device) typeKeys(ctx context.Context, strokes []keyStroke, st *TypeStats) error {
	rax, err := d.raxNow()
	if err != nil {
		return d.pressKeys(ctx, strokes) // the keyboard cannot be checked
	}
	t0 := time.Now()
	note := func(format string, args ...any) {
		st.Trace = append(st.Trace, fmt.Sprintf("%v ", time.Since(t0).Round(time.Millisecond))+fmt.Sprintf(format, args...))
	}
	latin, err := d.latinKeyboard(ctx, rax, note)
	if err != nil {
		return err
	}
	if latin {
		return d.pressKeys(ctx, strokes)
	}
	toggle := []keyStroke{{usage: keySpace, mod: keyLeftCtrl}}
	if err := d.pressKeys(ctx, toggle); err != nil {
		return err
	}
	note("switched")
	if err := sleepCtx(ctx, keyboardSwitch); err != nil {
		return err
	}
	latin, err = d.latinKeyboard(ctx, rax, note)
	if err == nil && latin {
		err = d.pressKeys(ctx, strokes)
		note("typed")
	} else if err == nil {
		err = fmt.Errorf("the active keyboard composes text and Control-Space did not switch to a Latin one: %w", driver.ErrUnsupported)
	}
	// Switch back whatever happened: the keyboard is the person's. Let the
	// Latin keyboard finish with the last word first.
	bctx := context.WithoutCancel(ctx)
	sleepCtx(bctx, keyboardSettle)
	if _, v := d.focusedValue(bctx); v != "" {
		note("before switching back: %d characters", len([]rune(v)))
	}
	if berr := d.pressKeys(bctx, toggle); err == nil {
		err = berr
	}
	note("switched back")
	sleepCtx(bctx, keyboardSwitch)
	if _, v := d.focusedValue(bctx); v != "" {
		note("after switching back: %d characters", len([]rune(v)))
	}
	return err
}

// keyboardSwitch is how long iOS takes to change keyboards; keyboardSettle
// how long a keyboard takes with the last keys typed.
const (
	keyboardSwitch = 700 * time.Millisecond
	keyboardSettle = 300 * time.Millisecond
	candidateWait  = time.Second // for an input method's candidate bar
)

// latinKeyboard types a probe letter into the focused field and reports
// whether it arrived as itself (a Latin keyboard) rather than as an input
// method's composition, which shows a candidate bar in the app's own tree
// (Pinyin: cells whose identifier is the candidate), sometimes only after a
// few hundred milliseconds. The probe is deleted again either way. Callers
// hold inputMu.
func (d *Device) latinKeyboard(ctx context.Context, rax *raxSession, note func(string, ...any)) (bool, error) {
	if err := d.pressKeys(ctx, []keyStroke{{usage: keyA}}); err != nil {
		return false, err
	}
	defer d.pressKeys(context.WithoutCancel(ctx), []keyStroke{{usage: keyBackspace}})
	start := time.Now()
	for {
		if err := sleepCtx(ctx, 150*time.Millisecond); err != nil {
			return false, err
		}
		field, value := d.focusedValue(ctx)
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), raxLimits.Budget+raxRequestTimeout)
		els, _, err := rax.walk(wctx, raxLimits)
		cancel()
		if err != nil {
			return false, err
		}
		cands := hasCandidates(els)
		note("probe: field %v, arrived %v, candidates %v", field != nil, probeArrived(value), cands)
		if cands || (field != nil && !probeArrived(value)) {
			return false, nil
		}
		if time.Since(start) >= candidateWait {
			return true, nil
		}
	}
}

// probeArrived reports whether the field's value ends with the probe letter
// as typed, or capitalized by the keyboard's auto-capitalization.
func probeArrived(value string) bool {
	return strings.HasSuffix(value, "a") || strings.HasSuffix(value, "A")
}

// hasCandidates reports whether an input method's candidate bar is on
// screen: cells or buttons identified by a short non-Latin candidate that
// starts their label ("啊, 叹词的啊", identifier "啊").
func hasCandidates(els []raxElement) bool {
	for _, e := range els {
		id := []rune(e.Ident)
		if len(id) == 0 || len(id) > 8 || id[0] < 0x80 {
			continue
		}
		if strings.HasPrefix(e.Label, e.Ident) {
			return true
		}
	}
	return false
}
