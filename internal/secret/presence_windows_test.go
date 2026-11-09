//go:build windows

package secret

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestSignedInBlankAccount checks that signedInBlankAccount turns down
// accounts that are not the signed-in one, or not local. On GitHub's runner
// it also asks Windows about the signed-in account, which has a password:
// that try is an empty password, which counts as a failed sign-in, so it
// does not run on a person's computer.
func TestSignedInBlankAccount(t *testing.T) {
	me, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	name, _, _, err := me.User.Sid.LookupAccount("")
	if err != nil {
		t.Fatal(err)
	}
	u := func(s string) []uint16 { return append(windows.StringToUTF16(s), make([]uint16, 8)...) }
	if signedInBlankAccount(u(name), u("NOT-THIS-COMPUTER")) {
		t.Errorf("an account of another domain counts as the signed-in one")
	}
	if signedInBlankAccount(u("pinky-no-such-user"), u("")) {
		t.Errorf("an account that does not exist counts as the signed-in one")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("asking Windows about the signed-in account counts as a failed sign-in; runs on CI only")
	}
	computer, err := windows.ComputerName()
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"", ".", computer} {
		if signedInBlankAccount(u(name), u(domain)) {
			t.Errorf("%s (domain %q), which has a password, counts as blank", name, domain)
		}
	}
}
