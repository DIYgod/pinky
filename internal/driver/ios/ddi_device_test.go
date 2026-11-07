//go:build device

package ios

import (
	"context"
	"os"
	"testing"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/imagemounter"
)

// TestDiskImageStatus reads, and only reads, how the phone's developer disk
// image is mounted: Developer Mode, the cryptexes cryptexd lists, the
// personalized images the image mounter lists, and whether the image this
// computer would mount is the one on the phone. Nothing is mounted,
// unmounted or revealed.
func TestDiskImageStatus(t *testing.T) {
	udid := os.Getenv("PINKY_DEVICE")
	if udid == "" {
		t.Skip("PINKY_DEVICE not set")
	}
	img, err := findDiskImage("")
	t.Logf("image on this computer: %+v err=%v", img, err)

	base, err := findEntry(udid, false)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := imagemounter.IsDevModeEnabled(base)
	t.Logf("developer mode: %v err=%v", dev, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	l, err := dialLinkEntry(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	t.Logf("services: cryptexd %v, display %v, hid %v, instruments %v", l.has(cryptexService), l.has(displayService), l.has(universalHIDService), l.has(instrumentsService))

	if l.has(cryptexService) {
		cm, err := imagemounter.NewCryptexDeveloperDiskImageMounter(l.entry)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		list, err := cm.ListCryptexes()
		t.Logf("cryptexes (%v): %+v err=%v", time.Since(start), list, err)
		if img.Dir != "" {
			ok, err := cm.IsImageMounted(img.Dir)
			t.Logf("this computer's image installed as a cryptex: %v err=%v", ok, err)
		}
	}

	v, err := goios.GetProductVersion(base)
	if err != nil {
		t.Fatal(err)
	}
	pm, err := imagemounter.NewPersonalizedDeveloperDiskImageMounter(base, v)
	if err != nil {
		t.Logf("personalized mounter: %v", err)
		return
	}
	defer pm.Close()
	start := time.Now()
	imgs, err := pm.ListImages()
	t.Logf("personalized images (%v): %d err=%v", time.Since(start), len(imgs), err)
}

// TestDiskImageMountKeeps runs Pinky's mount on a phone that already has
// this computer's image: it must find it there and change nothing (no
// signing request, no upload). Skipped when the phone's image differs.
func TestDiskImageMountKeeps(t *testing.T) {
	udid := os.Getenv("PINKY_DEVICE")
	if udid == "" {
		t.Skip("PINKY_DEVICE not set")
	}
	img, err := findDiskImage("")
	if err != nil {
		t.Skip(err)
	}
	base, err := findEntry(udid, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	l, err := dialLinkEntry(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	m, err := imagemounter.NewCryptexDeveloperDiskImageMounter(l.entry)
	if err != nil {
		t.Skip(err)
	}
	if ok, err := m.IsImageMounted(img.Dir); err != nil || !ok {
		t.Skipf("the phone does not have this computer's image (%v, %v); this test only checks that a mounted one is kept", ok, err)
	}
	start := time.Now()
	r, err := mountDiskImage(ctx, l, img)
	t.Logf("mountDiskImage (%v): %+v err=%v", time.Since(start), r, err)
	if err != nil || !r.Already || r.How != mountCryptex || r.Replaced != "" {
		t.Fatalf("mountDiskImage = %+v, %v; want the image kept as it is", r, err)
	}
	list, err := m.ListCryptexes()
	t.Logf("cryptexes afterwards: %+v err=%v", list, err)
}
