package harmony

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
)

func TestTargetsDoNotPretendHDCIsADB(t *testing.T) {
	infos := parseTargets("[Empty]\nServer started\nphone-1\tUSB\tUnauthorized\tlocalhost\nphone-2 USB Connected localhost\nphone-3 TCP Offline localhost\nphone-1 USB Connected localhost\n")
	if len(infos) != 3 {
		t.Fatalf("infos = %+v", infos)
	}
	if infos[0].State != driver.StateUnauthorized || infos[0].Setup != driver.FixAuthorize {
		t.Fatalf("unauthorized = %+v", infos[0])
	}
	if infos[1].State != driver.StateNeedsSetup || infos[1].Setup != "harmony_backend" {
		t.Fatalf("connected = %+v", infos[1])
	}
	if infos[2].State != driver.StateOffline || infos[2].Connection != "wifi" {
		t.Fatalf("offline = %+v", infos[2])
	}
	for _, info := range infos {
		if info.Platform != driver.HarmonyOS || info.Backend != "hdc" {
			t.Fatalf("wrong platform/backend: %+v", info)
		}
	}
}

func TestMissingHDCStillListsUSBPhone(t *testing.T) {
	p := NewProvider()
	p.find = func() (string, error) { return "", exec.ErrNotFound }
	p.usb = func(context.Context) ([]string, error) { return []string{"test-phone"}, nil }
	p.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("must not run absent hdc")
		return nil, nil
	}
	infos, err := p.List(context.Background())
	if err != nil || len(infos) != 1 {
		t.Fatalf("List = %+v, %v", infos, err)
	}
	if info := infos[0]; info.ID != "test-phone" || info.State != driver.StateNeedsSetup || info.Setup != "hdc" {
		t.Fatalf("info = %+v", info)
	}
}

func TestDiscoveryUsesOnlyTargetListing(t *testing.T) {
	p := NewProvider()
	p.find = func() (string, error) { return "/sdk/hdc", nil }
	p.run = func(_ context.Context, path string, args ...string) ([]byte, error) {
		if path != "/sdk/hdc" || !reflect.DeepEqual(args, []string{"list", "targets", "-v"}) {
			t.Fatalf("unexpected command: %s %q", path, args)
		}
		return []byte("test-phone USB Unauthorized localhost\n"), nil
	}
	infos, err := p.List(context.Background())
	if err != nil || len(infos) != 1 {
		t.Fatalf("List = %+v, %v", infos, err)
	}
	if _, err := p.Open(context.Background(), "test-phone"); !errors.Is(err, driver.ErrOffline) {
		t.Fatalf("Open unauthorized: %v", err)
	}
	if _, err := p.Open(context.Background(), "missing"); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("Open missing: %v", err)
	}
}

func TestHDCFailureIsNotADevice(t *testing.T) {
	p := NewProvider()
	p.find = func() (string, error) { return "hdc", nil }
	want := errors.New("server unavailable")
	p.run = func(context.Context, string, ...string) ([]byte, error) { return nil, want }
	if _, err := p.List(context.Background()); !errors.Is(err, want) {
		t.Fatalf("List failure: %v", err)
	}
}
