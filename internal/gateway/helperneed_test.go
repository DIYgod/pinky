package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/policy"
)

// helperPhone is an iPhone on iOS 27 left on Automatic, over CoreDevice
// with no switches and no second finger, the helper installed (or not).
func helperPhone(t *testing.T, installed bool) (*twoWayPhone, *Gateway) {
	t.Helper()
	shortSwitchBack(t)
	p, dev := newTwoWayPhone()
	dev.SetVirtual(true) // no Phone settings approval
	p.bareCore, p.noHelper = true, !installed
	g := twoWayGateway(t, p)
	if err := g.Policy().SetPreset(policy.PresetPayments); err != nil {
		t.Fatal(err)
	}
	return p, g
}

func rotateFeature(g *Gateway) bool {
	ds := g.Devices()
	return len(ds) == 1 && ds[0].Features.Rotate
}

// phone_device moves the task to the helper for a change and goes on; a
// read moves nothing. The task then stays on the helper until it ends, so
// what it changed is put back there.
func TestDeviceMovesToTheHelper(t *testing.T) {
	ctx := context.Background()
	_, g := helperPhone(t, true)
	s, _ := connect(t, g, "Test the landscape layout")
	if res := s.Device(ctx, DeviceRequest{}); res.Error != ErrUnsupported || !strings.Contains(res.Message, "a change moves the task to the helper by itself") {
		t.Fatalf("read over CoreDevice: %+v", res)
	}
	if b := backendOf(g); b != wayCoreDevice {
		t.Fatalf("a read moved the phone to %q", b)
	}
	res := s.Device(ctx, DeviceRequest{Rotation: driver.RotationLandscape})
	mustStatus(t, res, StatusSettled)
	if !strings.Contains(res.Message, "Pinky moved this task to the WebDriverAgent helper for turning the screen") {
		t.Fatalf("turn: %q", res.Message)
	}
	if b := backendOf(g); b != wayHelper {
		t.Fatalf("backend %q after the turn", b)
	}
	if len(res.Switches) != 1 || res.Switches[0].Value != driver.RotationLandscape || !res.Switches[0].Changed {
		t.Fatalf("switches %+v", res.Switches)
	}
	// Going back before the task ends would leave the turn in place.
	res = s.Connect(ctx, ConnectRequest{Task: "Test the landscape layout", Connection: "coredevice"})
	if !strings.Contains(res.Message, "keeps") || backendOf(g) != wayHelper {
		t.Fatalf("back to CoreDevice with a turn to put back: %q, %q", res.Message, backendOf(g))
	}
	// Put back, it may go.
	mustStatus(t, s.Device(ctx, DeviceRequest{Reset: []string{"rotation"}}), StatusSettled)
	mustStatus(t, s.Connect(ctx, ConnectRequest{Task: "Test the landscape layout", Connection: "coredevice"}), StatusSettled)
	if b := backendOf(g); b != wayCoreDevice {
		t.Fatalf("backend %q after the reset and asking for CoreDevice", b)
	}
}

// The task's turn is put back at its end, through the helper it stayed on;
// then the phone goes back to CoreDevice.
func TestPutBackOnTheHelper(t *testing.T) {
	ctx := context.Background()
	_, g := helperPhone(t, true)
	s, _ := connect(t, g, "Test the landscape layout")
	mustStatus(t, s.Device(ctx, DeviceRequest{Rotation: driver.RotationLandscape}), StatusSettled)
	mustStatus(t, s.Release(ctx, ReleaseRequest{}), StatusSettled)
	waitUntil(t, "the phone back on CoreDevice", func() bool { return backendOf(g) == wayCoreDevice })
	// Read it through the helper again: portrait, as before the task.
	s2, _ := connect(t, g, "Look")
	res := s2.Device(ctx, DeviceRequest{Rotation: driver.RotationPortrait})
	mustStatus(t, res, StatusSettled)
	if len(res.Switches) != 1 || res.Switches[0].Changed {
		t.Fatalf("after the put-back: %+v", res.Switches)
	}
}

// Without the helper, a change says so and nothing moves.
func TestDeviceWithoutTheHelper(t *testing.T) {
	_, g := helperPhone(t, false)
	s, _ := connect(t, g, "Test the landscape layout")
	res := s.Device(context.Background(), DeviceRequest{Rotation: driver.RotationLandscape})
	if res.Error != ErrUnsupported || !strings.Contains(res.Message, "has no switches") || !strings.Contains(res.Message, "not installed") {
		t.Fatalf("turn without the helper: %+v", res)
	}
	if b := backendOf(g); b != wayCoreDevice {
		t.Fatalf("backend %q", b)
	}
	if rotateFeature(g) {
		t.Fatal("the person's Rotate shows without the helper")
	}
}

// phone_pinch moves the task to the helper and returns stale with the
// screen read there; the next pinch pinches.
func TestPinchMovesToTheHelper(t *testing.T) {
	ctx := context.Background()
	_, g := helperPhone(t, true)
	s, _ := connect(t, g, "Zoom the map")
	res := s.Act(ctx, Call{}, Action{Tool: "pinch", Scale: 2})
	if res.Status != StatusStale || !strings.Contains(res.Message, "for pinching") || !strings.Contains(res.Message, "Nothing was pinched") || res.Screen == nil {
		t.Fatalf("first pinch: %+v", res)
	}
	if b := backendOf(g); b != wayHelper {
		t.Fatalf("backend %q after the pinch", b)
	}
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "pinch", Scale: 2}), StatusSettled)
}

// The person's Rotate shows on a phone over CoreDevice with the helper,
// moves it there, turns it, and the phone goes back to CoreDevice once no
// task holds it; the person's choice in Settings › Phones turns that off.
func TestPersonRotatesThroughTheHelper(t *testing.T) {
	ctx := context.Background()
	_, g := helperPhone(t, true)
	waitUntil(t, "Rotate offered", func() bool { return rotateFeature(g) })
	if v, err := g.DeviceSwitch(ctx, "iphone", driver.SwitchRotation); err != nil || v != "" {
		t.Fatalf("read over CoreDevice: %q, %v", v, err)
	}
	if err := g.SetDeviceSwitch(ctx, "iphone", driver.SwitchRotation, driver.RotationLandscape); err != nil {
		t.Fatal(err)
	}
	if v, err := g.DeviceSwitch(ctx, "iphone", driver.SwitchRotation); err != nil || v != driver.RotationLandscape {
		t.Fatalf("read on the helper: %q, %v", v, err)
	}
	waitUntil(t, "the phone back on CoreDevice", func() bool { return backendOf(g) == wayCoreDevice })
	if !rotateFeature(g) {
		t.Fatal("Rotate gone after going back")
	}
	if err := g.SetConnection("iphone", wayCoreDevice); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "Rotate gone with CoreDevice chosen", func() bool { return !rotateFeature(g) })
	if err := g.SetDeviceSwitch(ctx, "iphone", driver.SwitchRotation, driver.RotationPortrait); err == nil {
		t.Fatal("turned with CoreDevice chosen")
	}
}
