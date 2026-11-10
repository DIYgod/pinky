// Package secret keeps phone PINs in the operating system's keychain and
// hands one out only after the person confirms with Touch ID (or the Mac's
// password), Windows Hello, or the system's equivalent. Pinky never writes a
// PIN anywhere else, never logs it and never gives it to agents.
package secret

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
)

// Errors.
var (
	ErrNotFound    = errors.New("secret: nothing saved")
	ErrUnsupported = errors.New("secret: no supported keychain on this system")
	ErrCancelled   = errors.New("secret: the person cancelled")
	// ErrMicrosoftAccount: Windows asked for the password of an account
	// connected to a Microsoft account and got none, or only wrong ones.
	// Such an account always has a password, even when the person signs
	// in with a PIN or Windows Hello.
	ErrMicrosoftAccount = errors.New("secret: this Windows account is a Microsoft account, which has a password even when you sign in with a PIN or Windows Hello: type the Microsoft account's password")
)

var logger atomic.Pointer[slog.Logger]

// SetLogger sends what the system's checks decide to l: which way the
// person confirmed, and why a check failed. It never logs a secret or a
// password.
func SetLogger(l *slog.Logger) { logger.Store(l) }

func log() *slog.Logger {
	if l := logger.Load(); l != nil {
		return l
	}
	return slog.New(slog.DiscardHandler)
}

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

// Service is the keychain service (the Credential Manager's target
// prefix on Windows) Pinky's secrets are saved under.
const Service = "app.pinky.desktop"

// System returns the operating system's keychain, or a Store whose methods
// return ErrUnsupported where Pinky has none.
func System() Store { return system() }

// Supported reports whether a store can keep secrets on this system,
// without asking the person anything.
func Supported(s Store) bool {
	if v, ok := s.(interface{ Supported() bool }); ok {
		return v.Supported()
	}
	return true
}

// PINAccount is the account a phone's PIN is saved under.
func PINAccount(deviceID string) string { return "pin:" + deviceID }

// Memory is a Store in memory, for tests. Confirm stands in for the
// system's prompt; nil confirms.
type Memory struct {
	Confirm func(reason string) error

	mu sync.Mutex
	m  map[string]string
}

// Supported implements the optional method Supported looks for.
func (s *Memory) Supported() bool { return true }

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

func (unsupported) Supported() bool           { return false }
func (unsupported) Has(string) bool           { return false }
func (unsupported) Save(string, string) error { return ErrUnsupported }
func (unsupported) Delete(string) error       { return ErrUnsupported }
func (unsupported) Reveal(context.Context, string, string) (string, error) {
	return "", ErrUnsupported
}
