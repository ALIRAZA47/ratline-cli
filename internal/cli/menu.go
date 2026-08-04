package cli

import (
	"context"

	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// runMainMenu is the entry point for a bare `ratline` on a terminal.
//
// The root command is reachable without root so that usage errors report as
// usage errors, which means the privilege check belongs here, at the point the
// menu actually needs it.
func runMainMenu(ctx context.Context, g *Globals) error {
	if err := system.RequireRoot(); err != nil {
		return err
	}
	if err := system.CheckSelfBinary(); err != nil {
		return err
	}
	return mainMenu(ctx, g)
}
