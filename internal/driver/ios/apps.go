package ios

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/danielpaulus/go-ios/ios/installationproxy"
	"github.com/danielpaulus/go-ios/ios/springboard"
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
	list, err := d.listApps(ctx)
	return list, d.gone(err)
}

// listApps is Apps without marking a dropped connection (gone).
func (d *Device) listApps(ctx context.Context) ([]driver.App, error) {
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
	// The home screen names apps in the phone's language, while the
	// bundle's own name may be English ("WeChat" for 微信): agents and
	// people use the home screen's.
	if names, err := homeScreenNames(l, homeScreenWait); err == nil {
		addHomeScreenNames(out, names)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, byPath, nil
}

// homeScreenWait bounds reading the home screen's names: the app list
// waits for it, and does without when SpringBoard does not answer.
const homeScreenWait = 3 * time.Second

// homeScreenNames reads the names the home screen shows, by bundle id,
// from SpringBoard's icon layout: apps on its pages, in the dock and in
// folders. Apps only in the App Library are not in it.
func homeScreenNames(l *link, wait time.Duration) (map[string]string, error) {
	type result struct {
		names map[string]string
		err   error
	}
	ch := make(chan result, 1)
	var mu sync.Mutex
	var client *springboard.Client
	timedOut := false
	go func() {
		c, err := springboard.NewClient(l.base)
		if err != nil {
			ch <- result{err: err}
			return
		}
		mu.Lock()
		if timedOut {
			mu.Unlock()
			c.Close()
			return
		}
		client = c
		mu.Unlock()
		defer c.Close()
		layout, err := c.GetIconLayout("2")
		if err != nil {
			ch <- result{err: err}
			return
		}
		names := map[string]string{}
		collectIconNames(layout, names, 0)
		ch <- result{names: names}
	}()
	select {
	case r := <-ch:
		return r.names, r.err
	case <-time.After(wait):
		mu.Lock()
		timedOut = true
		if client != nil {
			client.Close() // ends the read that waits
		}
		mu.Unlock()
		return nil, fmt.Errorf("SpringBoard did not answer in %v", wait)
	}
}

// collectIconNames walks an icon layout (pages of icons, folders with
// pages of their own) for icons with a bundle id and a display name.
func collectIconNames(v any, names map[string]string, depth int) {
	if depth > 12 {
		return
	}
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			collectIconNames(e, names, depth+1)
		}
	case map[string]any:
		id, _ := x["bundleIdentifier"].(string)
		name, _ := x["displayName"].(string)
		if id != "" && strings.TrimSpace(name) != "" {
			names[id] = strings.TrimSpace(name)
		}
		for _, e := range x {
			switch e.(type) {
			case []any, map[string]any:
				collectIconNames(e, names, depth+1)
			}
		}
	}
}

// addHomeScreenNames gives apps whose home screen name differs from their
// own that name as an alias.
func addHomeScreenNames(apps []driver.App, names map[string]string) {
	for i := range apps {
		n := names[apps[i].ID]
		if n == "" || strings.EqualFold(n, apps[i].Name) {
			continue
		}
		apps[i].Aliases = append(apps[i].Aliases, n)
	}
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

// safariID is the app an http or https link opens in.
const safariID = "com.apple.mobilesafari"

// LinkApp is the app that opens the links Launch is given, for the rules
// that judge apps (the gateway's linkOpener).
func (d *Device) LinkApp() driver.App {
	return driver.App{ID: safariID, Name: "Safari"}
}

// Launch implements driver.Device. app is a bundle id, a display name or an
// http or https link, which Safari opens. A running app is brought to the
// front, not restarted.
func (d *Device) Launch(ctx context.Context, app string) error {
	return d.gone(d.launch(ctx, app))
}

// launch is Launch without marking a dropped connection (gone).
func (d *Device) launch(ctx context.Context, app string) error {
	link, isLink, err := linkTarget(app)
	if err != nil {
		return err
	}
	id := safariID
	if !isLink {
		if id, err = d.resolveApp(ctx, app); err != nil {
			return err
		}
	}
	d.markInput()
	input, err := launchInput(id, link)
	if err != nil {
		return err
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

// linkTarget tells whether app names a link rather than an app, which
// only http and https links may.
func linkTarget(app string) (link string, isLink bool, err error) {
	app = strings.TrimSpace(app)
	if !strings.Contains(app, "://") {
		return "", false, nil
	}
	u, err := url.Parse(app)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", true, fmt.Errorf("only http and https links can be opened: %w", driver.ErrUnsupported)
	}
	return app, true, nil
}

// launchInput is the launch request for an app, with the link the app opens
// when it comes up (payloadURL, as in devicectl's --payload-url).
func launchInput(id, link string) (map[string]any, error) {
	var platform bytes.Buffer
	if err := plist.NewBinaryEncoder(&platform).Encode(map[string]any{}); err != nil {
		return nil, err
	}
	opts := map[string]any{
		"arguments":                     []any{},
		"environmentVariables":          map[string]any{},
		"platformSpecificOptions":       platform.Bytes(),
		"standardIOUsesPseudoterminals": true,
		"startStopped":                  false,
		"terminateExisting":             false,
		"user":                          map[string]any{"active": true},
		"workingDirectory":              nil,
	}
	if link != "" {
		opts["payloadURL"] = map[string]any{"relative": link}
	}
	return map[string]any{
		"applicationSpecifier": map[string]any{
			"bundleIdentifier": map[string]any{"_0": id},
		},
		"options":               opts,
		"standardIOIdentifiers": map[string]any{},
	}, nil
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
