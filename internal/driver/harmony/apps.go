package harmony

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

var bundleID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)

func containsFailure(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "error") || strings.Contains(s, "failed") || strings.Contains(s, "inaccessible or not found") || strings.Contains(s, "usage:")
}

func (d *Device) label(ctx context.Context, id string) string {
	if d.labels == nil {
		d.labels = map[string]string{}
	}
	if label := d.labels[id]; label != "" {
		return label
	}
	label := d.readLabel(ctx, id)
	d.labels[id] = label
	return label
}

func (d *Device) readLabel(ctx context.Context, id string) string {
	label := id
	if id == "com.ohos.sceneboard" {
		label = "Home Screen"
	} else if bundleID.MatchString(id) {
		if out, err := d.shell(ctx, "bm", "dump", "-l", "-n", id); err == nil {
			if value := strings.TrimSpace(string(out)); value != "" && !containsFailure(value) && !strings.HasPrefix(value, "$string:") {
				label = value
			}
		}
	}
	return label
}

func (d *Device) Apps(ctx context.Context) ([]driver.App, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.apps != nil && time.Since(d.appsAt) < time.Minute {
		return append([]driver.App(nil), d.apps...), nil
	}
	out, err := d.shell(ctx, "bm", "dump", "-a")
	if err != nil {
		return nil, err
	}
	if containsFailure(string(out)) {
		return nil, fmt.Errorf("harmony: cannot list applications: %s", out)
	}
	var apps []driver.App
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		id := strings.TrimSpace(line)
		if !bundleID.MatchString(id) || seen[id] {
			continue
		}
		seen[id] = true
		apps = append(apps, driver.App{ID: id, Name: id})
	}
	// Resolve labels in a bounded batch. On a real phone a sequential
	// list of 300 labels took 55 s and blocked the first screen read.
	jobs := make(chan int, len(apps))
	for i, a := range apps {
		if label := d.labels[a.ID]; label != "" {
			apps[i].Name = label
		} else {
			jobs <- i
		}
	}
	close(jobs)
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				apps[i].Name = d.readLabel(ctx, apps[i].ID)
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if d.labels == nil {
		d.labels = map[string]string{}
	}
	for _, a := range apps {
		d.labels[a.ID] = a.Name
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	d.apps, d.appsAt = apps, time.Now()
	return append([]driver.App(nil), apps...), nil
}

type bundle struct {
	Name    string `json:"name"`
	Modules []struct {
		Name      string `json:"name"`
		Main      string `json:"mainElementName"`
		Abilities []struct {
			Name    string `json:"name"`
			Visible bool   `json:"visible"`
			Skills  []struct {
				Actions []string `json:"actions"`
			} `json:"skills"`
		} `json:"abilityInfos"`
	} `json:"hapModuleInfos"`
}

func mainAbility(out []byte) (module, ability string, err error) {
	start := strings.IndexByte(string(out), '{')
	if start < 0 {
		return "", "", fmt.Errorf("harmony: missing bundle metadata")
	}
	var b bundle
	if err = json.Unmarshal(out[start:], &b); err != nil {
		return "", "", err
	}
	for _, m := range b.Modules {
		for _, a := range m.Abilities {
			if a.Visible && (a.Name == m.Main || strings.HasSuffix(m.Main, "."+a.Name)) {
				return m.Name, a.Name, nil
			}
		}
	}
	return "", "", fmt.Errorf("harmony: no public main ability")
}

func (d *Device) Launch(ctx context.Context, app string) error {
	if !bundleID.MatchString(app) {
		apps, err := d.Apps(ctx)
		if err != nil {
			return err
		}
		var id string
		for _, a := range apps {
			if strings.EqualFold(a.Name, app) {
				if id != "" {
					return fmt.Errorf("harmony: ambiguous app name")
				}
				id = a.ID
			}
		}
		if id == "" {
			return fmt.Errorf("harmony: app %q: %w", app, driver.ErrNotFound)
		}
		app = id
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.requireUnlocked(ctx); err != nil {
		return err
	}
	out, err := d.shell(ctx, "bm", "dump", "-n", app)
	if err != nil {
		return err
	}
	module, ability, err := mainAbility(out)
	if err != nil {
		return err
	}
	out, err = d.shell(ctx, "aa", "start", "-b", app, "-a", ability, "-m", module)
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), "start ability successfully") {
		return fmt.Errorf("harmony: launch did not succeed: %s", out)
	}
	return nil
}
