package ios

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/imagemounter"
)

//go:embed legacywda/RunnerHost.m legacywda/package.sh
var legacyWDAPackager embed.FS

// helperPhones also finds wired iOS 16 devices, which devicectl can omit.
func helperPhones(ctx context.Context, x xcode, want string) ([]helperPhone, error) {
	var legacy []helperPhone
	for _, e := range legacyHelperEntries(ctx, want) {
		v, err := getValues(ctx, e)
		if err != nil || parseVersion(v.Value.ProductVersion).Major != 16 {
			continue
		}
		ph := helperPhone{UDID: e.Properties.SerialNumber, Name: v.Value.DeviceName,
			OS: v.Value.ProductVersion, Reachable: true, lockdown: e}
		if on, err := bounded(ctx, lockdownTimeout, func() (bool, error) { return imagemounter.IsDevModeEnabled(e) }); err == nil && !on {
			ph.DeveloperMode = "disabled"
		}
		legacy = append(legacy, ph)
	}
	if want != "" {
		if ph, err := pickPhone(legacy, want); err == nil && strings.EqualFold(ph.UDID, want) {
			return legacy, nil
		}
	}
	phones, err := listPhones(ctx, x)
	if err != nil && len(legacy) == 0 {
		return nil, err
	}
	for _, ph := range phones {
		found := false
		for _, old := range legacy {
			if strings.EqualFold(ph.UDID, old.UDID) {
				found = true
				break
			}
		}
		if !found {
			legacy = append(legacy, ph)
		}
	}
	return legacy, nil
}

func legacyHelperEntries(ctx context.Context, want string) []goios.DeviceEntry {
	list, err := bounded(ctx, lockdownTimeout, goios.ListDevices)
	if err != nil {
		return nil
	}
	var usb []goios.DeviceEntry
	for _, e := range list.DeviceList {
		if strings.EqualFold(e.Properties.SerialNumber, want) {
			if e.Properties.ConnectionType == "USB" {
				return []goios.DeviceEntry{e}
			}
			continue
		}
		if e.Properties.ConnectionType == "USB" {
			usb = append(usb, e)
		}
	}
	return usb // The standalone command can also name a phone by name.
}

// Build the signed runner by target. The iOS 16 phone cannot serve as a
// destination in newer Xcode versions.
func legacyHelperBuildArgs(src, dd, team, bid string, updates bool) []string {
	args := []string{"-project", filepath.Join(src, "WebDriverAgent.xcodeproj"),
		"-target", "WebDriverAgentRunner", "-sdk", "iphoneos", "-configuration", "Debug",
		"SYMROOT=" + filepath.Join(dd, "Build", "Products"),
		"OBJROOT=" + filepath.Join(dd, "Build", "Intermediates.noindex"),
		"CODE_SIGN_STYLE=Automatic", "DEVELOPMENT_TEAM=" + team, "PRODUCT_BUNDLE_IDENTIFIER=" + bid}
	if updates {
		args = append(args, "-allowProvisioningUpdates", "-allowProvisioningDeviceRegistration")
	}
	return append(args, "build")
}

// The icon phase asks for a simulator runtime even for a generic iPhone
// build. The legacy package only needs the executable and test bundle.
var legacyWDAIcon = regexp.MustCompile(`(?m)^[ \t]*[A-F0-9]+ /\* Assets\.xcassets in Resources \*/[^\n]*\n`)

func prepareLegacyWDAProject(src string) error {
	project := filepath.Join(src, "WebDriverAgent.xcodeproj", "project.pbxproj")
	body, err := os.ReadFile(project)
	if err != nil {
		return err
	}
	return os.WriteFile(project, legacyWDAIcon.ReplaceAll(body, nil), 0o644)
}

func installLegacyHelper(ctx context.Context, xr xcodeRunner, ph helperPhone, app, bid, tmp, logPath, imageDir string) error {
	for _, name := range []string{"RunnerHost.m", "package.sh"} {
		body, err := legacyWDAPackager.ReadFile("legacywda/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, name), body, 0o600); err != nil {
			return err
		}
	}
	ipa := filepath.Join(tmp, "PinkyLegacyWDA.ipa")
	cmd := exec.CommandContext(ctx, "/bin/sh", filepath.Join(tmp, "package.sh"), app, ipa)
	cmd.Env = xr.x.env()
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintln(xr.log, string(output))
		return helperErr("", fmt.Errorf("packaging the iOS 16 runner: %w: %s", err, xcodeErrors(string(output))), logPath)
	}
	if err := prepareLegacyImage(ctx, ph.lockdown, ph.OS, imageDir); err != nil {
		return helperErr("", err, logPath)
	}
	fmt.Fprintf(xr.out, "Installing and starting WebDriverAgent on %s over USB. Keep the phone unlocked.\n", ph.Name)
	runCtx, cancel := context.WithTimeout(ctx, helperStartTimeout)
	defer cancel()
	if err := installIPA(runCtx, ph.lockdown, ipa, helperDisplayName); err != nil {
		return helperErr("", err, logPath)
	}
	apps, err := userApps(ph.lockdown)
	if err != nil {
		return helperErr("", err, logPath)
	}
	id := bid + ".xctrunner"
	var runner wdaRunner
	for _, a := range apps {
		if a["CFBundleIdentifier"] == id {
			path, _ := a["Path"].(string)
			runner = wdaRunner{BundleID: id, Path: path}
			break
		}
	}
	if runner.BundleID == "" {
		return helperErr("", fmt.Errorf("installed runner %s was not found", id), logPath)
	}
	s := &wdaStart{base: ph.lockdown, c: newWDAUSB(ph.lockdown)}
	if s.ready(runCtx) {
		return helperErr("", errors.New("another WebDriverAgent is running on the iPhone. Stop it before testing the new installation"), logPath)
	}
	s.run = startWDA(ph.lockdown, ph.lockdown, runner, wdaBind(ph.lockdown, nil), true)
	defer s.stop()
	for {
		if s.ready(runCtx) {
			return nil
		}
		if alive, err := s.run.alive(); !alive {
			return helperErr("", fmt.Errorf("the iOS 16 runner ended before it answered: %w", err), logPath)
		}
		if s.run.asking() && xr.progress != nil {
			xr.progress(driver.HelperUnlock)
		}
		select {
		case <-runCtx.Done():
			return helperErr(driver.HelperFixPasscode, runCtx.Err(), logPath)
		case <-time.After(400 * time.Millisecond):
		}
	}
}

func prepareLegacyImage(ctx context.Context, e goios.DeviceEntry, version, ownDir string) error {
	if probeLockdownXCTest(ctx, e) == nil {
		return nil
	}
	img := legacyImagePath(ownDir, parseVersion(version))
	if img == "" {
		return fmt.Errorf("no compatible developer disk image for iOS %s: %w", version, errNoDiskImage)
	}
	_, err := bounded(ctx, helperStartTimeout, func() (bool, error) { return true, mountLegacyImage(e, img) })
	if err != nil {
		return fmt.Errorf("mounting the iOS 16 developer disk image: %w", err)
	}
	if err := probeLockdownXCTest(ctx, e); err != nil {
		return fmt.Errorf("the iOS 16 developer disk image did not make XCTest available: %w", err)
	}
	return nil
}

// mountLegacyImage is synchronous. A background preparation job must wait
// for the underlying mount to finish before allowing another attempt: the
// image mounter has no context and bounded would leave it running.
func mountLegacyImage(e goios.DeviceEntry, img string) error {
	m, err := imagemounter.NewImageMounter(e)
	if err != nil {
		return err
	}
	defer m.Close()
	list, err := m.ListImages()
	if err != nil || len(list) > 0 {
		return err
	}
	return m.MountImage(img)
}

func legacyImagePath(ownDir string, version osVersion) string {
	if x, err := findXcode(); err == nil {
		if img := legacyImageIn(filepath.Join(x.Developer, "Platforms", "iPhoneOS.platform", "DeviceSupport"), version); img != "" {
			return img
		}
	}
	if ownDir != "" {
		if img := legacyImageIn(filepath.Join(ownDir, "LegacyDiskImages"), version); img != "" {
			return img
		}
	}
	return ""
}

func legacyImageIn(dir string, version osVersion) string {
	dirs, _ := os.ReadDir(dir)
	var best osVersion
	var image string
	for _, d := range dirs {
		v := parseVersion(strings.SplitN(d.Name(), " ", 2)[0])
		if !d.IsDir() || v.Major != version.Major || v.Minor > version.Minor ||
			(v.Minor == version.Minor && v.Patch > version.Patch) ||
			(best.Major != 0 && (v.Minor < best.Minor || v.Minor == best.Minor && v.Patch <= best.Patch)) {
			continue
		}
		f := filepath.Join(dir, d.Name(), "DeveloperDiskImage.dmg")
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if _, err := os.Stat(f + ".signature"); err != nil {
			continue
		}
		best, image = v, f
	}
	return image
}
