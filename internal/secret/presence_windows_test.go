//go:build windows

package secret

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// onRunner reports whether the tests run on one of GitHub's own runners,
// a throwaway machine: the tests that sign in with an empty password
// (which counts as a failed sign-in) or create a Windows account run
// there only.
func onRunner() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true" && os.Getenv("RUNNER_ENVIRONMENT") == "github-hosted"
}

func TestInDomain(t *testing.T) {
	sid := func(s string) *windows.SID {
		t.Helper()
		v, err := windows.StringToSid(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	domain := sid("S-1-5-21-1-2-3")
	for s, want := range map[string]bool{
		"S-1-5-21-1-2-3-1001":   true,
		"S-1-5-21-1-2-3-500":    true,
		"S-1-5-21-1-2-3":        false,
		"S-1-5-21-1-2-4-1001":   false,
		"S-1-5-21-1-2-3-1001-1": false,
		"S-1-11-96-1-2-3-1001":  false,
		"S-1-5-32-544":          false,
	} {
		if got := inDomain(sid(s), domain); got != want {
			t.Errorf("inDomain(%s, %s) = %v, want %v", s, domain, got, want)
		}
	}
}

// TestSignedInUser checks what signedInUser learns about the account the
// test runs as, without signing in.
func TestSignedInUser(t *testing.T) {
	me := signedInUser()
	t.Logf("%v", me.attrs())
	if !me.Known {
		t.Fatalf("the account is unknown: %v", me.problems)
	}
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if !windows.EqualSid(me.sid, tu.User.Sid) {
		t.Errorf("SID %s, want the process's %s", me.sid, tu.User.Sid)
	}
	name, domain, _, err := tu.User.Sid.LookupAccount("")
	if err != nil {
		t.Fatal(err)
	}
	if me.name != name || me.domain != domain {
		t.Errorf("account %s\\%s, want %s\\%s", me.domain, me.name, domain, name)
	}
	if len(me.computer) == 0 {
		t.Errorf("no computer names")
	}
	if isMe, why := me.named(name, domain); !isMe {
		t.Errorf("%s\\%s does not name the account (%s)", domain, name, why)
	}
	if isMe, _ := me.named("pinky-no-such-user", ""); isMe {
		t.Errorf("an account that does not exist names the signed-in one")
	}
	if !onRunner() {
		return
	}
	// The runner's account is a local account with a password.
	if !me.Local || me.localBy != "account domain" || me.Microsoft || len(me.problems) > 0 {
		t.Errorf("runner account: %v, want local by its account domain, not Microsoft, no problems", me.attrs())
	}
	if err := me.blankLogon(); !errors.Is(err, errLogonFailure) {
		t.Errorf("an empty password for the runner's account: %v, want %v", errText(err), errLogonFailure)
	}
}

var (
	procCredPackAuthenticationBufferW = modcredui.NewProc("CredPackAuthenticationBufferW")
	procImpersonateLoggedOnUser       = modadvapi32.NewProc("ImpersonateLoggedOnUser")
)

// pack packs a name and password as the credential dialog returns them.
func pack(t *testing.T, name, password string) []byte {
	t.Helper()
	n, _ := windows.UTF16PtrFromString(name)
	p, _ := windows.UTF16PtrFromString(password)
	size := uint32(0)
	procCredPackAuthenticationBufferW.Call(0, uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		t.Fatalf("CredPackAuthenticationBufferW: no size for %q", name)
	}
	buf := make([]byte, size)
	if r, _, err := procCredPackAuthenticationBufferW.Call(0, uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); r == 0 {
		t.Fatalf("CredPackAuthenticationBufferW %q: %v", name, err)
	}
	return buf[:size]
}

// check runs checkPassword on what the dialog would return for name and
// password.
func check(t *testing.T, me *windowsUser, name, password string) (bool, error) {
	t.Helper()
	buf := pack(t, name, password)
	return checkPassword(unsafe.Pointer(&buf[0]), uint32(len(buf)), me)
}

// as runs fn on a thread that impersonates token. The thread ends with
// it, so nothing else runs as that account.
func as(t *testing.T, token windows.Token, fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread() // never unlocked: the thread ends with the goroutine
		if r, _, err := procImpersonateLoggedOnUser.Call(uintptr(token)); r == 0 {
			t.Errorf("ImpersonateLoggedOnUser: %v", err)
			return
		}
		fn()
	}()
	<-done
}

func netUser(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("net", append([]string{"user"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("net user %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// waitLogon retries a sign-in until it answers want: the blank-password
// policy changes within seconds.
func waitLogon(t *testing.T, user string, want error) {
	t.Helper()
	u, _ := windows.UTF16PtrFromString(user)
	dot, _ := windows.UTF16PtrFromString(".")
	empty := uint16(0)
	var err error
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		if err = logonUser(u, dot, &empty); errors.Is(err, want) || (want == nil && err == nil) {
			return
		}
	}
	t.Fatalf("an empty password for %s: %v, want %v", user, errText(err), errText(want))
}

// TestBlankPasswordAccount runs the password check as Windows' dialog
// would for a local account without a password, as in issue #41: it
// creates such an account, signs it in while the blank-password policy is
// off, turns the policy back on and checks what the dialog may return for
// it while impersonating it. It changes the machine, so it runs on
// GitHub's runners only.
func TestBlankPasswordAccount(t *testing.T) {
	if !onRunner() {
		t.Skip("creates a Windows account and changes a security policy; runs on GitHub's runners only")
	}
	const user, fullName = "pinkyblank", "Pinky Blank Test"
	exec.Command("net", "user", user, "/delete").Run()
	// No password argument: an empty one ("") is checked against the
	// password policy, which refuses it on the runner.
	netUser(t, user, "/add", "/fullname:"+fullName)
	t.Cleanup(func() { exec.Command("net", "user", user, "/delete").Run() })

	lsa, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Lsa`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer lsa.Close()
	policy, _, err := lsa.GetIntegerValue("LimitBlankPasswordUse")
	if err != nil {
		policy = 1
	}
	t.Cleanup(func() { lsa.SetDWordValue("LimitBlankPasswordUse", uint32(policy)) })
	if err := lsa.SetDWordValue("LimitBlankPasswordUse", 0); err != nil {
		t.Fatal(err)
	}
	waitLogon(t, user, nil)
	u, _ := windows.UTF16PtrFromString(user)
	dot, _ := windows.UTF16PtrFromString(".")
	empty := uint16(0)
	var token windows.Token
	if r, _, err := procLogonUserW.Call(uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(dot)), uintptr(unsafe.Pointer(&empty)),
		logon32LogonNetwork, logon32ProviderDefault, uintptr(unsafe.Pointer(&token))); r == 0 {
		t.Fatalf("sign in %s with the policy off: %v", user, err)
	}
	defer token.Close()
	// Windows' default, which issue #41's reporter has.
	if err := lsa.SetDWordValue("LimitBlankPasswordUse", 1); err != nil {
		t.Fatal(err)
	}
	waitLogon(t, user, errAccountRestriction)

	runner := signedInUser()
	computer := runner.computer
	t.Logf("computer names %v", computer)

	as(t, token, func() {
		me := signedInUser()
		t.Logf("as %s: %v", user, me.attrs())
		if !me.Known || !me.Local || me.Microsoft || !strings.EqualFold(me.name, user) {
			t.Errorf("signedInUser as %s: %v", user, me.attrs())
			return
		}
		// Whatever form of the account's name the dialog returns, its
		// empty password is right. An unknown domain stands for a
		// computer name Pinky does not know, as a DNS name or one longer
		// than NetBIOS's 15 characters: on a workgroup computer Windows
		// takes it for the local account.
		forms := []string{user, `.\` + user, `PINKY-LONG-HOSTNAME-2026\` + user, `NOTTHISDOMAIN\` + user, fullName}
		for _, c := range computer {
			forms = append(forms, c+`\`+user, strings.ToLower(c)+`\`+user, user+"@"+c)
		}
		for _, name := range forms {
			if ok, err := check(t, me, name, ""); !ok || err != nil {
				t.Errorf("as %s, %q with an empty password: ok %v, err %v; want ok", user, name, ok, err)
			}
		}
		if ok, err := check(t, me, user, "x"); ok || err != nil {
			t.Errorf("as %s, a typed password: ok %v, err %v; want a wrong password", user, ok, err)
		}
		ms := signedInUser()
		if ok, err := check(t, ms, `MicrosoftAccount\pinky@example.com`, ""); ok || !errors.Is(err, ErrMicrosoftAccount) {
			t.Errorf("as %s, a Microsoft account's name with an empty password: ok %v, err %v; want %v", user, ok, err, ErrMicrosoftAccount)
		}
	})

	// The runner's account has a password: an empty one is wrong, for
	// whatever account the dialog names.
	for _, name := range []string{user, computer[0] + `\` + user, `PINKY-LONG-HOSTNAME-2026\` + user, runner.name, "pinky-no-such-user"} {
		if ok, err := check(t, runner, name, ""); ok || err != nil {
			t.Errorf("as the runner, %q with an empty password: ok %v, err %v; want a wrong password", name, ok, err)
		}
	}

	// Windows checks the account's other restrictions first.
	netUser(t, user, "/active:no")
	as(t, token, func() {
		me := signedInUser()
		var errno syscall.Errno
		if ok, err := check(t, me, user, ""); ok || !errors.As(err, &errno) || errno != 1331 {
			t.Errorf("as %s, disabled, with an empty password: ok %v, err %v; want ERROR_ACCOUNT_DISABLED", user, ok, err)
		}
	})
}
