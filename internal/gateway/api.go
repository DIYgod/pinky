// Package gateway is the core of Pinky. It holds every device connection,
// runs the action pipeline (lease, policy, approval, execute, settle,
// record) and tells clients what changed.
package gateway

import (
	"context"
	"image"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// API is what the desktop client and remote consoles use. Agents never get
// it: the MCP server talks to the gateway's agent-facing methods, so an
// agent can never approve, stop or take over.
type API interface {
	Devices() []DeviceView
	Tasks() []TaskView
	Needs() []NeedView
	History(ctx context.Context, q HistoryQuery) ([]TaskView, error)

	// Subscribe delivers change notifications until ctx ends. Clients read
	// the views again when an event arrives; events carry no state.
	Subscribe(ctx context.Context) <-chan Event

	// Decide answers a NeedView of kind NeedApproval.
	Decide(needID string, d Decision, note string) error
	// Unlock answers a NeedView of kind NeedUnlock with the saved PIN.
	Unlock(deviceID string) error
	TakeOver(deviceID string) error
	HandBack(deviceID, note string) error
	Stop(deviceID string) error
	StopAll() error

	// Stream returns the device's live video for the mirror.
	Stream(ctx context.Context, deviceID string, opt driver.StreamOptions) (driver.Stream, error)
	// Person input on the mirror. Any of these pauses an agent task on that
	// device until HandBack.
	Touch(deviceID string, a driver.TouchAction, p screen.Point) error
	Key(deviceID string, k driver.Key) error
	Text(deviceID string, text string) error
}

// DeviceStatus is what the sidebar dot and status line show.
type DeviceStatus string

// Device statuses, in the order the UI ranks them.
const (
	DeviceNeedsYou  DeviceStatus = "needs_you"
	DeviceLocked    DeviceStatus = "locked"
	DeviceInControl DeviceStatus = "in_control" // the person took over
	DeviceWorking   DeviceStatus = "working"
	DeviceIdle      DeviceStatus = "idle"
	DeviceOffline   DeviceStatus = "offline"
)

// DeviceView is one row of the sidebar.
type DeviceView struct {
	Info       driver.Info
	Status     DeviceStatus
	StatusLine string // plain words: "Claude Code · Reorder lunch", "Idle", "Locked", "Offline"
	TaskID     string // the running task, if any
}

// TaskState is the state of an agent task.
type TaskState string

// Task states.
const (
	TaskWorking TaskState = "working"
	TaskWaiting TaskState = "waiting" // waiting for the person
	TaskPaused  TaskState = "paused"  // the person took over
	TaskDone    TaskState = "done"
	TaskStopped TaskState = "stopped"
	TaskFailed  TaskState = "failed"
)

// TaskView is a task card, or a row in Activity.
type TaskView struct {
	ID         string
	DeviceID   string
	DeviceName string
	Agent      string // "Claude Code"
	Title      string // the task name the agent gave
	State      TaskState
	Started    time.Time
	Ended      time.Time // zero while running
	Steps      []StepView
}

// StepView is one step of a task.
type StepView struct {
	Index   int
	Text    string // plain language: "Opened Dinely", "Waiting for you: pay $12.80"
	Tool    string // Developer mode only: "phone_tap"
	Args    string // Developer mode only: the arguments as JSON
	Status  string // settled, uncertain, blocked_by_policy, approval_pending, ...
	At      time.Time
	Elapsed time.Duration
	Before  string // media id of the screenshot before the step, may be empty
	After   string // media id of the screenshot after the step, may be empty
}

// NeedKind is what a Needs you card asks for.
type NeedKind string

// Need kinds.
const (
	NeedApproval NeedKind = "approval"
	NeedUnlock   NeedKind = "unlock"
)

// NeedView is a Needs you card.
type NeedView struct {
	ID         string
	Kind       NeedKind
	DeviceID   string
	DeviceName string
	App        string // the app's display name
	Agent      string
	TaskID     string
	Title      string // "Pay $12.80?"
	Detail     string // "Claude Code · 1 chicken rice bowl, no cilantro"
	Rule       string // the rule that asked, such as "Payments"
	Created    time.Time
	Deadline   time.Time // when no answer means Deny
	Screenshot image.Image
	Target     screen.Rect // the element the action targets, in Screenshot pixels
}

// Decision answers an approval.
type Decision string

// Decisions.
const (
	AllowOnce   Decision = "allow_once"
	AllowTask   Decision = "allow_task"
	AllowAlways Decision = "allow_always"
	Deny        Decision = "deny"
	TakeOver    Decision = "take_over"
)

// EventKind says which views changed.
type EventKind string

// Event kinds.
const (
	EventDevices EventKind = "devices"
	EventTasks   EventKind = "tasks"
	EventNeeds   EventKind = "needs"
)

// Event is a change notification.
type Event struct {
	Kind     EventKind
	DeviceID string
	TaskID   string
	NeedID   string
}

// HistoryQuery filters Activity.
type HistoryQuery struct {
	Search string
	Before time.Time // zero = now
	Limit  int
}
