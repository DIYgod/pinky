package policy

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

//go:embed defaults.yaml
var defaultRulesYAML []byte

// File is the rules file, rules.yaml in the config directory.
type File struct {
	// ReadOnly turns on read-only mode for every device.
	ReadOnly bool `yaml:"read_only"`
	// ReadOnlyDevices turns on read-only mode for these device ids.
	ReadOnlyDevices []string `yaml:"read_only_devices"`
	// MaskCodes masks digit codes in notifications. Default true.
	MaskCodes *bool  `yaml:"mask_codes"`
	Rules     []Rule `yaml:"rules"`
}

// DefaultFile returns the bundled default rules.
func DefaultFile() File {
	f, err := ParseFile(defaultRulesYAML)
	if err != nil {
		panic("policy: bundled defaults.yaml: " + err.Error())
	}
	return f
}

// DefaultRulesYAML returns the bundled rules file, with its comments.
func DefaultRulesYAML() []byte { return append([]byte(nil), defaultRulesYAML...) }

// ParseFile parses a rules file and checks it.
func ParseFile(data []byte) (File, error) {
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return File{}, err
	}
	for i, r := range f.Rules {
		if r.Name == "" {
			return File{}, fmt.Errorf("rule %d has no name", i+1)
		}
		m := r.Match
		if len(m.App) == 0 && len(m.Category) == 0 && len(m.Action) == 0 && len(m.Label) == 0 {
			return File{}, fmt.Errorf("rule %q matches everything; give it an app, category, action or label", r.Name)
		}
		for _, a := range m.Action {
			if !knownAction(a) {
				return File{}, fmt.Errorf("rule %q: unknown action %q", r.Name, a)
			}
		}
	}
	return f, nil
}

func knownAction(a string) bool {
	switch Kind(a) {
	case Commit, Payment, SendMessage, InstallApp, OpenURL, Call, ChangeSetting:
		return true
	}
	switch Op(a) {
	case OpTap, OpType, OpSwipe, OpKey, OpLaunch:
		return true
	}
	return false
}

// Load reads the rules file and the app table overrides from the given
// paths. A missing rules file is created with the bundled defaults, and a
// missing app file with a commented template, so the person can edit both.
// Empty paths use the bundled defaults without touching the disk.
func Load(rulesPath, appsPath string) (*Engine, error) {
	f := DefaultFile()
	if rulesPath != "" {
		data, err := os.ReadFile(rulesPath)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := writeNew(rulesPath, defaultRulesYAML); err != nil {
				return nil, err
			}
		case err != nil:
			return nil, err
		default:
			if f, err = ParseFile(data); err != nil {
				return nil, fmt.Errorf("%s: %w", rulesPath, err)
			}
		}
	}
	apps := BundledApps()
	if appsPath != "" {
		data, err := os.ReadFile(appsPath)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := writeNew(appsPath, appsTemplate); err != nil {
				return nil, err
			}
		case err != nil:
			return nil, err
		default:
			var o AppOverrides
			if err := yaml.Unmarshal(data, &o); err != nil {
				return nil, fmt.Errorf("%s: %w", appsPath, err)
			}
			apps.Apply(o)
		}
	}
	e := New(f, apps)
	e.rulesPath, e.appsPath = rulesPath, appsPath
	return e, nil
}

// Reload reads the files again, keeping runtime read-only switches.
func (e *Engine) Reload() error {
	n, err := Load(e.rulesPath, e.appsPath)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.file, e.apps = n.file, n.apps
	return nil
}

func writeNew(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// UnmarshalYAML accepts "allow", "ask" or "deny".
func (d *Decision) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := ParseDecision(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// MarshalYAML writes the decision as a word.
func (d Decision) MarshalYAML() (any, error) { return d.String(), nil }

// UnmarshalYAML accepts a scalar or a sequence.
func (l *StringList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		*l = StringList{s}
		return nil
	case yaml.SequenceNode:
		var ss []string
		if err := n.Decode(&ss); err != nil {
			return err
		}
		*l = ss
		return nil
	}
	return fmt.Errorf("line %d: want a string or a list of strings", n.Line)
}
