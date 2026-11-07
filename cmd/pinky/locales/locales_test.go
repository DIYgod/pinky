package locales

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// catalog reads a catalog's messages, without the "@" entries.
func catalog(t *testing.T, file string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	out := map[string]string{}
	for k, v := range raw {
		if strings.HasPrefix(k, "@") {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			t.Fatalf("%s: %s: %v", file, k, err)
		}
		out[k] = s
	}
	return out
}

// argRe finds the arguments of an ICU message: {name} and the {name, …}
// of plural, select and formats.
var argRe = regexp.MustCompile(`\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*[,}]`)

// args returns the names of a message's arguments, sorted, without
// repeats. Quoted braces ('{') are not arguments.
func args(msg string) []string {
	msg = regexp.MustCompile(`'[{}#][^']*'`).ReplaceAllString(msg, "")
	var out []string
	for _, m := range argRe.FindAllStringSubmatch(msg, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	slices.Sort(out)
	return out
}

// TestTranslations checks that every English message has a Simplified
// Chinese one with the same arguments, and that the translation has no key
// English lacks.
func TestTranslations(t *testing.T) {
	en, zh := catalog(t, "en.json"), catalog(t, "zh-Hans.json")
	for k, msg := range en {
		tr, ok := zh[k]
		if !ok {
			t.Errorf("zh-Hans.json lacks %q (%q)", k, msg)
			continue
		}
		if a, b := args(msg), args(tr); !reflect.DeepEqual(a, b) {
			t.Errorf("%s: English has the arguments %v, Chinese %v", k, a, b)
		}
		if strings.TrimSpace(tr) == "" {
			t.Errorf("%s: empty translation", k)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("zh-Hans.json has %q, which en.json lacks", k)
		}
	}
}

// TestChineseSays checks a few messages as the app shows them.
func TestChineseSays(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{In("zh-Hans").SidebarStopAll(), "全部停止"},
		{In("en").SidebarStopAll(), "Stop all"},
		{In("zh-CN").SidebarStopAll(), "全部停止"},
		{In("zh-Hans-SG").SidebarStopAll(), "全部停止"},
		{In("zh").SidebarStopAll(), "全部停止"},
		{In("de").SidebarStopAll(), "Stop all"}, // other languages fall back to English
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
