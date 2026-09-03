package rl

import (
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// maxSecretLength bounds what may be written to a command's stdin.
//
// Generous for a password or a connection string, and far below anything that could
// be an attack on the child's input buffer. ratline caps its own read as well —
// `user password set` at 4 KiB, `site env set` at a megabyte — so this is the panel
// refusing early rather than the only limit.
const maxSecretLength = 8192

// StdinPayload composes exactly what will be written to the command's standard input.
//
// One function, because this is the moment a credential becomes bytes, and getting it
// wrong in two places is how one of them ends up in argv. Everything it produces has
// been validated: a name that is a real environment key, a value with no newline in
// it — because `site env set --stdin` reads *lines*, and a value containing one would
// silently become a second assignment setting a variable nobody asked for.
func StdinPayload(policy Policy, req Request) (string, error) {
	if req.Secret == "" {
		if req.SecretKey != "" {
			return "", rlerr.Usagef("a name was given with no value")
		}
		return "", nil
	}
	if policy.Stdin == nil {
		return "", rlerr.Usagef("this command does not read a value from standard input").
			WithHint("this is a panel bug: the action was not declared as carrying one")
	}
	if len(req.Secret) > maxSecretLength {
		return "", rlerr.Usagef("the value is %d bytes; the limit is %d",
			len(req.Secret), maxSecretLength)
	}

	if policy.Stdin.KeyLabel == "" {
		// Verbatim. A password may legitimately contain anything except the
		// newline ratline would treat as the end of it.
		if strings.ContainsAny(req.Secret, "\n\r") {
			return "", rlerr.Usagef("a %s may not contain a line break", policy.Stdin.Label).
				WithHint("ratline reads it as one line")
		}
		return req.Secret, nil
	}

	name := strings.TrimSpace(req.SecretKey)
	if name == "" {
		return "", rlerr.Usagef("a %s is required", policy.Stdin.KeyLabel)
	}
	// The real validator, the one ratline uses. A name containing '=' would make
	// the split ambiguous; a name containing a newline would make it two lines.
	if err := validate.EnvKey(name); err != nil {
		return "", err
	}
	if err := validate.EnvValue(req.Secret); err != nil {
		return "", err
	}
	if strings.ContainsAny(req.Secret, "\n\r") {
		return "", rlerr.Usagef("a value may not contain a line break").
			WithHint("ratline reads standard input as one NAME=value assignment per line, " +
				"so a line break would set a second variable")
	}
	return name + "=" + req.Secret + "\n", nil
}
