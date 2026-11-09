package secret

import (
	"errors"
	"syscall"
	"testing"
)

func TestPasswordVerdict(t *testing.T) {
	const (
		errInvalidLogonHours = syscall.Errno(1328) // ERROR_INVALID_LOGON_HOURS
		errAccountDisabled   = syscall.Errno(1331) // ERROR_ACCOUNT_DISABLED
		errAccountExpired    = syscall.Errno(1793) // ERROR_ACCOUNT_EXPIRED
		errMustChange        = syscall.Errno(1907) // ERROR_PASSWORD_MUST_CHANGE
		errLockedOut         = syscall.Errno(1909) // ERROR_ACCOUNT_LOCKED_OUT
	)
	tests := []struct {
		name          string
		logonErr      error
		emptyPassword bool
		signedInBlank bool
		wantOK        bool
		wantErr       bool // an error that wraps logonErr
		wantAsked     bool // signedInBlankAccount is consulted
	}{
		{name: "signed in", logonErr: nil, wantOK: true},
		{name: "signed in, empty", logonErr: nil, emptyPassword: true, wantOK: true},
		{name: "wrong password", logonErr: errLogonFailure},
		{name: "wrong, empty", logonErr: errLogonFailure, emptyPassword: true},
		{name: "blank password, own account", logonErr: errAccountRestriction, emptyPassword: true, signedInBlank: true, wantOK: true, wantAsked: true},
		{name: "blank password, another account", logonErr: errAccountRestriction, emptyPassword: true, wantErr: true, wantAsked: true},
		{name: "restriction, password typed", logonErr: errAccountRestriction, signedInBlank: true, wantErr: true},
		{name: "disabled, empty", logonErr: errAccountDisabled, emptyPassword: true, signedInBlank: true, wantErr: true},
		{name: "logon hours, empty", logonErr: errInvalidLogonHours, emptyPassword: true, signedInBlank: true, wantErr: true},
		{name: "must change, empty", logonErr: errMustChange, emptyPassword: true, signedInBlank: true, wantErr: true},
		{name: "account expired, empty", logonErr: errAccountExpired, emptyPassword: true, signedInBlank: true, wantErr: true},
		{name: "locked out, empty", logonErr: errLockedOut, emptyPassword: true, signedInBlank: true, wantErr: true},
		{name: "other error", logonErr: errors.New("boom"), emptyPassword: true, signedInBlank: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asked := false
			ok, err := passwordVerdict(tt.logonErr, tt.emptyPassword, func() bool {
				asked = true
				return tt.signedInBlank
			})
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			switch {
			case !tt.wantErr && err != nil:
				t.Errorf("err = %v, want none", err)
			case tt.wantErr && !errors.Is(err, tt.logonErr):
				t.Errorf("err = %v, want one that wraps %v", err, tt.logonErr)
			}
			if asked != tt.wantAsked {
				t.Errorf("signedInBlankAccount consulted = %v, want %v", asked, tt.wantAsked)
			}
		})
	}
}
