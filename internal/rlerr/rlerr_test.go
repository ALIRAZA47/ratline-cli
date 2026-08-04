package rlerr

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCodeContract(t *testing.T) {
	// These numbers are a public interface. Automation branches on them, so a
	// change here is a breaking change and this test is the tripwire.
	want := map[Code]int{
		CodeOK: 0, CodeGeneric: 1, CodeUsage: 2, CodePrecondition: 3,
		CodeExternal: 4, CodeLocked: 5, CodeRollbackFailed: 6, CodeUnhealthy: 7,
		CodeACME: 8, CodeRateLimited: 9, CodeInputRequired: 10,
	}
	for code, n := range want {
		if int(code) != n {
			t.Errorf("%s = %d, want %d", code.Name(), int(code), n)
		}
	}
	names := map[string]bool{}
	for code := range want {
		n := code.Name()
		if n == "" || names[n] {
			t.Errorf("code %d has a missing or duplicate name %q", int(code), n)
		}
		names[n] = true
	}
}

func TestExitCodeOfWrappedErrors(t *testing.T) {
	base := Preconditionf("nginx is not installed")
	if got := ExitCode(base); got != 3 {
		t.Errorf("ExitCode = %d, want 3", got)
	}
	// A ratline error wrapped by fmt.Errorf keeps its code.
	wrapped := fmt.Errorf("while adding the site: %w", base)
	if got := ExitCode(wrapped); got != 3 {
		t.Errorf("ExitCode of a wrapped error = %d, want 3", got)
	}
	if got := ExitCode(errors.New("plain")); got != 1 {
		t.Errorf("ExitCode of a plain error = %d, want 1", got)
	}
	if got := ExitCode(nil); got != 0 {
		t.Errorf("ExitCode(nil) = %d, want 0", got)
	}
}

func TestOutermostCodeWins(t *testing.T) {
	inner := Externalf("certbot failed")
	outer := Wrap(inner, CodeACME, "the challenge could not be completed")
	if got := CodeOf(outer); got != CodeACME {
		t.Errorf("CodeOf = %v, want acme", got)
	}
	if !errors.Is(outer, inner) {
		t.Error("the wrapped error is not reachable with errors.Is")
	}
}

func TestMessageComposition(t *testing.T) {
	err := Wrap(errors.New("permission denied"), CodeGeneric, "writing %s", "/etc/nginx/x.conf").
		WithOp("site add").
		WithHint("check that you are root")
	want := "site add: writing /etc/nginx/x.conf: permission denied"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if Hint(err) != "check that you are root" {
		t.Errorf("Hint = %q", Hint(err))
	}
	if Op(err) != "site add" {
		t.Errorf("Op = %q", Op(err))
	}
}

func TestFieldsSurviveWrapping(t *testing.T) {
	err := Externalf("failed").WithField("unit", "ratline-alice-example_com.service").WithField("exit_code", "3")
	f := Fields(err)
	if f["unit"] != "ratline-alice-example_com.service" || f["exit_code"] != "3" {
		t.Errorf("Fields = %v", f)
	}
	if Fields(errors.New("plain")) != nil {
		t.Error("Fields on a plain error returned a map")
	}
}

func TestWrapWithNilDoesNotProduceAMisleadingSuccess(t *testing.T) {
	// Returning a typed nil here would create a non-nil error interface holding
	// a nil pointer — the classic Go trap. Wrap always returns something real.
	err := Wrap(nil, CodeGeneric, "boom")
	if err == nil {
		t.Fatal("Wrap(nil) returned nil, which would be an interface trap for callers")
	}
	if err.Error() == "" {
		t.Error("Wrap(nil) produced an empty message")
	}
}

func TestIs(t *testing.T) {
	if !Is(Lockedf("held"), CodeLocked) {
		t.Error("Is did not match the code")
	}
	if Is(Lockedf("held"), CodeUsage) {
		t.Error("Is matched the wrong code")
	}
	if Is(nil, CodeOK) {
		t.Error("Is(nil, …) returned true")
	}
}
