package ios

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyMirrorChoosesPhoneVersion(t *testing.T) {
	for _, test := range []struct {
		version, path string
		wantSize      int64
	}{
		{"16.0", "16.0/DeveloperDiskImage.dmg", 8464365},
		{"16.5.1", "16.5/DeveloperDiskImage.dmg", 7427565},
		{"16.7.12", "16.7/DeveloperDiskImage.dmg", 7432520},
	} {
		files, err := legacyMirrorFiles(test.version)
		if err != nil || len(files) != 2 || files[0].from != test.path || files[0].size != test.wantSize || files[1].size != 128 {
			t.Errorf("iOS %s: files = %+v, err = %v", test.version, files, err)
		}
	}
	if _, err := legacyMirrorFiles("17.0"); err == nil {
		t.Error("iOS 17 should not receive an iOS 16 image")
	}
}

func TestLegacyImagePicksNewestCompatibleVersion(t *testing.T) {
	dir := t.TempDir()
	for _, version := range []string{"16.0", "16.5", "16.7"} {
		folder := filepath.Join(dir, version)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"", ".signature"} {
			if err := os.WriteFile(filepath.Join(folder, "DeveloperDiskImage.dmg"+suffix), []byte("test"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := legacyImageIn(dir, parseVersion("16.5.1")); got != filepath.Join(dir, "16.5", "DeveloperDiskImage.dmg") {
		t.Errorf("selected %q", got)
	}
}
