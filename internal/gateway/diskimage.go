package gateway

import (
	"context"
	"errors"

	"github.com/DIYgod/pinky/internal/driver"
)

// ErrNoDiskImageDownload is returned when no provider can download a
// developer disk image.
var ErrNoDiskImageDownload = errors.New("no connected platform can download a developer disk image")

// DiskImageMirror says where Pinky can download the developer disk image a
// computer without Xcode lacks, or whose image Apple refused (iPhones),
// from the first provider that can
// (driver.DiskImageDownloader); ok is false when none can.
func (g *Gateway) DiskImageMirror() (driver.DiskImageMirror, bool) {
	for _, p := range g.opt.Providers {
		if d, ok := p.(driver.DiskImageDownloader); ok {
			return d.DiskImageMirror(), true
		}
	}
	return driver.DiskImageMirror{}, false
}

// DownloadDiskImage downloads it with that provider, when the person asks.
// Phones that waited for the image are prepared at the next discovery.
func (g *Gateway) DownloadDiskImage(ctx context.Context, progress func(done, total int64)) error {
	for _, p := range g.opt.Providers {
		if d, ok := p.(driver.DiskImageDownloader); ok {
			return d.DownloadDiskImage(ctx, progress)
		}
	}
	return ErrNoDiskImageDownload
}

// LegacyDiskImageMirror describes the version-specific iOS 16 image offered
// when the person is adding that phone.
func (g *Gateway) LegacyDiskImageMirror(version string) (driver.DiskImageMirror, bool) {
	for _, p := range g.opt.Providers {
		if d, ok := p.(interface {
			LegacyDiskImageMirror(string) (driver.DiskImageMirror, bool)
		}); ok {
			return d.LegacyDiskImageMirror(version)
		}
	}
	return driver.DiskImageMirror{}, false
}

// DownloadLegacyDiskImage downloads the image only after the person chooses
// Download in Add phone.
func (g *Gateway) DownloadLegacyDiskImage(ctx context.Context, version string, progress func(done, total int64)) error {
	for _, p := range g.opt.Providers {
		if d, ok := p.(interface {
			DownloadLegacyDiskImage(context.Context, string, func(int64, int64)) error
		}); ok {
			return d.DownloadLegacyDiskImage(ctx, version, progress)
		}
	}
	return ErrNoDiskImageDownload
}
