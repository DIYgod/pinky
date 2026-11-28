//go:build !darwin

package harmony

import "context"

// Other hosts discover through hdc. USB-descriptor discovery without the
// SDK is currently available only on macOS.
func usbTargets(context.Context) ([]string, error) { return nil, nil }
