package android

import (
	"strings"
	"testing"
)

// Synthetic fixture, never a dump of a person's phone.
const wmIdentityFixture = `
WINDOW MANAGER LAST ANR (dumpsys window lastanr)
  Window #0 Window{bad u0 old.app/Old}:
    mDisplayId=0 package=old.app
WINDOW MANAGER POLICY STATE (dumpsys window policy)
  showing=false
  mIsShowing=false
WINDOW MANAGER DISPLAY CONTENTS (dumpsys window displays)
  Display: mDisplayId=7
  mCurrentFocus=Window{bad u0 other.app/Other}
  mFocusedApp=ActivityRecord{def u0 other.app/.Other t3}
  Display: mDisplayId=0 (organized)
  mCurrentFocus=Window{abc u0 org.example.reader/WebView}
  mFocusedApp=ActivityRecord{def u0 org.example.reader/.WebView t4}
WINDOW MANAGER TOKENS (dumpsys window tokens)
  Window #0 Window{bad u0 stale.app/Old}:
WINDOW MANAGER WINDOWS (dumpsys window windows)
  Window #0 Window{abc u0 org.example.reader/WebView}:
    mDisplayId=0 taskId=4
    mOwnerUid=10042 showForAllUsers=false package=org.example.reader appop=NONE
    mAttrs={(0,0)(fillxfill) ty=BASE_APPLICATION fmt=TRANSLUCENT}
      fl=LAYOUT_IN_SCREEN HARDWARE_ACCELERATED
    Frames: parent=[0,0][1080,1920] display=[0,0][1080,1920] frame=[0,0][1080,1920]
    mHasSurface=true isReadyForDisplay()=true
    isOnScreen=true
    isVisible=true
  Window #1 Window{bbb u0 StatusBar}:
    mDisplayId=0
    mOwnerUid=10041 showForAllUsers=true package=com.android.systemui appop=NONE
    mAttrs={(0,0)(fillx74) ty=STATUS_BAR fmt=TRANSLUCENT}
      fl=NOT_FOCUSABLE
    Frames: parent=[0,0][1080,1920] display=[0,0][1080,1920] frame=[0,0][1080,74]
    mHasSurface=true
    isOnScreen=true
    isVisible=true
  Window #2 Window{ccc u0 Wallpaper}:
    mDisplayId=0
    mHasSurface=true
    isOnScreen=false
    isVisible=false
  mTopFocusedDisplayId=0
WINDOW MANAGER TRACE (dumpsys window trace)
`

func TestWindowIdentity(t *testing.T) {
	p := parseWindowIdentity(wmIdentityFixture, 1080, 1920)
	if p == nil {
		t.Fatal("no verified identity")
	}
	if p.app != "org.example.reader" || len(p.bars) != 1 || p.bars[0].H != 74 || p.token == "" {
		t.Fatalf("identity: %+v", p)
	}
	other := strings.ReplaceAll(wmIdentityFixture, "Window{abc ", "Window{abc2 ")
	q := parseWindowIdentity(other, 1080, 1920)
	if q == nil || p.token == q.token {
		t.Fatal("window change did not change capture identity")
	}
	q = parseWindowIdentity(strings.Replace(wmIdentityFixture, "mOwnerUid=10042", "mOwnerUid=10043", 1), 1080, 1920)
	if q == nil || p.token == q.token {
		t.Fatal("owner change did not change capture identity")
	}
}

func TestWindowIdentityRejectsUnsafeSnapshots(t *testing.T) {
	for _, tt := range []struct{ name, old, new string }{
		{"locked", "showing=false", "showing=true"},
		{"unknown lock", "showing=false\n  mIsShowing=false", "unknown"},
		{"wrong display", "mTopFocusedDisplayId=0", "mTopFocusedDisplayId=7"},
		{"no focus", "mCurrentFocus=Window{abc u0 org.example.reader/WebView}", "mCurrentFocus=null"},
		{"focus ownership mismatch", "package=org.example.reader", "package=org.example.wallet"},
		{"missing owner", "mOwnerUid=10042", "unknownOwner=10042"},
		{"missing geometry", "frame=[0,0][1080,1920]", "unknown=[0,0][1080,1920]"},
		{"not fullscreen", "frame=[0,0][1080,1920]", "frame=[0,0][540,1920]"},
		{"secure", "fl=LAYOUT_IN_SCREEN", "fl=SECURE LAYOUT_IN_SCREEN"},
		{"flag secure", "fl=LAYOUT_IN_SCREEN", "fl=FLAG_SECURE LAYOUT_IN_SCREEN"},
		{"surface absent", "mHasSurface=true isReadyForDisplay()=true", "mHasSurface=false"},
		{"hidden focus", "isOnScreen=true\n    isVisible=true", "isOnScreen=false\n    isVisible=false"},
		{"unknown visibility", "isVisible=true", "visibilityUnknown=true"},
		{"malformed visibility", "isVisible=true", "isVisible=true_unknown"},
		{"contradictory visibility", "isVisible=false", "isVisible=false isVisible=true"},
		{"duplicate visibility", "isVisible=true", "isVisible=true isVisible=true"},
		{"malformed hidden window", "Window #2 Window{ccc", "Window #2 Window{unknown"},
		{"overflow geometry", "frame=[0,0][1080,74]", "frame=[0,0][1080,999999999999999999999999]"},
		{"ANR has focus despite old activity", "mCurrentFocus=Window{abc u0 org.example.reader/WebView}", "mCurrentFocus=Window{bbb u0 Application Not Responding: org.example.reader}"},
		{"system alert over app", "ty=STATUS_BAR", "ty=SYSTEM_ALERT"},
		{"keyboard", "ty=STATUS_BAR", "ty=INPUT_METHOD"},
		{"other app visible", "ty=STATUS_BAR", "ty=BASE_APPLICATION"},
		{"bar not system owned", "package=com.android.systemui", "package=org.example.fake"},
		{"bar focusable", "fl=NOT_FOCUSABLE", "fl=HARDWARE_ACCELERATED"},
		{"bar too large", "frame=[0,0][1080,74]", "frame=[0,0][1080,1920]"},
		{"truncated windows section", "WINDOW MANAGER WINDOWS (", "WINDOW MANAGER MISSING ("},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out := strings.Replace(wmIdentityFixture, tt.old, tt.new, 1)
			if out == wmIdentityFixture {
				t.Fatal("fixture replacement did not match")
			}
			if got := parseWindowIdentity(out, 1080, 1920); got != nil {
				t.Fatalf("accepted unsafe snapshot: %+v", got)
			}
		})
	}
	if parseWindowIdentity(wmIdentityFixture, 0, 0) != nil {
		t.Fatal("accepted unknown display size")
	}
}

func TestWindowIdentityOwnedWindowBehindFocus(t *testing.T) {
	start := strings.Index(wmIdentityFixture, "  Window #0 Window{abc")
	end := strings.Index(wmIdentityFixture, "  Window #1 Window{bbb")
	parent := strings.Replace(wmIdentityFixture[start:end], "Window #0 Window{abc", "Window #3 Window{ddd", 1)
	out := strings.Replace(wmIdentityFixture, "  mTopFocusedDisplayId=0", parent+"  mTopFocusedDisplayId=0", 1)
	if parseWindowIdentity(out, 1080, 1920) == nil {
		t.Fatal("rejected same-owner full-screen parent below focus")
	}
	other := strings.Replace(parent, "mOwnerUid=10042", "mOwnerUid=10049", 1)
	if parseWindowIdentity(strings.Replace(out, parent, other, 1), 1080, 1920) != nil {
		t.Fatal("accepted foreign UID behind focus")
	}
	if parseWindowIdentity(strings.Replace(out, parent, strings.Replace(parent, "fl=LAYOUT_IN_SCREEN", "fl=SECURE LAYOUT_IN_SCREEN", 1), 1), 1080, 1920) != nil {
		t.Fatal("accepted protected parent")
	}
	out = strings.Replace(wmIdentityFixture, "  Window #0 Window{abc", parent+"  Window #0 Window{abc", 1)
	if parseWindowIdentity(out, 1080, 1920) != nil {
		t.Fatal("accepted application above focus")
	}
}
