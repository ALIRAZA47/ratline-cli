// Package rlerr defines ratline's error type and the exit-code contract.
//
// Exit codes are part of the CLI's public interface — automation branches on
// them — so they are declared once here and never inferred from error text.
package rlerr

import (
	"errors"
	"fmt"
	"strings"
)

// Code is a ratline process exit code.
type Code int

// The exit-code contract. Documented in COMMANDS.md; do not renumber.
const (
	CodeOK             Code = 0  // success
	CodeGeneric        Code = 1  // unclassified failure
	CodeUsage          Code = 2  // bad flags, bad arguments, failed validation
	CodePrecondition   Code = 3  // the system is not in a state where this can run
	CodeExternal       Code = 4  // an external command failed
	CodeLocked         Code = 5  // another ratline invocation holds the lock
	CodeRollbackFailed Code = 6  // the operation failed and so did its rollback
	CodeUnhealthy      Code = 7  // started, but never became healthy
	CodeACME           Code = 8  // ACME challenge failed
	CodeRateLimited    Code = 9  // would exceed a CA rate limit
	CodeInputRequired  Code = 10 // a prompt was needed but input is unavailable
)

var codeNames = map[Code]string{
	CodeOK:             "ok",
	CodeGeneric:        "error",
	CodeUsage:          "usage",
	CodePrecondition:   "precondition_failed",
	CodeExternal:       "external_command_failed",
	CodeLocked:         "locked",
	CodeRollbackFailed: "rollback_failed",
	CodeUnhealthy:      "health_check_failed",
	CodeACME:           "acme_challenge_failed",
	CodeRateLimited:    "rate_limited",
	CodeInputRequired:  "input_required",
}

// Name returns the stable machine-readable name used in --json output.
func (c Code) Name() string {
	if n, ok := codeNames[c]; ok {
		return n
	}
	return fmt.Sprintf("code_%d", int(c))
}

func (c Code) String() string { return c.Name() }

// Error carries a message, an exit code, an optional actionable hint and
// optional structured fields for --json output.
//
// Every error surfaced to an operator should answer three questions: what
// failed, why, and what to do next. Msg covers the first two, Hint the third.
type Error struct {
	Code   Code
	Op     string // logical operation, e.g. "site add"
	Msg    string
	Hint   string
	Fields map[string]string
	Err    error
}

func (e *Error) Error() string {
	var b strings.Builder
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	b.WriteString(e.Msg)
	if e.Err != nil {
		if e.Msg != "" {
			b.WriteString(": ")
		}
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// WithHint attaches the "what to do next" line.
func (e *Error) WithHint(format string, a ...any) *Error {
	e.Hint = fmt.Sprintf(format, a...)
	return e
}

// WithOp labels the error with the operation that produced it.
func (e *Error) WithOp(op string) *Error {
	e.Op = op
	return e
}

// WithField attaches a structured detail that survives into --json output.
func (e *Error) WithField(key, value string) *Error {
	if e.Fields == nil {
		e.Fields = make(map[string]string, 4)
	}
	e.Fields[key] = value
	return e
}

// New builds an error with an explicit code.
func New(code Code, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// Wrap annotates err with a code and message. err must not be nil; being
// asked to wrap nil is a programming bug and produces a loud error rather
// than a silently non-nil interface holding a nil pointer.
func Wrap(err error, code Code, format string, a ...any) *Error {
	if err == nil {
		err = errors.New("no underlying error (rlerr.Wrap called with nil)")
	}
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...), Err: err}
}

// Convenience constructors, one per code that callers raise directly.

func Genericf(format string, a ...any) *Error { return New(CodeGeneric, format, a...) }

func Usagef(format string, a ...any) *Error { return New(CodeUsage, format, a...) }

func Preconditionf(format string, a ...any) *Error { return New(CodePrecondition, format, a...) }

func Externalf(format string, a ...any) *Error { return New(CodeExternal, format, a...) }

func Lockedf(format string, a ...any) *Error { return New(CodeLocked, format, a...) }

func RollbackFailedf(format string, a ...any) *Error { return New(CodeRollbackFailed, format, a...) }

func Unhealthyf(format string, a ...any) *Error { return New(CodeUnhealthy, format, a...) }

func ACMEf(format string, a ...any) *Error { return New(CodeACME, format, a...) }

func RateLimitedf(format string, a ...any) *Error { return New(CodeRateLimited, format, a...) }

func InputRequiredf(format string, a ...any) *Error { return New(CodeInputRequired, format, a...) }

// ExitCode maps any error onto the process exit code.
func ExitCode(err error) int {
	return int(CodeOf(err))
}

// CodeOf reports the code of the outermost *Error in the chain.
func CodeOf(err error) Code {
	if err == nil {
		return CodeOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeGeneric
}

// Is reports whether err carries the given code.
func Is(err error, code Code) bool { return err != nil && CodeOf(err) == code }

// Hint returns the actionable hint, if any.
func Hint(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}

// Op returns the labelled operation, if any.
func Op(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Op
	}
	return ""
}

// Fields returns the structured details, if any.
func Fields(err error) map[string]string {
	var e *Error
	if errors.As(err, &e) {
		return e.Fields
	}
	return nil
}
