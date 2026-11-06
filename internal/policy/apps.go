package policy

import (
	_ "embed"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/DIYgod/pinky/internal/screen"
)

//go:embed apps.yaml
var bundledAppsYAML []byte

// App categories in the bundled table.
const (
	CatFinance   = "finance"
	CatPasswords = "passwords"
	CatMessaging = "messaging"
	CatSocial    = "social"
	CatStores    = "stores"
	CatBrowsers  = "browsers"
	CatPhone     = "phone"
	CatSettings  = "settings"
	// CatSystem holds launchers and system UI. They are inside every app
	// scope and allow-list, so agents can always navigate.
	CatSystem = "system"
)

// CategoryEntries lists the apps of one category.
type CategoryEntries struct {
	// IDs are package names or bundle ids. A trailing "*" matches a prefix,
	// such as "com.chase.*".
	IDs []string `yaml:"ids"`
	// Names are display names, matched without case, for backends that
	// cannot read the foreground app's id.
	Names []string `yaml:"names"`
}

// AppOverrides is the format of apps.yaml in the config directory: apps to
// add to categories, and ids or names to take out of the bundled table.
type AppOverrides struct {
	Add     map[string]CategoryEntries `yaml:"add"`
	Exclude []string                   `yaml:"exclude"`
}

// Apps maps apps to categories.
type Apps struct {
	ids      map[string][]string // exact id -> categories
	prefixes map[string][]string // id prefix -> categories
	names    map[string][]string // lower-case name -> categories
}

// BundledApps returns the table that ships with Pinky.
func BundledApps() *Apps {
	var m map[string]CategoryEntries
	if err := yaml.Unmarshal(bundledAppsYAML, &m); err != nil {
		panic("policy: bundled apps.yaml: " + err.Error())
	}
	a := &Apps{ids: map[string][]string{}, prefixes: map[string][]string{}, names: map[string][]string{}}
	for cat, e := range m {
		a.add(cat, e)
	}
	return a
}

func (a *Apps) add(cat string, e CategoryEntries) {
	for _, id := range e.IDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if p, ok := strings.CutSuffix(id, "*"); ok {
			a.prefixes[p] = appendUnique(a.prefixes[p], cat)
		} else {
			a.ids[id] = appendUnique(a.ids[id], cat)
		}
	}
	for _, n := range e.Names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" {
			a.names[n] = appendUnique(a.names[n], cat)
		}
	}
}

// Add puts apps into a category.
func (a *Apps) Add(cat string, e CategoryEntries) { a.add(cat, e) }

// Apply adds and removes entries.
func (a *Apps) Apply(o AppOverrides) {
	for cat, e := range o.Add {
		a.add(cat, e)
	}
	for _, x := range o.Exclude {
		x = strings.TrimSpace(x)
		if p, ok := strings.CutSuffix(x, "*"); ok {
			delete(a.prefixes, p)
			continue
		}
		delete(a.ids, x)
		delete(a.names, strings.ToLower(x))
	}
}

// Categories returns the categories of an app, sorted.
func (a *Apps) Categories(app screen.App) []string {
	var out []string
	if app.ID != "" {
		out = append(out, a.ids[app.ID]...)
		for p, cats := range a.prefixes {
			if strings.HasPrefix(app.ID, p) {
				for _, c := range cats {
					out = appendUnique(out, c)
				}
			}
		}
	}
	if app.Name != "" {
		for _, c := range a.names[strings.ToLower(strings.TrimSpace(app.Name))] {
			out = appendUnique(out, c)
		}
	}
	sort.Strings(out)
	return out
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

var appsTemplate = []byte(`# App categories for Pinky's rules.
#
# Pinky ships a table of apps by category (finance, passwords, messaging,
# social, stores, browsers, phone, settings, system). This file adds to it
# and takes entries out; the bundled table keeps getting updates.
#
# add:
#   finance:
#     ids: [com.example.mybank]      # package name or bundle id; "com.bank.*" matches a prefix
#     names: [My Bank]               # display name, for backends without ids
#   messaging:
#     ids: [com.example.chat]
# exclude:
#   - com.paypal.android.p2pmobile   # take an id or name out of every category

add: {}
exclude: []
`)
