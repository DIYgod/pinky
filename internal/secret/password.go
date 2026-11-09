package secret

import (
	"errors"
	"fmt"
	"syscall"
)

// The Win32 errors of LogonUserW that the Windows password check reads
// (winerror.h). They are here, without a build tag, so that the decision
// below is tested on every system.
const (
	errLogonFailure       = syscall.Errno(1326) // ERROR_LOGON_FAILURE
	errAccountRestriction = syscall.Errno(1327) // ERROR_ACCOUNT_RESTRICTION
)

// passwordVerdict reads LogonUserW's answer to the password typed in the
// Windows credential dialog: true when it is the account's password, false
// with no error when it is wrong (the dialog asks again), and an error for
// anything else. logonErr is nil when LogonUserW succeeded. emptyPassword
// reports that nothing was typed, and signedInBlank whether the dialog's
// account is the signed-in local account with a blank password
// (signedInBlankAccount on Windows); it is called only when it matters.
//
// A local account with a blank password never passes the network logon
// the check uses: Windows' default policy "Accounts: Limit local account
// use of blank passwords to console logon only" (LimitBlankPasswordUse=1)
// refuses it with ERROR_ACCOUNT_RESTRICTION, and so does an interactive
// logon through LogonUserW. Windows gives that error only after the blank
// password has matched. Measured on 2026-11-10 on GitHub's windows-latest
// runner (Windows Server 2025), with the policy on and off: an empty
// password for an account that has a password was ERROR_LOGON_FAILURE in
// every state (enabled, disabled, outside its logon hours, must change
// its password, account expired, with or without "password not
// required"), or ERROR_ACCOUNT_LOCKED_OUT when locked out; a
// blank-password account with another restriction got that restriction's
// error, not this one. So ERROR_ACCOUNT_RESTRICTION for an empty password
// is a correct password when signedInBlankAccount confirms it for the
// signed-in local account. Any other account, or a password that is not
// empty, keeps it an error.
// docs/notes/platform.md has the measurements.
func passwordVerdict(logonErr error, emptyPassword bool, signedInBlank func() bool) (bool, error) {
	switch {
	case logonErr == nil:
		return true, nil
	case errors.Is(logonErr, errLogonFailure):
		return false, nil
	case errors.Is(logonErr, errAccountRestriction) && emptyPassword && signedInBlank():
		return true, nil
	}
	return false, fmt.Errorf("secret: check the password: %w", logonErr)
}
