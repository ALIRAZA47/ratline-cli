package cli

import (
	"time"

	"github.com/spf13/cobra"
)

func newKeyPruneCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove expired keys and record key usage",
		Args:  cobra.NoArgs,
		Long: "Run daily by ratline-key-prune.timer.\n\n" +
			"Two jobs, both of which have to happen on a schedule. Expired keys are removed:\n" +
			"OpenSSH 8.2+ already refuses them through expiry-time=, but this is what takes\n" +
			"the line out of the file, and on an older daemon it is the only mechanism.\n" +
			"And key usage is scraped from the journal, because logs rotate — a key last used\n" +
			"four months ago leaves no trace by the time anyone asks.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := g.keyManager(cmd.Context())
			if err != nil {
				return err
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			since, _ := st.LastKeyUsageScan(cmd.Context())
			observed, err := mgr.ScanUsage(cmd.Context(), since)
			if err != nil {
				g.Log.Warn("the usage scan failed", "err", err)
			}
			pruned, err := mgr.Prune(cmd.Context(), time.Now())
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"pruned": pruned, "usage_observations": observed})
			}
			g.Printf("Removed %d expired key(s); recorded %d usage observation(s)\n", len(pruned), observed)
			return nil
		},
	}
	return Mutating(cmd)
}
