package ui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/video"
)

// Options configure the client.
type Options struct {
	// Version shows in Settings › Advanced and the About panel.
	Version string
	// Theme forces "light" or "dark"; empty follows the system.
	Theme string
	// Shortcut is the global Stop all shortcut, in MyGo's accelerator
	// syntax. Empty is Option-Command-Period on macOS and
	// Ctrl+Alt+Period elsewhere; "-" registers none.
	Shortcut string
	// NoTray leaves out the menu bar or notification area icon.
	NoTray bool
	// Menus are extra menus for the menu bar, after the client's own; the
	// demo adds its Demo menu.
	Menus []*mygo.MenuItem
	// OnReady runs on the main thread once the main window exists.
	OnReady func(main *mygo.Window)
	// Settings are the Safety, Privacy and Advanced choices to start with;
	// nil is the defaults. gateway.API has no settings methods yet, so the
	// client keeps them and reports changes through OnSettings.
	Settings   *Settings
	OnSettings func(Settings)
	// RulesFile is the policy rules file that Settings › Advanced opens.
	RulesFile string
	// Agents lists the agents for Settings › Agents; nil shows those seen
	// in tasks.
	Agents []AgentInfo
	// StartPage opens "activity", "settings" or "add-phone" at start
	// instead of the phones.
	StartPage string
	// Hidden starts with only the menu bar icon and no window, as when an
	// agent launches Pinky through `pinky mcp`. The window opens by itself
	// when the first Needs you item arrives, and from the menu bar icon.
	Hidden bool
}

// Settings are what the Settings screen changes.
type Settings struct {
	Preset             int // 1: payments, messages and deletions (recommended); 2: payments only; 3: every action
	BlockedApps        []BlockedApp
	ReadOnly           bool
	UnlockFromComputer bool
	HideCodes          bool
	KeepDays           int
	Record             bool
	DeveloperMode      bool
}

// BlockedApp is an app agents may not open.
type BlockedApp struct {
	Name, Kind string
}

// AgentInfo is a row of Settings › Agents.
type AgentInfo struct {
	Name      string
	Scope     string // "Can use Dinely, Chatter and Calendar"
	Connected bool
}

// DefaultSettings are the design's defaults.
func DefaultSettings() Settings {
	return Settings{
		Preset: 1,
		BlockedApps: []BlockedApp{
			{Name: "Northbank", Kind: "Banking"}, {Name: "Vaultly", Kind: "Passwords"}, {Name: "Coinpouch", Kind: "Wallet"},
		},
		UnlockFromComputer: true, HideCodes: true, KeepDays: 30, Record: true,
	}
}

type page int

const (
	pagePhones page = iota
	pageActivity
	pageSettings
	pageAddPhone
)

// allPhones is the sidebar row that shows every phone.
const allPhones = "\x00all"

// app is the client's state. Everything in it is read and written on the
// main thread, by the views and by functions posted with post.
type app struct {
	api gateway.API
	opt Options
	ctx context.Context
	end context.CancelFunc

	main   *mygo.Window
	panel  *mygo.Window
	flyout *mygo.Window
	tray   *mygo.Tray

	devices []gateway.DeviceView
	tasks   []gateway.TaskView
	needs   []gateway.NeedView
	unlocks map[string]bool // phones with an open unlock need, by id
	loaded  bool

	page page
	sel  string // the selected phone, or allPhones

	mirrors map[string]*mirror
	shots   map[string]*mg.Bitmap // approval screenshots by need ID

	noting   map[string]bool   // needs whose Deny with a note is open
	notes    map[string]string // the notes
	details  map[string]bool   // tasks whose technical details show
	answered []answer          // recent answers the approval panel shows
	panelIdx int               // the approval the floating panel shows
	notified map[string]bool   // needs a notification went out for
	seenNeed map[string]bool

	settings Settings
	act      activityState
	set      settingsState
	add      addState
	more     map[string]bool // the More phone controls popover, per device
	toasts   []string

	quitting  bool
	focused   bool // the main window has the keyboard
	shortcut  string
	videoErr  error
	buildSeq  int
	updating  atomic.Bool
	callMu    sync.Mutex
	callQueue map[string]chan func()
	postHook  func(func()) // tests run posted functions themselves
	shownOnce bool         // the main window has shown (Options.Hidden)

	mediaCache map[string]*mediaEntry // step screenshots by media id
	previews   map[string]bool        // steps whose screenshot shows in a task card
	agents     agentsState
	wantScope  sync.Map // agent name → the gateway.Scope the person chose last
}

// Run starts the client and returns when it quits. It owns the main
// thread, as MyGo's App.Run does.
func Run(api gateway.API, opt Options) error {
	a := newApp(api, opt)
	if !mygo.App.IsPackaged() {
		// A packaged app keeps the name its bundle was built with.
		mygo.App.SetName("Pinky")
	}
	if opt.Version != "" {
		mygo.App.SetVersion(opt.Version)
	}
	switch opt.Theme {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	}
	if opt.Hidden && runtime.GOOS == "darwin" {
		// No Dock icon until the window first shows.
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	mygo.App.OnWindowAllClosed(func() {}) // closing the window keeps the gateway in the menu bar
	mygo.App.OnActivate(func(bool) { a.showMain() })
	mygo.App.OnNotificationClick(func(id string) { a.openNeed(id) })
	mygo.App.WhenReady(a.ready)
	err := mygo.App.Run()
	a.shutdown()
	return err
}

func newApp(api gateway.API, opt Options) *app {
	ctx, end := context.WithCancel(context.Background())
	a := &app{
		api: api, opt: opt, ctx: ctx, end: end,
		sel:     "",
		mirrors: map[string]*mirror{}, shots: map[string]*mg.Bitmap{},
		noting: map[string]bool{}, notes: map[string]string{}, details: map[string]bool{},
		notified: map[string]bool{}, seenNeed: map[string]bool{}, unlocks: map[string]bool{}, more: map[string]bool{},
		callQueue:  map[string]chan func(){},
		mediaCache: map[string]*mediaEntry{}, previews: map[string]bool{},
	}
	if opt.Settings != nil {
		a.settings = *opt.Settings
	} else {
		a.settings = DefaultSettings()
	}
	a.act.init()
	switch opt.StartPage {
	case "activity":
		a.page = pageActivity
	case "settings":
		a.page = pageSettings
	case "add-phone":
		a.page = pageAddPhone
	}
	return a
}

func (a *app) ready() {
	keepAppAwake()
	// Load FFmpeg now, so a missing one is reported at start rather than
	// as the first mirror opens.
	if err := video.Load(); err != nil {
		a.videoErr = err
		log.Printf("pinky: %v", err)
	}
	// Read the views before the window shows, so its first frame has the
	// phones rather than an empty sidebar; watch keeps them current.
	a.devices, a.tasks = a.api.Devices(), a.api.Tasks()
	a.setNeeds(a.api.Needs())
	a.loaded = true
	a.afterRefresh(true)

	a.main = mygo.NewWindow(mygo.WindowOptions{
		Title: "Pinky", Width: 1280, Height: 820, MinWidth: 960, MinHeight: 620,
		StateKey: "main", TitleBarStyle: mygo.TitleBarHidden, Hidden: a.opt.Hidden,
		TrafficLightPosition: &mygo.Point{X: 18, Y: 17},
		BackgroundColor:      "light-dark(#f7f7f8, #212125)",
		Content:              mg.View(a.view),
	})
	if runtime.GOOS != "darwin" {
		a.main.SetIcon(iconPNG())
	}
	a.main.OnClose(func(e *mygo.CloseEvent) {
		if !a.quitting {
			e.PreventDefault()
			a.main.Hide()
		}
	})
	// Hidden or minimized, the mirrors stop streaming; shown again, the
	// view builds anew and opens them.
	a.main.OnHide(a.closeMirrors)
	a.main.OnMinimize(a.closeMirrors)
	a.main.OnShow(a.main.Invalidate)
	a.main.OnRestore(a.main.Invalidate)
	a.main.OnFocus(func() { a.focused = true; a.syncPanel() })
	a.main.OnBlur(func() { a.focused = false; a.syncPanel() })
	a.focused = a.main.IsFocused()

	a.newPanel()
	if !a.opt.NoTray {
		a.newTray()
	}
	a.registerShortcut()
	a.setMenu()
	a.updateTray()
	// Started hidden while something already waits (an agent's first
	// connection, say): open for it now.
	if len(a.needs) > 0 {
		a.openForNeed(a.needs[0])
	}
	if a.opt.OnReady != nil {
		a.opt.OnReady(a.main)
	}
	go a.watch()
}

func (a *app) shutdown() {
	a.end()
	for id, m := range a.mirrors {
		m.close()
		delete(a.mirrors, id)
	}
	mygo.GlobalShortcut.UnregisterAll()
}

// post runs fn on the main thread and redraws every window.
func (a *app) post(fn func()) {
	if a.postHook != nil {
		a.postHook(fn) // tests, which have no window
		return
	}
	if a.main == nil {
		return
	}
	a.main.Update(func() {
		fn()
		a.invalidate()
	})
}

// invalidate redraws the windows other than the main one, which Update
// redraws itself.
func (a *app) invalidate() {
	for _, w := range []*mygo.Window{a.panel, a.flyout} {
		if w != nil && !w.IsDestroyed() {
			w.Invalidate()
		}
	}
}

// watch reads the gateway's views and follows its events, a few at a
// time, until the client quits.
func (a *app) watch() {
	events := a.api.Subscribe(a.ctx)
	a.refresh(true, true, true)
	for {
		var ev gateway.Event
		var ok bool
		select {
		case ev, ok = <-events:
		case <-a.ctx.Done():
			return
		}
		if !ok {
			return
		}
		devs, tasks, needs := ev.Kind == gateway.EventDevices, ev.Kind == gateway.EventTasks, ev.Kind == gateway.EventNeeds
		// Events come in bursts: take what else arrives within a frame.
		timeout := time.After(16 * time.Millisecond)
	drain:
		for {
			select {
			case ev, ok = <-events:
				if !ok {
					break drain
				}
				switch ev.Kind {
				case gateway.EventDevices:
					devs = true
				case gateway.EventTasks:
					tasks = true
				case gateway.EventNeeds:
					needs = true
				}
			case <-timeout:
				break drain
			}
		}
		a.refresh(devs, tasks, needs)
	}
}

func (a *app) refresh(devs, tasks, needs bool) {
	var d []gateway.DeviceView
	var t []gateway.TaskView
	var n []gateway.NeedView
	if devs {
		d = a.api.Devices()
	}
	if tasks {
		t = a.api.Tasks()
	}
	if needs {
		n = a.api.Needs()
	}
	a.post(func() {
		if devs {
			a.devices = d
		}
		if tasks {
			a.tasks = t
		}
		if needs {
			a.setNeeds(n)
		}
		first := !a.loaded
		a.loaded = true
		a.afterRefresh(first)
	})
}

// afterRefresh keeps the selection valid and opens Add phone on a first
// run without phones.
func (a *app) afterRefresh(first bool) {
	if first && len(a.devices) == 0 && a.page == pagePhones {
		a.page = pageAddPhone
	}
	if a.sel == allPhones && len(a.devices) < 2 {
		a.sel = ""
	}
	if a.sel != allPhones && a.device(a.sel) == nil {
		a.sel = ""
		if len(a.devices) > 0 {
			a.sel = a.devices[0].Info.ID
		}
	}
	a.updateTray()
	a.syncPanel()
}

func (a *app) setNeeds(n []gateway.NeedView) {
	sort.SliceStable(n, func(i, j int) bool { return n[i].Created.Before(n[j].Created) })
	// A handoff on a locked phone shows as one card, with the unlock
	// button in it, rather than as a handoff and an unlock card.
	a.unlocks = map[string]bool{}
	handoff := map[string]bool{}
	for _, x := range n {
		switch x.Kind {
		case gateway.NeedUnlock:
			a.unlocks[x.DeviceID] = true
		case gateway.NeedHandoff:
			handoff[x.DeviceID] = true
		}
	}
	shown := n[:0:0]
	for _, x := range n {
		if x.Kind != gateway.NeedUnlock || !handoff[x.DeviceID] {
			shown = append(shown, x)
		}
	}
	n = shown
	// Settings › Agents reads the agents again when a first connection is
	// asked or answered.
	asks := 0
	for _, x := range n {
		if newAgentNeed(x) {
			asks++
		}
	}
	if asks != a.agents.asks {
		a.agents.asks, a.agents.lastLoad = asks, time.Time{}
	}
	a.needs = n
	live := map[string]bool{}
	for _, x := range n {
		live[x.ID] = true
		if !a.seenNeed[x.ID] {
			a.seenNeed[x.ID] = true
			a.notify(x)
			a.openForNeed(x)
		}
	}
	for id := range a.shots {
		if !live[id] {
			delete(a.shots, id)
		}
	}
	for id := range a.noting {
		if !live[id] {
			delete(a.noting, id)
			delete(a.notes, id)
		}
	}
	if a.panelIdx >= len(a.approvals()) {
		a.panelIdx = max(0, len(a.approvals())-1)
	}
}

func (a *app) device(id string) *gateway.DeviceView {
	for i := range a.devices {
		if a.devices[i].Info.ID == id {
			return &a.devices[i]
		}
	}
	return nil
}

func (a *app) taskOf(deviceID string) *gateway.TaskView {
	// The running task, or the one that ended last.
	var best *gateway.TaskView
	for i := range a.tasks {
		t := &a.tasks[i]
		if t.DeviceID != deviceID {
			continue
		}
		if running(t.State) {
			return t
		}
		if best == nil || t.Started.After(best.Started) {
			best = t
		}
	}
	return best
}

// approvals are the needs the floating panel shows: approvals and
// handoffs, everything but unlocks.
func (a *app) approvals() []gateway.NeedView {
	var out []gateway.NeedView
	for _, n := range a.needs {
		if n.Kind != gateway.NeedUnlock {
			out = append(out, n)
		}
	}
	return out
}

func (a *app) needsOf(deviceID string) []gateway.NeedView {
	var out []gateway.NeedView
	for _, n := range a.needs {
		if n.DeviceID == deviceID {
			out = append(out, n)
		}
	}
	return out
}

// call runs a gateway call off the main thread, one at a time per
// device so the person's input keeps its order, and shows its error.
func (a *app) call(deviceID, what string, fn func() error) {
	a.callMu.Lock()
	q, ok := a.callQueue[deviceID]
	if !ok {
		q = make(chan func(), 256)
		a.callQueue[deviceID] = q
		go func() {
			for f := range q {
				f()
			}
		}()
	}
	a.callMu.Unlock()
	job := func() {
		if err := fn(); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("pinky: %s: %v", what, err)
			a.post(func() { a.toasts = append(a.toasts, fmt.Sprintf("Couldn't %s: %v", what, err)) })
		}
	}
	select {
	case q <- job:
	default:
		// The gateway stopped answering; drop rather than block the UI.
	}
}

// --- actions ---

func (a *app) selectPhone(id string) {
	a.page = pagePhones
	a.sel = id
}

func (a *app) decide(n gateway.NeedView, d gateway.Decision, note string) {
	a.remember(n, d, note)
	a.call(n.DeviceID, "answer the approval", func() error { return a.api.Decide(n.ID, d, note) })
	if d == gateway.TakeOver {
		a.selectPhone(n.DeviceID)
		a.showMain()
	}
}

func (a *app) takeOver(id string) {
	a.call(id, "take over", func() error { return a.api.TakeOver(id) })
	a.selectPhone(id)
}

func (a *app) handBack(id string) {
	a.call(id, "hand back", func() error { return a.api.HandBack(id, "") })
}

func (a *app) stop(id string) {
	a.call(id, "stop the task", func() error { return a.api.Stop(id) })
}

func (a *app) unlock(id string) {
	a.call(id, "unlock the phone", func() error { return a.api.Unlock(id) })
}

// stopAll is the emergency stop, from the sidebar, the flyout and the
// global shortcut.
func (a *app) stopAll() {
	a.call("", "stop all", a.api.StopAll)
}

func (a *app) showMain() {
	if a.main == nil {
		return
	}
	a.regularApp()
	if a.main.IsMinimized() {
		a.main.Restore()
	}
	a.main.Show()
	a.main.Focus()
}

// openForNeed opens the window of an app started hidden when something
// first needs the person, without taking the keyboard from the app in use.
func (a *app) openForNeed(n gateway.NeedView) {
	if !a.opt.Hidden || a.main == nil || a.main.IsVisible() || a.shownOnce {
		return
	}
	a.selectPhone(n.DeviceID)
	a.regularApp()
	a.main.ShowInactive()
}

// regularApp gives a hidden-started app its Dock icon once a window shows.
func (a *app) regularApp() {
	if a.opt.Hidden && !a.shownOnce && runtime.GOOS == "darwin" {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	}
	a.shownOnce = true
}

// openNeed brings the main window up at the phone a need is about.
func (a *app) openNeed(id string) {
	for _, n := range a.needs {
		if n.ID == id {
			a.selectPhone(n.DeviceID)
		}
	}
	a.showMain()
	if a.main != nil {
		a.main.Invalidate()
	}
}

// notify sends a system notification for a new need while the main
// window is not in front.
func (a *app) notify(n gateway.NeedView) {
	if a.main == nil || a.notified[n.ID] || a.focused || !mygo.NotificationsSupported() {
		return
	}
	a.notified[n.ID] = true
	title := n.Title
	sub := n.DeviceName
	if n.App != "" {
		sub += " · " + n.App
	}
	nt := mygo.NewNotification(mygo.NotificationOptions{ID: n.ID, Group: "needs", Title: title, Subtitle: sub, Body: n.Detail})
	nt.OnClick(func() {
		if n.Kind != gateway.NeedUnlock {
			a.showPanel(true)
		} else {
			a.openNeed(n.ID)
		}
		nt.Close()
	})
	go func() {
		if err := nt.Show(); err != nil && !errors.Is(err, mygo.ErrNotificationsDenied) {
			log.Printf("pinky: notification: %v", err)
		}
	}()
}

func (a *app) registerShortcut() {
	acc := a.opt.Shortcut
	if acc == "-" {
		return
	}
	if acc == "" {
		acc = "CmdOrCtrl+Alt+."
	}
	if err := mygo.GlobalShortcut.Register(acc, a.stopAll); err != nil {
		log.Printf("pinky: global shortcut %s: %v", acc, err)
		return
	}
	a.shortcut = acc
}

// shortcutLabel is how the sidebar and the flyout write the shortcut.
func shortcutLabel() string {
	if runtime.GOOS == "darwin" {
		return "⌥⌘."
	}
	return "Ctrl+Alt+."
}

func (a *app) setMenu() {
	items := []*mygo.MenuItem{}
	if runtime.GOOS == "darwin" {
		items = append(items, &mygo.MenuItem{Role: mygo.RoleAppMenu})
	}
	items = append(items,
		&mygo.MenuItem{Role: mygo.RoleEditMenu},
		&mygo.MenuItem{Label: "Phones", Submenu: []*mygo.MenuItem{
			{Label: "Activity", Accelerator: "CmdOrCtrl+1", Click: func(*mygo.MenuItem, *mygo.Window) { a.goPage(pageActivity) }},
			{Label: "Settings…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.goPage(pageSettings) }},
			{Label: "Add Phone…", Accelerator: "CmdOrCtrl+N", Click: func(*mygo.MenuItem, *mygo.Window) { a.goPage(pageAddPhone) }},
			mygo.Separator(),
			{Label: "Stop All", Click: func(*mygo.MenuItem, *mygo.Window) { a.stopAll() }},
		}},
	)
	items = append(items, a.opt.Menus...)
	if debugMenu != nil {
		items = append(items, debugMenu(a))
	}
	items = append(items, &mygo.MenuItem{Role: mygo.RoleWindowMenu})
	mygo.App.SetMenu(mygo.NewMenu(items))
}

func (a *app) goPage(p page) {
	a.page = p
	a.showMain()
	if a.main != nil {
		a.main.Invalidate()
	}
}

// debugMenu adds a Debug menu in test builds (tag e2e).
var debugMenu func(a *app) *mygo.MenuItem

// logf is for errors nobody needs to act on.
func logf(format string, args ...any) {
	if os.Getenv("PINKY_UI_DEBUG") != "" {
		log.Printf(format, args...)
	}
}
