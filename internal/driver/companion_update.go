package driver

import (
	"context"
	"errors"
)

// CompanionUpdate says what an automatic update of the companion app did.
type CompanionUpdate struct {
	// From and To are the versions before and after, as people read them:
	// "0.1.0 (75)".
	From, To string
	// Updated is false when the phone already had the bundled build or a
	// newer one.
	Updated bool
	// ServiceOn: the app's accessibility service is on afterwards. An
	// update keeps it as it was: on stays on, off stays off.
	ServiceOn bool
}

// CompanionUpdater updates an installed companion app without asking the
// person, for automatic updates while the phone is free. Unlike
// CompanionInstaller it never installs an app that is not there, never
// turns the service on for a person who turned it off, and refuses with
// ErrNeedsPerson when the phone would ask its owner (Google Play Protect
// checks installs over USB, or the installed app is signed with another
// key and has to be replaced).
type CompanionUpdater interface {
	UpdateCompanion(ctx context.Context) (CompanionUpdate, error)
}

// ErrNeedsPerson means the update would ask the person on the phone, so it
// waits for them to click Update.
var ErrNeedsPerson = errors.New("the phone would ask its owner to allow the update")
