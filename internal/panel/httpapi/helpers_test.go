package httpapi

import (
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/auth"
)

// hashFor and codeFor keep the tests reading as tests rather than as a second copy
// of the auth package.
func hashFor(password string) (string, error) { return auth.HashPassword(password) }

func codeFor(secret string) (string, error) { return auth.TOTPCode(secret, time.Now().UTC()) }
