package ios

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// These iOS 16 images and signatures are from the same pinned mirror commit
// as the personalized image in mirror.go. Pinky downloads only the minor
// version the person requested, after they choose Download in Add phone.
var legacyMirrorImages = [...]struct {
	size      int64
	hash      string
	signature string
}{
	{8464365, "429d2d05b7c9093f214b117a26571fa7d9596c5ff03e7983b70caff743f0afb1", "d30ae9c12874254c5cb453abb7c412c6796913e5533de6b68f1b28f435981100"},
	{8448885, "086bf1c1a278615e60dd5ce2d2ff55a21996bbdef3dc67df20e209e7dcd48558", "54b0ad8113231b1e11faebfdae4b347b482ed12dbe8caf155d25b17e74b1a60e"},
	{8466283, "d30f6d1274e1300910cd22c1104136006f0c55e28110b16cf20ed1ec48fad0e5", "247aa8dd50dea880437a1fabfda21552971f88d39384aca6f13d2c0eba7bdac0"},
	{8466283, "d30f6d1274e1300910cd22c1104136006f0c55e28110b16cf20ed1ec48fad0e5", "247aa8dd50dea880437a1fabfda21552971f88d39384aca6f13d2c0eba7bdac0"},
	{7432520, "6e4449ff8c751b4432934ade838382c79330b096b865eebabbc1fa42ead9c185", "4e82ee8aa75c5ecdd320e075b7d9e4ea2fc1915d0bb776007e8dbc5728ca5d86"},
	{7427565, "99fcf9a7300ab2504a2b9a79f210702f2f6d6eb056053fc8517b80a6a3e18a16", "355169a9753c23fff95f6ebc1e39a433f653a9e0e059580642d92629c1ac0071"},
	{7432520, "6e4449ff8c751b4432934ade838382c79330b096b865eebabbc1fa42ead9c185", "4e82ee8aa75c5ecdd320e075b7d9e4ea2fc1915d0bb776007e8dbc5728ca5d86"},
	{7432520, "6e4449ff8c751b4432934ade838382c79330b096b865eebabbc1fa42ead9c185", "4e82ee8aa75c5ecdd320e075b7d9e4ea2fc1915d0bb776007e8dbc5728ca5d86"},
}

func legacyMirrorVersion(version string) (int, error) {
	v := parseVersion(version)
	if v.Major != 16 || v.Minor < 0 {
		return 0, fmt.Errorf("no iOS 16 developer disk image for iOS %s", version)
	}
	return min(v.Minor, len(legacyMirrorImages)-1), nil
}

func legacyMirrorFiles(version string) ([]mirrorFile, error) {
	minor, err := legacyMirrorVersion(version)
	if err != nil {
		return nil, err
	}
	image := legacyMirrorImages[minor]
	from := fmt.Sprintf("16.%d/DeveloperDiskImage.dmg", minor)
	return []mirrorFile{
		{from, "DeveloperDiskImage.dmg", image.size, image.hash},
		{from + ".signature", "DeveloperDiskImage.dmg.signature", 128, image.signature},
	}, nil
}

// LegacyDiskImageMirror describes the iOS 16 image offered for this phone.
func (p *Provider) LegacyDiskImageMirror(version string) (driver.DiskImageMirror, bool) {
	files, err := legacyMirrorFiles(version)
	if err != nil {
		return driver.DiskImageMirror{}, false
	}
	minor, _ := legacyMirrorVersion(version)
	return driver.DiskImageMirror{Name: mirrorName, Image: fmt.Sprintf("iOS 16.%d DeveloperDiskImage", minor), Size: mirrorSize(files)}, true
}

// DownloadLegacyDiskImage downloads the requested image to Pinky's own
// folder, checking both files before they can be used to mount a phone.
func (p *Provider) DownloadLegacyDiskImage(ctx context.Context, version string, progress func(done, total int64)) error {
	if p.DiskImageDir == "" {
		return errors.New("Pinky has no folder to keep the developer disk image in")
	}
	minor, err := legacyMirrorVersion(version)
	if err != nil {
		return err
	}
	files, _ := legacyMirrorFiles(version)
	parent := filepath.Join(p.DiskImageDir, "LegacyDiskImages")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	base := "https://raw.githubusercontent.com/doronz88/DeveloperDiskImage/" + mirrorCommit + "/DeveloperDiskImages/"
	client := &http.Client{Timeout: 10 * time.Minute}
	var done int64
	total := mirrorSize(files)
	for _, f := range files {
		if err := fetchMirrorFile(ctx, client, base+f.from, f, filepath.Join(tmp, f.to), func(n int64) {
			done += n
			if progress != nil {
				progress(done, total)
			}
		}); err != nil {
			return err
		}
	}
	final := filepath.Join(parent, fmt.Sprintf("16.%d", minor))
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	p.retryNow()
	return nil
}
