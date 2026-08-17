package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/mongod"
)

// `ratline db access` controls which addresses can reach the MongoDB port. Two facts
// have to agree for that to mean anything — what mongod binds and what the firewall
// admits — and these commands own both together, because an operator changing one by
// hand gets either a server that is unreachable for no visible reason or one that is
// reachable by everyone, and both look identical from the machine itself.

func newDBAccessCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "access",
		Short: "Control which addresses can reach this host's MongoDB",
		Long: "Manages remote access to the MongoDB server `ratline db install` set up. By\n" +
			"default that server listens only on localhost. Allowing an address adds a ufw rule\n" +
			"admitting it to port 27017 and — on the first address — reconfigures mongod to\n" +
			"listen on all interfaces, in that order: the firewall stands guard before the\n" +
			"door opens. Revoking the last address puts mongod back on localhost only.\n\n" +
			"ufw must already be active with a default-deny incoming policy. ratline never\n" +
			"enables the firewall itself: done in the wrong order that locks you out of SSH,\n" +
			"and only you know what else this machine must keep serving.\n\n" +
			"For a MongoDB ratline did not install — Atlas, another host — the access list\n" +
			"lives with that server, not with this machine's firewall, and these commands\n" +
			"refuse.",
		Example: "  ratline db access allow 203.0.113.19          # one machine\n" +
			"  ratline db access allow 10.8.0.0/24 --note vpn # a network\n" +
			"  ratline db access list\n" +
			"  ratline db access revoke 203.0.113.19",
	}
	cmd.AddCommand(
		newDBAccessAllowCommand(g),
		newDBAccessRevokeCommand(g),
		newDBAccessListCommand(g),
	)
	return cmd
}

func newDBAccessAllowCommand(g *Globals) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "allow <address>",
		Short: "Let an address or network reach MongoDB",
		Args:  cobra.ExactArgs(1),
		Long: "Adds a ufw rule admitting the address to port 27017. The first allowed address\n" +
			"also reconfigures mongod to listen beyond localhost and restarts it — firewall\n" +
			"rule first, wider bind second, and the outcome is verified against the running\n" +
			"server: it must still enforce authorization, and it must actually be listening\n" +
			"where the config says.\n\n" +
			"The address can be one machine (203.0.113.19) or a network in CIDR notation\n" +
			"(10.8.0.0/24). Prefer the narrowest thing that works: this port now stands\n" +
			"behind a password alone for everyone the rule admits.",
		Example: "  ratline db access allow 203.0.113.19\n" +
			"  ratline db access allow 10.8.0.0/24 --note \"office vpn\"",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if engine, eerr := g.dbEngineChoice(cmd); eerr != nil {
				return eerr
			} else if engine == engineMySQL {
				return g.mysqlAccessAllow(cmd, args[0], note)
			}
			mgr, err := g.mongodManager(ctx)
			if err != nil {
				return err
			}
			if g.DryRun {
				canonical, err := mongod.CanonicalAddress(args[0])
				if err != nil {
					return err
				}
				g.Log.Info("would allow the address through to MongoDB",
					"address", canonical, "port", mongod.Port)
				return nil
			}
			res, err := mgr.AccessAllow(ctx, args[0], note, g.Invoked())
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(res)
			}
			if res.AlreadyThere {
				g.Printf("%s is already allowed. Nothing changed.\n", res.Address)
				return nil
			}
			g.Printf("%s can now reach MongoDB on port %s.\n", res.Address, mongod.Port)
			if res.OpenedNetwork {
				g.Printf("\nThis was the first allowed address, so mongod now listens on all\n" +
					"interfaces; the firewall admits only the allowed list. Verified: the\n" +
					"server still enforces authorization.\n")
			}
			g.Printf("\nSee the whole list:\n    ratline db access list\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "A word on whose address this is, shown in the list")
	return Mutating(cmd)
}

func newDBAccessRevokeCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <address>",
		Short: "Stop an address reaching MongoDB",
		Args:  cobra.ExactArgs(1),
		Long: "Deletes the ufw rule an allow created. Revoking the last allowed address also\n" +
			"puts mongod back to listening on localhost only, restarts it, and verifies both\n" +
			"facts against the running server.\n\n" +
			"Connections that address already holds open are not cut — the firewall stops new\n" +
			"ones. Restarting mongod cuts everything; revoking the last address does that\n" +
			"anyway, as a side effect of the rebind.\n\n" +
			"Revoking an address that was never allowed reports as much and changes nothing:\n" +
			"it is already the state you asked for.",
		Example: "  ratline db access revoke 203.0.113.19",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if engine, eerr := g.dbEngineChoice(cmd); eerr != nil {
				return eerr
			} else if engine == engineMySQL {
				return g.mysqlAccessRevoke(cmd, args[0])
			}
			mgr, err := g.mongodManager(ctx)
			if err != nil {
				return err
			}
			if g.DryRun {
				canonical, err := mongod.CanonicalAddress(args[0])
				if err != nil {
					return err
				}
				g.Log.Info("would revoke the address's access to MongoDB", "address", canonical)
				return nil
			}
			res, err := mgr.AccessRevoke(ctx, args[0])
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(res)
			}
			if res.WasAbsent {
				g.Printf("%s was not on the allowed list. Nothing changed.\n", res.Address)
				return nil
			}
			g.Printf("%s can no longer open connections to MongoDB.\n", res.Address)
			if res.ClosedNetwork {
				g.Printf("\nThat was the last allowed address: mongod is back to localhost only,\n" +
					"verified against the running server.\n")
			} else {
				g.Printf("\nConnections it already holds stay open until they close themselves.\n")
			}
			return nil
		},
	}
	return Mutating(cmd)
}

func newDBAccessListCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show who can reach this host's MongoDB",
		Args:  cobra.NoArgs,
		Long: "Shows the allowed addresses, what mongod is bound to, and whether the firewall\n" +
			"is still standing guard — together, because each is meaningless without the\n" +
			"others.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if engine, eerr := g.dbEngineChoice(cmd); eerr != nil {
				return eerr
			} else if engine == engineMySQL {
				return g.mysqlAccessList(cmd)
			}
			mgr, err := g.mongodManager(ctx)
			if err != nil {
				return err
			}
			status, err := mgr.AccessList(ctx)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(status)
			}

			bind := "localhost only"
			if status.BindRemote {
				bind = "all interfaces"
			}
			firewall := "ufw not installed"
			switch {
			case status.UfwActive && status.DefaultDeny:
				firewall = "ufw active, default deny incoming"
			case status.UfwActive:
				firewall = "ufw active, but default incoming policy is not deny"
			case status.UfwPresent:
				firewall = "ufw installed but not active"
			}
			if err := g.Fields(
				[2]string{"mongod listening on", bind},
				[2]string{"firewall", firewall},
				[2]string{"allowed addresses", fmt.Sprintf("%d", len(status.Addresses))},
			); err != nil {
				return err
			}
			if len(status.Addresses) > 0 {
				g.Printf("\n")
				for _, a := range status.Addresses {
					line := "    " + a.Address
					if a.Note != "" {
						line += "    # " + a.Note
					}
					g.Println(line)
				}
			}
			if status.BindRemote && !(status.UfwActive && status.DefaultDeny) {
				g.Printf("\nmongod listens beyond localhost and the firewall is not standing guard.\n"+
					"Anyone who can reach port %s faces only a password. Fix ufw, or revoke\n"+
					"every address to go back to localhost only.\n", mongod.Port)
			}
			return nil
		},
	}
	return cmd
}
