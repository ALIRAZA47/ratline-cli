package cli

import (
	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// newDBCommand is the database-provisioning stub.
//
// Database provisioning is out of scope for v1, but the command exists so that
// the shape of it is settled and so an operator who types it gets an answer
// rather than "unknown command" — which reads as "I typed it wrong" rather than
// "this does not exist yet".
//
// It is hidden unless features.db_provisioning is on, so it does not clutter the
// help output of a server that cannot use it.
func newDBCommand(g *Globals) *cobra.Command {
	enabled := g.Cfg != nil && g.Cfg.Features.DBProvisioning

	cmd := &cobra.Command{
		Use:     "db",
		Short:   "Provision databases (not implemented in v1)",
		GroupID: GroupOps,
		Hidden:  !enabled,
		Long: "Database provisioning is not implemented. This command exists so that the\n" +
			"intended shape is settled and so typing it gives you an answer.\n\n" +
			"When it lands it will create a database and a least-privilege role per site,\n" +
			"store the credentials in the site's .env, and record the grant in state so that\n" +
			"'site delete --purge' can revoke it. Until then, provision by hand and put the\n" +
			"connection string in the environment:\n\n" +
			"    ratline site env set example.com DATABASE_URL=postgres://…\n\n" +
			"That is deliberately the same interface the built-in version will use, so\n" +
			"nothing about your application has to change later.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return notImplemented(g, "db")
		},
	}
	// The verbs are declared rather than left out, so `ratline db --help` shows
	// what is planned instead of an empty command.
	for _, spec := range []struct{ use, short string }{
		{"create <domain>", "Create a database and a least-privilege role for a site"},
		{"list", "List databases and the sites that own them"},
		{"drop <domain>", "Drop a site's database and revoke its role"},
		{"password rotate <domain>", "Rotate a site's database password and update its .env"},
	} {
		sub := &cobra.Command{
			Use:    spec.use,
			Short:  spec.short + " (not implemented)",
			Hidden: !enabled,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return notImplemented(g, "db "+cmd.Name())
			},
		}
		cmd.AddCommand(sub)
	}
	return cmd
}

// notImplemented is the honest refusal: it names the workaround rather than
// pretending the feature is coming imminently.
func notImplemented(g *Globals, what string) error {
	e := rlerr.Preconditionf("ratline %s is not implemented", what).
		WithHint("provision the database by hand, then give the site its connection string:\n" +
			"        ratline site env set <domain> DATABASE_URL=…")
	if g.Cfg != nil && !g.Cfg.Features.DBProvisioning {
		e = e.WithField("feature_flag", "features.db_provisioning is off")
	}
	return e
}
