package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/ui/demogw"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// handEdit saves a rules file as most editors do: to a new file renamed
// over the old one.
func handEdit(t *testing.T, path, data string) {
	t.Helper()
	tmp := path + ".edit"
	if err := os.WriteFile(tmp, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// TestGatewayHandEditedRules edits rules.yaml by hand while Settings ›
// Safety shows, against the real gateway: the list shows the edit; Save in
// a form opened before another edit is refused, and saves on top of it the
// second time; an edit that does not parse shows a note and turns off what
// would change the file, until it is fixed.
func TestGatewayHandEditedRules(t *testing.T) {
	dir := t.TempDir()
	rules := filepath.Join(dir, "rules.yaml")
	ga := newGatewayApp(t, true, func(o *demogw.Options) { o.ConfigDir = dir })
	g := ga.w.Gateway
	ga.page, ga.set.sec = pageSettings, secSafety
	tt := mg.NewTester(ga.view, 1280, 2600)
	ga.waitFor(t, "the rules", func() bool { tt.Frame(); return ga.rules.loaded })
	read := func() string {
		t.Helper()
		data, err := os.ReadFile(rules)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	// Settings reads again at once rather than in a few seconds.
	reload := func() { ga.rules.lastLoad, ga.files.lastLoad = time.Time{}, time.Time{} }

	// A rule added by hand shows in the list.
	handEdit(t, rules, strings.Replace(read(), "\nrules:\n",
		"\nrules:\n  - name: Chatter asks\n    match: { app: Chatter, action: commit }\n    decision: ask\n", 1))
	reload()
	ga.waitFor(t, "the rule added by hand", func() bool { tt.Frame(); return hasText(tt, "Chatter asks") })

	// The form, opened before another edit: Save is refused, and the form
	// stays on its rule, to save again on top of the edit.
	if err := tt.Click("Change Chatter asks"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	ga.rules.form.labels = "delete"
	handEdit(t, rules, strings.Replace(read(), "name: Password managers", "name: Password apps", 1))
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	ga.waitFor(t, "the refusal", func() bool { tt.Frame(); return hasText(tt, locales.SettingsRulesErrChanged()) })
	ga.waitFor(t, "the file's rules", func() bool {
		return slices.ContainsFunc(ga.rules.view.File, func(r gateway.RuleView) bool { return r.Name == "Password apps" })
	})
	if !ga.rules.editing || ga.rules.view.File[ga.rules.edit].Name != "Chatter asks" {
		t.Fatalf("the form after the refusal: editing %v on %d of %+v", ga.rules.editing, ga.rules.edit, ga.rules.view.File)
	}
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	ga.waitFor(t, "the change", func() bool {
		tt.Frame()
		f := g.Rules().File
		return !ga.rules.editing && len(f) == 3 && f[0].Name == "Chatter asks" && slices.Equal(f[0].Labels, []string{"delete"}) &&
			f[2].Name == "Password apps"
	})

	// An edit that does not parse: a note says what is wrong, here and in
	// Advanced, and what would change rules.yaml is off.
	broken := read() + "  - name: [Chatter\n"
	handEdit(t, rules, broken)
	reload()
	title := locales.SettingsFileProblemTitle("rules.yaml")
	ga.waitFor(t, "the note", func() bool { tt.Frame(); return hasText(tt, title) })
	for _, want := range []string{locales.SettingsFileProblemDesc(), "What's wrong: yaml: ", "Edit rules.yaml"} {
		if !hasText(tt, want) {
			t.Errorf("no %q in the note: %q", want, tt.Texts())
		}
	}
	if _, ok := tt.Find(locales.SettingsNeedsYou()); !ok {
		t.Error("the Safety tab does not say it needs the person")
	}
	preset := ga.settings.Preset
	for _, label := range []string{locales.SettingsSafetyPresetPayments(), "Add rule", "Change Chatter asks", locales.SettingsSafetyReadOnly()} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	if ga.settings.Preset != preset || ga.rules.editing || ga.settings.ReadOnly {
		t.Fatalf("Settings changed rules.yaml's settings: preset %d, form open %v, read-only %v",
			ga.settings.Preset, ga.rules.editing, ga.settings.ReadOnly)
	}
	// Blocked apps are in apps.yaml, which still works.
	if err := tt.Click("Add app"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !ga.set.adding {
		t.Error("Add app is off while only rules.yaml is broken")
	}
	ga.set.adding = false
	ga.set.sec = secAdvanced
	tt.Frame()
	if !hasText(tt, title) {
		t.Errorf("no note in Advanced: %q", tt.Texts())
	}
	ga.set.sec = secSafety
	if read() != broken {
		t.Fatalf("the broken rules.yaml was written over:\n%s", read())
	}

	// Fixed, the note goes, and Settings changes the file again.
	handEdit(t, rules, strings.TrimSuffix(broken, "  - name: [Chatter\n"))
	reload()
	ga.waitFor(t, "the note to go", func() bool { tt.Frame(); return !hasText(tt, title) })
	if err := tt.Click("Add rule"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !ga.rules.editing {
		t.Fatal("Add rule is off after the fix")
	}
}

// TestAppsFileProblem turns off only the blocked apps while apps.yaml
// cannot be used.
func TestAppsFileProblem(t *testing.T) {
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.api.SetFileProblems([]gateway.FileProblem{{File: "apps.yaml", Path: "/config/apps.yaml",
		Error: "yaml: line 2: did not find expected node content"}})
	ta.page, ta.set.sec = pageSettings, secSafety
	tt := mg.NewTester(ta.view, 1280, 2600)
	title := locales.SettingsFileProblemTitle("apps.yaml")
	ta.waitFor(t, "the note", func() bool { tt.Frame(); return hasText(tt, title) })
	if !hasText(tt, "What's wrong: yaml: line 2: did not find expected node content") {
		t.Errorf("the note: %q", tt.Texts())
	}
	if err := tt.Click("Add app"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ta.set.adding {
		t.Error("Add app works while apps.yaml is broken")
	}
	if err := tt.Click(locales.SettingsSafetyPresetPayments()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ta.settings.Preset != 2 {
		t.Errorf("the preset did not change while only apps.yaml is broken: %d", ta.settings.Preset)
	}
}
