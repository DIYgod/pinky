// Command windows-dev-assets prepares the dependencies of a local Windows build.
// The FFmpeg archive is for development only; released packages use scripts/dist.sh.
package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/driver/ios"
)

const (
	archiveURL  = "https://github.com/FFmpeg-Builds/FFmpeg-Builds/releases/download/latest/ffmpeg-n9.0-latest-win64-gpl-shared-9.0.zip"
	archiveHash = "dfde29675826fad6590da9f9cf9362dd05cc82a1a302a158a9130288bda8cbcf"
)

// BtbN's GPL build is used only for local development, never in a release.
var dlls = map[string]string{
	"avcodec-63.dll":   "ae4f5a53ac8a3c9c33c7964b6ff9c284d4f6dde1e36eb97ec811fb7552f2262e",
	"avutil-61.dll":    "b0555f29e01fda8e5a10882c2a67b301ec598107cdae300197d186de7c9a2e74",
	"swresample-7.dll": "61b3b383c0f4189e0a877650071338627a2c4b1429755d495b69b5268194d7d1",
	"swscale-10.dll":   "4d6b2a094fe35629c90246d6d53cc458de8160b65193d7b416da76c3cac24a7d",
}

func main() {
	if len(os.Args) != 2 {
		fatal(errors.New("usage: windows-dev-assets ddi|ffmpeg"))
	}
	var err error
	switch os.Args[1] {
	case "ddi":
		err = ddi()
	case "ffmpeg":
		err = ffmpeg()
	default:
		err = errors.New("usage: windows-dev-assets ddi|ffmpeg")
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "windows-dev-assets:", err)
	os.Exit(1)
}

func ddi() error {
	dirs, err := api.DefaultDirs()
	if err != nil {
		return err
	}
	folder := filepath.Join(dirs.Data, "DeveloperDiskImages")
	if ios.DiskImageMirrorReady(folder) {
		fmt.Println("Developer disk image: verified")
		return nil
	}
	fmt.Println("Downloading the pinned developer disk image...")
	p := ios.NewProvider()
	p.DiskImageDir = folder
	return p.DownloadDiskImage(context.Background(), nil)
}

func ffmpeg() error {
	dir := filepath.Join("build", "pinky", "windows-amd64")
	for name, want := range dlls {
		if !matches(filepath.Join(dir, name), want) {
			return stageFFmpeg(dir)
		}
	}
	fmt.Println("FFmpeg 9 DLLs: verified")
	return nil
}

func matches(path, want string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == want
}

func stageFFmpeg(dir string) error {
	cache := filepath.Join("build", "ffmpeg-windows-dev")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return err
	}
	archive := filepath.Join(cache, "ffmpeg.zip")
	if !matches(archive, archiveHash) {
		source := os.Getenv("PINKY_WINDOWS_FFMPEG_ZIP")
		if source != "" {
			if !matches(source, archiveHash) {
				return fmt.Errorf("%s does not match the pinned FFmpeg archive", source)
			}
			fmt.Println("Caching the verified FFmpeg archive...")
			if err := copyVerified(source, archive, archiveHash); err != nil {
				return err
			}
		} else {
			fmt.Println("Downloading the pinned FFmpeg 9 development archive...")
			if err := download(archiveURL, archive, archiveHash); err != nil {
				return err
			}
		}
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	found := make(map[string]bool)
	for _, entry := range zr.File {
		name := filepath.Base(entry.Name)
		want, ok := dlls[name]
		if !ok || found[name] {
			continue
		}
		found[name] = true
		if matches(filepath.Join(dir, name), want) {
			continue
		}
		r, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeVerified(r, filepath.Join(dir, name), want)
		r.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Println("Installed", name)
	}
	if len(found) != len(dlls) {
		return errors.New("the FFmpeg archive is missing a required DLL")
	}
	return nil
}

func copyVerified(source, target, want string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	return writeVerified(f, target, want)
}

func download(url, target, want string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	res, err := client.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("FFmpeg download: %s", res.Status)
	}
	return writeVerified(res.Body, target, want)
}

func writeVerified(src io.Reader, target, want string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(target), ".pinky-asset-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), src)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("SHA-256 %s does not match pinned %s", got, want)
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(out.Name(), target)
}
