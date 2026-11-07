package ios

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/danielpaulus/go-ios/ios/installationproxy"
	"howett.net/plist"
)

const (
	appService               = "com.apple.coredevice.appservice"
	featureLaunchApplication = "com.apple.coredevice.feature.launchapplication"
)

// appCatalog caches the installed apps for a short while; Launch resolves
// display names through it and Observe app bundles.
type appCatalog struct {
	mu     sync.Mutex
	apps   []driver.App
	byPath map[string]driver.App // bundle path -> app, hidden apps included
	at     time.Time
}

const appCacheTTL = 30 * time.Second

// Apps implements driver.Device. It lists the apps a person can open from
// the home screen, through the installation proxy.
func (d *Device) Apps(ctx context.Context) ([]driver.App, error) {
	c, err := d.catalog(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]driver.App(nil), c.apps...), nil
}

// catalog returns the app catalog, refreshed when older than appCacheTTL.
func (d *Device) catalog(ctx context.Context) (*appCatalog, error) {
	return d.catalogWithin(ctx, appCacheTTL)
}

// catalogFresh returns the app catalog read again unless it was read in
// the last appRecheck: for an app installed or updated since.
func (d *Device) catalogFresh(ctx context.Context) (*appCatalog, error) {
	return d.catalogWithin(ctx, appRecheck)
}

// appRecheck is how often at most the catalog is read again for a process
// it does not list.
const appRecheck = 10 * time.Second

// catalogWithin returns the app catalog, read again when older than ttl.
func (d *Device) catalogWithin(ctx context.Context, ttl time.Duration) (*appCatalog, error) {
	d.svcMu.Lock()
	if d.apps == nil {
		d.apps = &appCatalog{}
	}
	c := d.apps
	d.svcMu.Unlock()
	c.mu.Lock()
	fresh := c.apps != nil && time.Since(c.at) < ttl
	c.mu.Unlock()
	if fresh {
		return c, nil
	}
	type result struct {
		apps   []driver.App
		byPath map[string]driver.App
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		apps, byPath, err := browseApps(d.l)
		ch <- result{apps, byPath, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		c.mu.Lock()
		c.apps, c.byPath, c.at = r.apps, r.byPath, time.Now()
		c.mu.Unlock()
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func browseApps(l *link) ([]driver.App, map[string]driver.App, error) {
	conn, err := installationproxy.New(l.base)
	if err != nil {
		return nil, nil, fmt.Errorf("installation proxy: %w", err)
	}
	defer conn.Close()
	infos, err := conn.BrowseAllApps()
	if err != nil {
		return nil, nil, fmt.Errorf("list apps: %w", err)
	}
	var out []driver.App
	byPath := map[string]driver.App{}
	for _, a := range infos {
		id, _ := a["CFBundleIdentifier"].(string)
		name, _ := a["CFBundleDisplayName"].(string)
		if name == "" {
			name, _ = a["CFBundleName"].(string)
		}
		typ, _ := a["ApplicationType"].(string)
		app := driver.App{ID: id, Name: name, System: typ != "User"}
		if path, _ := a["Path"].(string); path != "" && id != "" {
			byPath[strings.TrimSuffix(path, "/")] = app
		}
		if visibleApp(a) {
			out = append(out, app)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, byPath, nil
}

// appForExecutable maps a process's executable path to its app.
func (c *appCatalog) appForExecutable(exe string) (driver.App, bool) {
	if strings.HasPrefix(exe, "/System/Library/CoreServices/SpringBoard.app/") {
		return driver.App{ID: springBoard, Name: "Home Screen", System: true}, true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for dir := exe; dir != "/" && dir != "."; {
		i := strings.LastIndex(dir, "/")
		if i <= 0 {
			break
		}
		dir = dir[:i]
		if app, ok := c.byPath[dir]; ok {
			return app, true
		}
		// Paths under /private/var are listed without the /private prefix,
		// or the other way round.
		if app, ok := c.byPath[strings.TrimPrefix(dir, "/private")]; ok {
			return app, true
		}
		if app, ok := c.byPath["/private"+dir]; ok {
			return app, true
		}
	}
	return driver.App{}, false
}

const springBoard = "com.apple.springboard"

// processPaths lists the device's processes as pid -> executable path.
func (d *Device) processPaths(ctx context.Context) (map[uint64]string, error) {
	svc, err := dialCoreDevice(d.l.entry, appService)
	if err != nil {
		return nil, err
	}
	defer svc.Close()
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := svc.invoke(c, "com.apple.coredevice.feature.listprocesses", "", nil)
	if err != nil {
		return nil, err
	}
	procs, _ := out["processTokens"].([]any)
	m := make(map[uint64]string, len(procs))
	for _, p := range procs {
		t, _ := p.(map[string]any)
		pid := uint64(toInt64(t["processIdentifier"]))
		u, _ := t["executableURL"].(map[string]any)
		rel, _ := u["relative"].(string)
		if pid != 0 && rel != "" {
			m[pid] = executablePath(rel)
		}
	}
	return m, nil
}

// visibleApp reports whether an app shows on the home screen: user and
// system apps, without hidden or web-clip helpers.
func visibleApp(a installationproxy.AppInfo) bool {
	id, _ := a["CFBundleIdentifier"].(string)
	if id == "" {
		return false
	}
	switch a["ApplicationType"] {
	case "User", "System":
	default:
		return false
	}
	if tags, ok := a["SBAppTags"].([]any); ok {
		for _, t := range tags {
			if s, _ := t.(string); s == "hidden" || s == "SBInternalAppTag" {
				return false
			}
		}
	}
	if b, _ := a["LSApplicationLaunchProhibited"].(bool); b {
		return false
	}
	return true
}

// Launch implements driver.Device. app is a bundle id or a display name.
// A running app is brought to the front, not restarted.
func (d *Device) Launch(ctx context.Context, app string) error {
	id, err := d.resolveApp(ctx, app)
	if err != nil {
		return err
	}
	d.markInput()
	var opts bytes.Buffer
	if err := plist.NewBinaryEncoder(&opts).Encode(map[string]any{}); err != nil {
		return err
	}
	input := map[string]any{
		"applicationSpecifier": map[string]any{
			"bundleIdentifier": map[string]any{"_0": id},
		},
		"options": map[string]any{
			"arguments":                     []any{},
			"environmentVariables":          map[string]any{},
			"platformSpecificOptions":       opts.Bytes(),
			"standardIOUsesPseudoterminals": true,
			"startStopped":                  false,
			"terminateExisting":             false,
			"user":                          map[string]any{"active": true},
			"workingDirectory":              nil,
		},
		"standardIOIdentifiers": map[string]any{},
	}
	svc, err := dialCoreDevice(d.l.entry, appService)
	if err != nil {
		return err
	}
	defer svc.Close()
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := svc.invoke(c, featureLaunchApplication, "", input); err != nil {
		return fmt.Errorf("launch %s: %w", id, err)
	}
	return nil
}

// resolveApp turns a bundle id or display name into a bundle id.
func (d *Device) resolveApp(ctx context.Context, app string) (string, error) {
	app = strings.TrimSpace(app)
	if app == "" {
		return "", fmt.Errorf("no app given: %w", driver.ErrNotFound)
	}
	apps, err := d.Apps(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range apps {
		if a.ID == app {
			return a.ID, nil
		}
	}
	for _, a := range apps {
		if strings.EqualFold(a.Name, app) || strings.EqualFold(a.ID, app) {
			return a.ID, nil
		}
	}
	return "", fmt.Errorf("app %q: %w", app, driver.ErrNotFound)
}
