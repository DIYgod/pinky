package ios

import (
	"testing"
)

// A read that remoteAXService fails for goes without the inspector walk
// when the last read it answered showed web content, and with it again
// once a read shows none.
func TestLastReadWeb(t *testing.T) {
	d := &Device{}
	if d.lastReadWeb() {
		t.Fatal("a phone not read yet counts as showing web content")
	}
	page := []raxElement{{Label: "Alipay"}, {Label: "row", Web: true}}
	d.noteReadWeb(hasWeb(page))
	if !d.lastReadWeb() {
		t.Fatal("a page does not count as web content")
	}
	native := []raxElement{{Label: "Settings"}, {Label: "General"}}
	d.noteReadWeb(hasWeb(native))
	if d.lastReadWeb() {
		t.Fatal("a native screen still counts as web content")
	}
}
