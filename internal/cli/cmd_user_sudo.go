package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/user"
)

// newUserSudoCommand is the escape hatch, kept deliberately awkward.
//
// A tenant with sudo can reach every other tenant's files, so this is
// config-gated, never a blanket grant, and validated with visudo before the file
// is installed — a malformed sudoers locks every sudo user out of the machine.
func newUserSudoCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sudo",
		Short: "Grant or revoke a narrow sudo permission (off unless users.allow_sudo)",
		Long: "Created users get no sudo. This exists because a real deployment occasionally\n" +
			"needs one specific command — a client's CI restarting their own service.\n\n" +
			"Every rule pins the full argument list. A grant of just the program name would\n" +
			"let the tenant pass any arguments to it, and most programs with arbitrary\n" +
			"arguments are equivalent to root.",
	}

	var commands []string
	grant := &cobra.Command{
		Use:   "grant <username>",
		Short: "Install a sudo rule for exactly the commands named",
		Args:  cobra.ExactArgs(1),
		Example: "  ratline user sudo grant acme \\\n" +
			"      --command '/usr/bin/systemctl restart ratline-acme-example_com.service'",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := RequireFlags(cmd, g, "command"); err != nil {
				return err
			}
			mgr, err := g.userManager(cmd.Context())
			if err != nil {
				return err
			}
			if err := g.ConfirmTyped(args[0],
				"A tenant with sudo can reach every other tenant's files. Grant it anyway?"); err != nil {
				return err
			}
			if err := mgr.GrantSudo(cmd.Context(), user.SudoGrant{User: args[0], Commands: commands}); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"user": args[0], "commands": commands})
			}
			g.Printf("Granted %s sudo for %d command(s).\n", args[0], len(commands))
			return nil
		},
	}
	grant.Flags().StringArrayVar(&commands, "command", nil,
		"An absolute command with its full arguments (repeatable, required)")
	Required(grant, "command")
	cmd.AddCommand(Mutating(grant))

	revoke := &cobra.Command{
		Use:   "revoke <username>",
		Short: "Remove a tenant's sudo grant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.userManager(cmd.Context())
			if err != nil {
				return err
			}
			if err := mgr.RevokeSudo(cmd.Context(), args[0]); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"user": args[0], "revoked": true})
			}
			g.Printf("Revoked %s's sudo grant.\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(Mutating(revoke))

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List the tenants with a ratline-installed sudo grant",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := g.userManager(cmd.Context())
			if err != nil {
				return err
			}
			grants, err := mgr.SudoGrants(cmd.Context())
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"grants": grants})
			}
			if len(grants) == 0 {
				g.Println("No tenant has a ratline sudo grant.")
				return nil
			}
			tbl := g.Table("user", "permitted")
			for u, rules := range grants {
				tbl.Row(u, strings.Join(rules, "; "))
			}
			return tbl.Render()
		},
	})
	return cmd
}
