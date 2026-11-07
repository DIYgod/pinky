// Command secretcheck checks by hand that the system store asks the person
// before it hands out a secret, which no automated test can answer:
//
//	go run ./scripts/secretcheck                 # confirm the prompt: the secret comes back
//	go run ./scripts/secretcheck -expect cancel  # press Cancel (or fail): ErrCancelled
//	go run ./scripts/secretcheck -expect timeout -timeout 5s
//	                                             # leave the prompt: it goes away after 5 s
//
// It saves a made-up secret under a fresh account of Pinky's service, asks
// for it with Reveal, and deletes it again whatever happens. On macOS the
// prompt is Touch ID or the Mac's password; on Windows, Windows Hello or
// the account's password.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/DIYgod/pinky/internal/secret"
)

func main() {
	expect := flag.String("expect", "secret", "what the person will do: secret (confirm), cancel or timeout")
	timeout := flag.Duration("timeout", 2*time.Minute, "how long Reveal may wait for the person")
	flag.Parse()

	var b [6]byte
	rand.Read(b[:])
	account := "check:" + hex.EncodeToString(b[:])
	want := "pin-" + hex.EncodeToString(b[3:])

	s := secret.System()
	if err := s.Save(account, want); err != nil {
		fail("save: %v", err)
	}
	defer func() {
		if err := s.Delete(account); err != nil {
			fmt.Fprintf(os.Stderr, "delete %s: %v\n", account, err)
		}
	}()
	if !s.Has(account) {
		fail("Has(%s) = false after Save", account)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	fmt.Printf("Saved a secret under %s. Asking for it (%s)...\n", account, *expect)
	start := time.Now()
	got, err := s.Reveal(ctx, account, "check that Pinky asks before it reveals a secret")
	took := time.Since(start).Round(10 * time.Millisecond)

	switch {
	case *expect == "secret" && err == nil && got == want:
		fmt.Printf("PASS: the secret came back after the person confirmed (%v).\n", took)
	case *expect == "cancel" && errors.Is(err, secret.ErrCancelled):
		fmt.Printf("PASS: ErrCancelled after the person cancelled (%v).\n", took)
	case *expect == "timeout" && errors.Is(err, context.DeadlineExceeded):
		fmt.Printf("PASS: the prompt went away when the context ended (%v).\n", took)
	case err == nil && got != want:
		fail("FAIL: Reveal returned a different secret")
	default:
		fail("FAIL: expected %s, got secret=%t err=%v (%v)", *expect, err == nil, err, took)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
