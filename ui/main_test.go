package ui

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/i18n"
	mg "github.com/egoist/mygo/ui"
)

// testLang is the language of the client's words in tests: English, the
// labels the tests look for, whatever the computer's language is, unless a
// test asks for another with inLanguage.
var testLang atomic.Value

func TestMain(m *testing.M) {
	testLang.Store("en")
	i18n.SetLocaleFunc(func() string { return testLang.Load().(string) })
	os.Exit(m.Run())
}

// inLanguage shows the client's words in tag for the rest of the test, and
// MyGo's own in the testers given.
func inLanguage(t testing.TB, tag string, testers ...*mg.Tester) {
	t.Helper()
	testLang.Store(tag)
	for _, tt := range testers {
		tt.SetLocale(tag)
	}
	t.Cleanup(func() { testLang.Store("en") })
}
