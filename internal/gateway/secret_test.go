package gateway

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

// The password and code these tests type: distinct enough that any copy
// of them, anywhere, is a leak.
const (
	leakPassword = "Hunter2-zq9!"
	leakCode     = "804217"
)

// TestTypedSecretsAreNeverKept types a password and a code on a phone
// (refused, then asked and allowed) and on a virtual phone (allowed), by
// ref and without, taps an app's hidden passcode keypad, and fails a type
// by a stale ref; then looks for the text everywhere Pinky keeps or shows
// it: the live steps, the cards and their details, history's steps (words,
// arguments, intents, screen text) and approvals, an export of it, and
// the log. Only the agent's own results may hold it. An ordinary field
// keeps what was typed into it.
func TestTypedSecretsAreNeverKept(t *testing.T) {
	ctx := context.Background()
	var logs bytes.Buffer
	var typedCode atomic.Bool
	g, hd := newHookGateway(t, func(o *Options) {
		o.Policy = loadPolicy(t, t.TempDir())
		o.DataDir = t.TempDir()
		o.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	hd.setObserve(func(sc *screen.Screen) {
		if sc.App.ID != demo.Dinely {
			return
		}
		for i, l := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "FaceID", "0", "Delete"} {
			sc.Elements = append(sc.Elements, screen.Element{Role: screen.RoleButton, Label: l, Source: screen.SourceTree,
				States: []string{screen.StateNotInScreenshot}, Bounds: screen.Rect{X: 100 + i%3*250, Y: 1500 + i/3*100, W: 250, H: 100}})
		}
		code := screen.Element{Role: screen.RoleInput, Label: "Verification code", Source: screen.SourceTree,
			Bounds: screen.Rect{X: 100, Y: 1150, W: 800, H: 120}}
		if typedCode.Load() && typed(hd, leakCode) {
			// The field shows the code once it is typed, as Android's do.
			code.Label, code.Content = "Verification code, "+leakCode, leakCode
		}
		sc.Elements = append(sc.Elements,
			screen.Element{Role: screen.RoleInput, Label: "Email", Source: screen.SourceTree, Bounds: screen.Rect{X: 100, Y: 1000, W: 800, H: 120}},
			code,
			screen.Element{Role: screen.RoleInput, Label: "Password", Source: screen.SourceTree,
				States: []string{screen.StatePassword}, Bounds: screen.Rect{X: 100, Y: 1300, W: 800, H: 120}})
	})
	pol := g.Policy()
	// With Hide codes off, nothing else masks the code in the field: the
	// masking here stands on its own.
	if err := pol.SetHideCodes(false); err != nil {
		t.Fatal(err)
	}
	s, _ := connect(t, g, "Sign in to the test account")
	if res := s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}); res.IsError() {
		t.Fatalf("launch: %+v", res)
	}
	typeInto := func(label, text string) *Result {
		obs := s.Observe(ctx, ObserveRequest{})
		return s.Act(ctx, Call{Intent: "Sign in with " + text}, Action{Tool: "type", Ref: ref(t, obs, label), Text: text})
	}

	// Refused: the switch is off.
	if res := typeInto("Password", leakPassword); res.Status != StatusBlocked {
		t.Fatalf("typed with the switch off: %+v", res)
	}
	// Refused: an app's hidden passcode keypad.
	obs := s.Observe(ctx, ObserveRequest{})
	if res := tap(s, ref(t, obs, "8")); res.Status != StatusBlocked {
		t.Fatalf("tapped the hidden keypad: %+v", res)
	}
	// Failed: a ref that is gone, on a screen with a password field.
	s.Observe(ctx, ObserveRequest{})
	if res := s.Act(ctx, Call{}, Action{Tool: "type", Ref: 9999, Text: leakPassword}); !res.IsError() {
		t.Fatalf("typed by a stale ref: %+v", res)
	}

	// Asked: the card and its detail show neither the text nor the intent's
	// copy of it.
	if err := pol.SetPhoneCredentials(true); err != nil {
		t.Fatal(err)
	}
	answer(t, g, AllowOnce, func(n NeedView) {
		if strings.Contains(n.Title+n.Detail, leakPassword) || !strings.Contains(n.Detail, secretMask) {
			t.Errorf("the card shows the password: %q · %q", n.Title, n.Detail)
		}
	})
	if res := typeInto("Password", leakPassword); res.IsError() || res.Status == StatusBlocked {
		t.Fatalf("allowed typing: %+v", res)
	}
	answer(t, g, AllowOnce, nil)
	typedCode.Store(true) // once the code is typed (typed)
	res := typeInto("Verification code", leakCode)
	if res.IsError() || res.Status == StatusBlocked {
		t.Fatalf("allowed code: %+v", res)
	}
	// The agent's own result keeps the field as it reads: the agent gave
	// the code, and checks the field with it.
	if !strings.Contains(res.Screen.Render(), leakCode) {
		t.Errorf("the agent's result lost the field's text:\n%s", res.Screen.Render())
	}
	typedCode.Store(false)

	// An ordinary field keeps its text.
	if res := typeInto("Email", "sam@example.com"); res.IsError() {
		t.Fatalf("email: %+v", res)
	}

	// A virtual phone: let through, by ref and without.
	if err := pol.SetVirtualCredentials(true); err != nil {
		t.Fatal(err)
	}
	hd.SetVirtual(true)
	for deadline := time.Now().Add(3 * time.Second); !g.Devices()[0].Info.Virtual; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the phone did not become virtual")
		}
	}
	if res := typeInto("Password", leakPassword); res.Status == StatusBlocked || res.Status == StatusPending {
		t.Fatalf("a virtual phone: %+v", res)
	}
	s.Observe(ctx, ObserveRequest{})
	if res := s.Act(ctx, Call{}, Action{Tool: "type", Text: leakPassword}); res.Status == StatusBlocked || res.Status == StatusPending {
		t.Fatalf("a virtual phone, without ref: %+v", res)
	}
	obs = s.Observe(ctx, ObserveRequest{})
	if res := tap(s, ref(t, obs, "8")); res.Status == StatusBlocked {
		t.Fatalf("a virtual phone's hidden keypad: %+v", res)
	}

	leaks := func(where, s string) {
		t.Helper()
		for _, secret := range []string{leakPassword, leakCode} {
			if strings.Contains(s, secret) {
				t.Errorf("%s holds %q:\n%s", where, secret, s)
			}
		}
	}

	// The live steps, as Activity and the phone's page show them (ui's
	// gwtext tests translate the masked words).
	steps := taskSteps(g, s)
	var masked, email, keys int
	for _, st := range steps {
		leaks("live step "+st.Tool, st.Text+"\n"+st.Args)
		if strings.Contains(st.Text, "Typed “"+secretMask+"”") {
			masked++
		}
		if strings.Contains(st.Text, "Typed “sam@example.com” into “Email”") {
			email++
		}
		if st.Tool == "phone_tap" && strings.Contains(st.Text, "“"+secretKey+"”") {
			keys++
			if !st.Target.Empty() || st.Point != (screen.Point{}) || strings.Contains(st.Args, `"ref"`) || strings.Contains(st.Args, `"x"`) {
				t.Errorf("a keypad tap says where it went: %+v", st)
			}
		}
		if st.Tool == "phone_tap" && strings.Contains(st.Text, "“8”") {
			t.Errorf("a keypad tap names its key: %+v", st)
		}
	}
	if masked < 6 || email != 1 || keys != 2 {
		t.Errorf("masked %d, email %d, keys %d:\n%+v", masked, email, keys, steps)
	}

	// History: steps, approvals, and an export.
	s.Release(ctx, ReleaseRequest{})
	g.Flush()
	tasks, err := g.History(ctx, HistoryQuery{})
	if err != nil || len(tasks) == 0 {
		t.Fatalf("history: %v %v", tasks, err)
	}
	for _, task := range tasks {
		stored, err := g.store.Steps(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range stored {
			leaks("history step "+st.Tool, strings.Join([]string{st.Text, st.Args, st.Intent, st.ScreenText}, "\n"))
		}
		aps, err := g.store.Approvals(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(aps) < 2 {
			t.Errorf("approvals %+v", aps)
		}
		for _, a := range aps {
			leaks("history approval", a.Title+"\n"+a.Note)
		}
	}
	dir := t.TempDir()
	if _, err := g.ExportHistory(ctx, nil, dir); err != nil {
		t.Fatal(err)
	}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(path)
			leaks("export "+filepath.Base(path), string(b))
		}
		return nil
	})
	leaks("the log", logs.String())
}

// TestHideTyped: history's screen text shows a code or password field's
// content as the mask, and its screenshots cover such fields; other fields
// keep their text.
func TestHideTyped(t *testing.T) {
	s := &screen.Screen{Width: 100, Height: 200, Elements: []screen.Element{
		{Ref: 1, Role: screen.RoleInput, Label: "Verification code, " + leakCode, Content: leakCode, Bounds: screen.Rect{X: 10, Y: 50, W: 80, H: 20}},
		{Ref: 2, Role: screen.RoleInput, Label: "sam@example.com", Content: "sam@example.com", Bounds: screen.Rect{X: 10, Y: 100, W: 80, H: 20}},
		{Ref: 3, Role: screen.RoleInput, Label: "Password", Bounds: screen.Rect{X: 10, Y: 150, W: 80, H: 20}, States: []string{screen.StatePassword}},
	}}
	got := body(hideTyped(s))
	if strings.Contains(got, leakCode) || !strings.Contains(got, `"Verification code, `+secretMask+`"`) || !strings.Contains(got, "sam@example.com") {
		t.Fatalf("history's screen text:\n%s", got)
	}
	if !strings.Contains(body(s), leakCode) {
		t.Fatal("hideTyped changed the screen it was given")
	}

	g, _ := newTestGateway(t, func(o *Options) { o.DataDir = t.TempDir(); o.NoScreenshots = false })
	white := image.NewRGBA(image.Rect(0, 0, 100, 200))
	draw.Draw(white, white.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	p := &bgShot{img: white, state: shotKept, reads: []*screen.Screen{s}}
	g.mu.Lock()
	id := g.writeShotLocked(p)
	g.mu.Unlock()
	g.Flush()
	if id == "" {
		t.Fatal("not kept")
	}
	path, err := g.store.MediaPath(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	gray := func(x, y int) bool {
		r, gg, b, _ := img.At(x, y).RGBA()
		return r>>8 < 200 && gg>>8 < 200 && b>>8 < 200
	}
	if !gray(50, 60) {
		t.Error("the code field is not covered")
	}
	if gray(50, 110) || gray(50, 160) {
		t.Error("covered a field with no secret text in it")
	}
}

// TestBatchIntentIsMasked runs batches whose intent names what a step
// types. Every step keeps and shows the intent without the password: the
// tap before the type and the one after it, a step's own intent, and a
// batch that reaches the password field only on its next screen, typing
// there without ref. An email typed by ref into an ordinary field stays in
// the intent.
func TestBatchIntentIsMasked(t *testing.T) {
	ctx := context.Background()
	g, hd := newHookGateway(t, func(o *Options) {
		o.Policy = loadPolicy(t, t.TempDir())
		o.DataDir = t.TempDir()
	})
	var nextScreen atomic.Int64 // taps after which the password page shows; 0 for the sign-in page
	taps := func() int64 {
		var n int64
		for _, in := range hd.Inputs() {
			if in.Kind == "tap" {
				n++
			}
		}
		return n
	}
	hd.setObserve(func(sc *screen.Screen) {
		if sc.App.ID != demo.Dinely {
			return
		}
		if n := nextScreen.Load(); n > 0 {
			// The email page, then, after a tap on Next, the password page
			// with its field focused.
			if taps() < n {
				sc.Elements = append(sc.Elements,
					screen.Element{Role: screen.RoleButton, Label: "Next", Source: screen.SourceTree, Bounds: screen.Rect{X: 100, Y: 1300, W: 800, H: 120}})
				return
			}
			sc.Elements = append(sc.Elements,
				screen.Element{Role: screen.RoleInput, Label: "Password", Source: screen.SourceTree,
					States: []string{screen.StatePassword, screen.StateFocused}, Bounds: screen.Rect{X: 100, Y: 1300, W: 800, H: 120}},
				screen.Element{Role: screen.RoleButton, Label: "Sign in", Source: screen.SourceTree, Bounds: screen.Rect{X: 100, Y: 1500, W: 800, H: 120}})
			return
		}
		sc.Elements = append(sc.Elements,
			screen.Element{Role: screen.RoleInput, Label: "Email", Source: screen.SourceTree, Bounds: screen.Rect{X: 100, Y: 1000, W: 800, H: 120}},
			screen.Element{Role: screen.RoleInput, Label: "Password", Source: screen.SourceTree,
				States: []string{screen.StatePassword}, Bounds: screen.Rect{X: 100, Y: 1300, W: 800, H: 120}})
	})
	if err := g.Policy().SetVirtualCredentials(true); err != nil {
		t.Fatal(err)
	}
	hd.SetVirtual(true)
	for deadline := time.Now().Add(3 * time.Second); !g.Devices()[0].Info.Virtual; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the phone did not become virtual")
		}
	}
	s, _ := connect(t, g, "Sign in to the test account")
	if res := s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}); res.IsError() {
		t.Fatalf("launch: %+v", res)
	}
	batch := func(intent string, steps ...Action) {
		t.Helper()
		if res := s.Batch(ctx, BatchRequest{Call: Call{Intent: intent}, Steps: steps}); res.IsError() || len(res.Steps) != len(steps) {
			t.Fatalf("batch %q: %+v", intent, res)
		}
	}
	const (
		withPassword = "Sign in with " + leakPassword
		masked       = "Sign in with " + secretMask
		mine         = "Sign in as sam@example.com"
	)
	obs := s.Observe(ctx, ObserveRequest{})
	pw, email := ref(t, obs, "Password"), ref(t, obs, "Email")
	batch(withPassword, Action{Tool: "tap", Ref: pw}, Action{Tool: "type", Ref: pw, Text: leakPassword},
		Action{Tool: "tap", Ref: email, Intent: "Done with " + leakPassword})
	batch(mine, Action{Tool: "type", Ref: email, Text: "sam@example.com"}, Action{Tool: "tap", Ref: pw})
	// The password field is on the next screen, which the agent has not read.
	nextScreen.Store(taps() + 1)
	obs = s.Observe(ctx, ObserveRequest{})
	batch(withPassword, Action{Tool: "tap", Ref: ref(t, obs, "Next")}, Action{Tool: "type", Text: leakPassword},
		Action{Tool: "key", Key: "enter"})

	want := []string{masked, masked, "Done with " + secretMask, mine, mine, masked, masked, masked}
	check := func(where string, intents []string) {
		t.Helper()
		if len(intents) != len(want) {
			t.Fatalf("%s: intents %q", where, intents)
		}
		for i := range want {
			if intents[i] != want[i] {
				t.Errorf("%s: step %d's intent %q, want %q", where, i+1, intents[i], want[i])
			}
		}
	}
	var live []string
	for _, st := range taskSteps(g, s) {
		if strings.Contains(st.Text+st.Args, leakPassword) {
			t.Errorf("live step %s: %q · %s", st.Tool, st.Text, st.Args)
		}
		if i := strings.Index(st.Args, `"intent":"`); i >= 0 {
			rest := st.Args[i+len(`"intent":"`):]
			live = append(live, rest[:strings.Index(rest, `"`)])
		}
	}
	check("live", live)
	s.Release(ctx, ReleaseRequest{})
	g.Flush()
	tasks, err := g.History(ctx, HistoryQuery{})
	if err != nil || len(tasks) == 0 {
		t.Fatalf("history: %v %v", tasks, err)
	}
	var stored []string
	for _, task := range tasks {
		steps, err := g.store.Steps(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range steps {
			if strings.Contains(strings.Join([]string{st.Text, st.Args, st.Intent, st.ScreenText}, "\n"), leakPassword) {
				t.Errorf("history step %s: intent %q, args %s", st.Tool, st.Intent, st.Args)
			}
			if st.Intent != "" {
				stored = append(stored, st.Intent)
			}
		}
	}
	check("history", stored)
}

// typed reports whether the phone was given text to type.
func typed(hd *hookDev, text string) bool {
	for _, in := range hd.Inputs() {
		if in.Kind == "type" && in.Text == text {
			return true
		}
	}
	return false
}

// TestTypedCodeFieldIsNamedMasked types a code on a virtual phone, then
// taps the field, which now names the code in its label ("Verification
// code, 804217", as iOS reads it), once without asking and once with every
// action asking: the steps, the card and history name the field with the
// mask, not the code.
func TestTypedCodeFieldIsNamedMasked(t *testing.T) {
	ctx := context.Background()
	g, hd := newHookGateway(t, func(o *Options) {
		o.Policy = loadPolicy(t, t.TempDir())
		o.DataDir = t.TempDir()
	})
	hd.setObserve(func(sc *screen.Screen) {
		if sc.App.ID != demo.Dinely {
			return
		}
		code := screen.Element{Role: screen.RoleInput, Label: "Verification code", Source: screen.SourceTree,
			Bounds: screen.Rect{X: 100, Y: 1150, W: 800, H: 120}}
		if typed(hd, leakCode) {
			code.Label, code.Content = "Verification code, "+leakCode, leakCode
		}
		sc.Elements = append(sc.Elements, code)
	})
	pol := g.Policy()
	if err := pol.SetHideCodes(false); err != nil {
		t.Fatal(err)
	}
	if err := pol.SetVirtualCredentials(true); err != nil {
		t.Fatal(err)
	}
	hd.SetVirtual(true)
	for deadline := time.Now().Add(3 * time.Second); !g.Devices()[0].Info.Virtual; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the phone did not become virtual")
		}
	}
	s, _ := connect(t, g, "Sign in to the test account")
	if res := s.Act(ctx, Call{}, Action{Tool: "launch", App: "Dinely"}); res.IsError() {
		t.Fatalf("launch: %+v", res)
	}
	obs := s.Observe(ctx, ObserveRequest{})
	if res := s.Act(ctx, Call{}, Action{Tool: "type", Ref: ref(t, obs, "Verification code"), Text: leakCode}); res.IsError() || res.Status == StatusBlocked {
		t.Fatalf("code: %+v", res)
	}
	field := "Verification code, " + secretMask
	obs = s.Observe(ctx, ObserveRequest{})
	// The agent quotes the field as it read it; its own result keeps it so.
	quoted := Call{Intent: "Tap Verification code, " + leakCode}
	res := s.Act(ctx, quoted, Action{Tool: "tap", Ref: ref(t, obs, "Verification code, "+leakCode)})
	if res.IsError() {
		t.Fatalf("tap: %+v", res)
	}
	if !strings.Contains(res.Screen.Render(), leakCode) {
		t.Errorf("the agent's result lost the field's text:\n%s", res.Screen.Render())
	}
	// With every action asking, the card names the field too.
	if err := pol.SetPreset(policy.PresetEvery); err != nil {
		t.Fatal(err)
	}
	var card atomic.Value
	answer(t, g, AllowOnce, func(n NeedView) { card.Store(n) })
	obs = s.Observe(ctx, ObserveRequest{})
	if res := s.Act(ctx, quoted, Action{Tool: "tap", Ref: ref(t, obs, "Verification code, "+leakCode)}); res.IsError() || res.Status == StatusBlocked {
		t.Fatalf("asked tap: %+v", res)
	}
	if n, _ := card.Load().(NeedView); n.Title != "Tap “"+field+"”?" || strings.Contains(n.Detail, leakCode) || !strings.Contains(n.Detail, "Tap Verification code, "+secretMask) {
		t.Errorf("the card: %q · %q", n.Title, n.Detail)
	}

	var taps int
	for _, st := range taskSteps(g, s) {
		if strings.Contains(st.Text+st.Args, leakCode) {
			t.Errorf("live step %s: %q", st.Tool, st.Text)
		}
		if st.Tool == "phone_tap" && strings.Contains(st.Text, "Tapped “"+field+"”") {
			taps++
		}
	}
	if taps != 2 {
		t.Errorf("%d taps name the field with the mask:\n%+v", taps, taskSteps(g, s))
	}
	s.Release(ctx, ReleaseRequest{})
	g.Flush()
	tasks, err := g.History(ctx, HistoryQuery{})
	if err != nil || len(tasks) == 0 {
		t.Fatalf("history: %v %v", tasks, err)
	}
	for _, task := range tasks {
		stored, err := g.store.Steps(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range stored {
			if strings.Contains(strings.Join([]string{st.Text, st.Args, st.Intent, st.ScreenText}, "\n"), leakCode) {
				t.Errorf("history step %s: %q", st.Tool, st.Text)
			}
		}
		aps, err := g.store.Approvals(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(aps) != 1 {
			t.Errorf("approvals %+v", aps)
		}
		for _, a := range aps {
			if strings.Contains(a.Title+a.Note, leakCode) || !strings.Contains(a.Title, field) {
				t.Errorf("history approval: %q", a.Title)
			}
		}
	}
}
