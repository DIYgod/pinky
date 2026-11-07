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

// TestLanguageMatch checks which catalog a language reads: Chinese written
// in simplified characters reads Chinese, other languages English.
func TestLanguageMatch(t *testing.T) {
	zh, en := In("zh-Hans").SidebarStopAll(), In("en").SidebarStopAll()
	if zh == en {
		t.Fatalf("the Chinese catalog says %q as English does", zh)
	}
	for _, tag := range []string{"zh-CN", "zh-Hans-SG", "zh"} {
		if got := In(tag).SidebarStopAll(); got != zh {
			t.Errorf("%s says %q, want the Chinese %q", tag, got, zh)
		}
	}
	for _, tag := range []string{"en-GB", "de", "zh-Hant"} {
		if got := In(tag).SidebarStopAll(); got != en {
			t.Errorf("%s says %q, want the English %q", tag, got, en)
		}
	}
}
