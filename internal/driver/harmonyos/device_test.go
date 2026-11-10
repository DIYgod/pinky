package harmonyos

import (
	"errors"
	"strings"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

const settingsReport = `Current State: AWAKE  Reason: 1
SCBStatusBar31 0 17 4 2108 102 1 110 0 [ 0 0 1840 87 ]
SCBGestureBack26 0 17 31 2111 1 0 4102 0 [ 0 0 1840 2800 ]
settings0          0 33 59 1 1 16 102 0 [ 0 0 1840 2800 ] [ 0 0 ]
ScreenLockWindow   0 17 20 2110 1 0 -1 0 [ 0 0 1840 2800 ]
Focus window: 59
`

func TestSettingsSurface(t *testing.T) {
	s, err := parseFocusSurface(settingsReport)
	if err != nil || s != (surface{id: "59", pid: "33", chromePID: "17", width: 1840, height: 2800, top: 87}) {
		t.Fatalf("surface=%+v, error=%v", s, err)
	}
	for _, tc := range []struct {
		name, report string
		want         error
	}{
		{"screen off", "Current State: SLEEP\n" + settingsReport, driver.ErrLocked},
		{"lock above settings", settingsReport + "ScreenLockWindow 0 17 21 2110 1 0 120 0 [ 0 0 1840 2800 ]\n", driver.ErrLocked},
		{"notification above settings", settingsReport + "SystemUi_BannerNotic 0 17 23 2111 1 0 120 0 [ 0 0 1840 300 ]\n", driver.ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseFocusSurface(tc.report); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
		})
	}
}

func TestSwipeStaysInsideScrollablePane(t *testing.T) {
	panes := []screen.Rect{{X: 0, Y: 80, W: 620, H: 1700}}
	from := screen.Point{X: 310, Y: 1200}
	if !inScrollPane(panes, from, screen.Point{X: 310, Y: 500}) {
		t.Fatal("valid scroll was rejected")
	}
	if inScrollPane(panes, from, screen.Point{X: 310, Y: 82}) {
		t.Fatal("allowed a gesture through the edge of the pane")
	}
	if inScrollPane(panes, from, screen.Point{X: 565, Y: 1205}) {
		t.Fatal("allowed a horizontal gesture")
	}
	if inScrollPane(panes, from, screen.Point{X: 720, Y: 500}) {
		t.Fatal("allowed a gesture out of the pane")
	}
}

func TestPreviewMotionSamples(t *testing.T) {
	still := make([]uint8, 400)
	slightNoise := append([]uint8(nil), still...)
	slightNoise[20] = 8
	if !samePicture(still, slightNoise) {
		t.Fatal("small JPEG fluctuations should not count as movement")
	}
	for i := range slightNoise[:40] {
		slightNoise[i] = 80
	}
	if samePicture(still, slightNoise) {
		t.Fatal("the sample missed a moving region")
	}
}

func TestHomeSurfaceRequiresSceneBoard(t *testing.T) {
	report := `Current State: AWAKE
SCBScenePanel23 0 17 5 2109 1 0 100 0 [ 0 0 1840 2800 ]
SCBDesktop9 0 17 12 2001 1 0 2 0 [ 0 0 1840 2800 ]
SCBGestureNavBar19 0 17 24 2111 1 0 2200 0 [ 0 2737 1840 63 ]
SCBStatusBar31 0 17 37 2108 1 0 2202 0 [ 0 0 1840 130 ]
Focus window: 12
PINKY_PROCESS_LIST
17 com.ohos.sceneboard
`
	s, err := verifyFocusReport([]byte(report))
	if err != nil || !s.desktop || s.id != "12" || s.bottom != 63 {
		t.Fatalf("Home surface=%+v, error=%v", s, err)
	}
	for _, changed := range []string{
		strings.Replace(report, "17 com.ohos.sceneboard", "17 other.process", 1),
		strings.Replace(report, "SCBScenePanel23 0 17", "SCBScenePanel23 0 99", 1),
	} {
		if _, err := verifyFocusReport([]byte(changed)); !errors.Is(err, driver.ErrUnsupported) {
			t.Fatalf("unverified desktop accepted, error=%v", err)
		}
	}
}

func TestOwnerPreviewAllowsAppSwitchButProtectsInput(t *testing.T) {
	app := strings.Replace(settingsReport, "settings0", "calculator0", 1)
	owner, err := parseOwnerSurface(app)
	if err != nil || owner.id != "59" || owner.width != 1840 || owner.height != 2800 {
		t.Fatalf("owner surface=%+v, error=%v", owner, err)
	}
	if _, err := parseFocusSurface(app); !errors.Is(err, driver.ErrUnsupported) {
		t.Fatalf("non-Settings window accepted for an agent read: %v", err)
	}
	modal := strings.Replace(app, "Focus window: 59", "Focus window: 67", 1) +
		"SCBTransition7 0 17 67 2110 1 0 6000 0 [ 200 500 800 400 ]\n"
	owner, err = parseOwnerSurface(modal)
	if err != nil || owner.id != "" || owner.width != 1840 || owner.height != 2800 {
		t.Fatalf("transition surface=%+v, error=%v", owner, err)
	}
	locked := modal + "ScreenLockWindow9 0 17 70 2110 1 0 6200 0 [ 0 0 1840 2800 ]\n"
	if _, err := parseOwnerSurface(locked); !errors.Is(err, driver.ErrLocked) {
		t.Fatalf("lock did not stop the owner mirror: %v", err)
	}
}

func TestNextDisplayRotationUsesPhysicalPanel(t *testing.T) {
	for angle, want := range map[string]byte{"0": 'H', "90": 'V', "180": 'H', "270": 'V'} {
		report := "DisplayId: 1019\r\nScreenRotation: 0\r\nDisplayId: 0\r\nScreenRotation: " + angle + "\r\n"
		if got, err := nextDisplayRotation(report); err != nil || got != want {
			t.Fatalf("angle %s: command=%c, error=%v", angle, got, err)
		}
	}
	if _, err := nextDisplayRotation("DisplayId: 1019\nScreenRotation: 0"); err == nil {
		t.Fatal("accepted the rotation of the virtual display")
	}
}
