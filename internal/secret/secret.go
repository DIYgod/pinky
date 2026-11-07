// Package secret keeps phone PINs in the operating system's keychain and
// hands one out only after the person confirms with Touch ID (or the Mac's
// password), Windows Hello, or the system's equivalent. Pinky never writes a
// PIN anywhere else, never logs it and never gives it to agents.
package secret

import (
	"context"
	"errors"
	"sync"
)

// Errors.
var (
	ErrNotFound    = errors.New("secret: nothing saved")
	ErrUnsupported = errors.New("secret: no supported keychain on this system")
	ErrCancelled   = errors.New("secret: the person cancelled")
)

// Store holds secrets by account name.
type Store interface {
	// Has reports whether a secret is saved, without asking the person.
	Has(account string) bool
	Save(account, secret string) error
	Delete(account string) error
	// Reveal asks the person to confirm, with reason in the system's
	// prompt ("unlock Mi 10 Pro"), then returns the secret. It returns
	// ErrCancelled when they cancel or fail, and ErrNotFound when nothing
	// is saved.
	Reveal(ctx context.Context, account, reason string) (string, error)
}

// System returns the operating system's keychain, or a Store whose methods
// return ErrUnsupported where Pinky has none.
func System() Store { return system() }

// PINAccount is the account a phone's PIN is saved under.
func PINAccount(deviceID string) string { return "pin:" + deviceID }

// Memory is a Store in memory, for tests. Confirm stands in for the
// system's prompt; nil confirms.
type Memory struct {
	Confirm func(reason string) error

	mu sync.Mutex
	m  map[string]string
}

func (s *Memory) Has(account string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[account]
	return ok
}

func (s *Memory) Save(account, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[account] = secret
	return nil
}

func (s *Memory) Delete(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, account)
	return nil
}

func (s *Memory) Reveal(ctx context.Context, account, reason string) (string, error) {
	s.mu.Lock()
	v, ok := s.m[account]
	s.mu.Unlock()
	if !ok {
		return "", ErrNotFound
	}
	if s.Confirm != nil {
		if err := s.Confirm(reason); err != nil {
			return "", err
		}
	}
	return v, nil
}

// unsupported is the Store of systems without a keychain Pinky can use.
type unsupported struct{}

func (unsupported) Has(string) bool           { return false }
func (unsupported) Save(string, string) error { return ErrUnsupported }
func (unsupported) Delete(string) error       { return ErrUnsupported }
func (unsupported) Reveal(context.Context, string, string) (string, error) {
	return "", ErrUnsupported
}
