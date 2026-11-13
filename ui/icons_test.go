package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every bundled MingCute icon is one the app shows: the folder holds the
// icons in use and no others (icons/mingcute/README.md).
func TestBundledIconsAreUsed(t *testing.T) {
	files, err := mingcuteSVGs.ReadDir("icons/mingcute")
	if err != nil {
		t.Fatal(err)
	}
	srcs, _ := filepath.Glob("*.go")
	var code strings.Builder
	for _, src := range srcs {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		code.Write(b)
	}
	for _, f := range files {
		name := strings.TrimSuffix(f.Name(), ".svg")
		if !strings.Contains(code.String(), `mingcute("`+name+`")`) {
			t.Errorf("icons/mingcute/%s is bundled but not shown: remove it", f.Name())
		}
	}
}
