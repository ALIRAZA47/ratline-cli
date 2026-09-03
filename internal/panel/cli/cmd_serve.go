package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/httpapi"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/jobs"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/rl"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/web"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func newServeCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the panel (this is what the systemd unit starts)",
		Args:  cobra.NoArgs,
		Long: "Opens the panel's database, checks the ratline binary, starts the job\n" +
			"workers and listens.\n\n" +
			"It listens on 127.0.0.1 unless the configuration says otherwise, because a\n" +
			"panel is a root-equivalent surface and the thing facing the internet should\n" +
			"be nginx with a certificate rather than this process.",
		RunE: func(cmd *cobra.Command, _ []string) error { return app.serve(cmd.Context()) },
	}
}

func (app *App) serve(ctx context.Context) error {
	httpapi.Version = buildinfo.Version

	st, err := store.Open(app.Cfg.Paths.StateDB)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			app.Log.Debug("could not close the panel database", "err", cerr)
		}
	}()

	client, err := rl.NewClient(app.Cfg.Ratline.Binary, app.Runner, app.Log)
	if err != nil {
		return err
	}
	client.ConfigPath = app.Cfg.Ratline.Config
	client.ReadTimeout = app.Cfg.Ratline.ReadTimeout.D()
	client.WriteTimeout = app.Cfg.Ratline.WriteTimeout.D()
	client.JobTimeout = app.Cfg.Ratline.JobTimeout.D()

	// Read once at startup so that a misconfigured binary is a refusal to start
	// rather than an error on every page. A panel that comes up and then answers
	// nothing is harder to diagnose than one that says why it did not.
	cat, err := client.Catalogue(ctx)
	if err != nil {
		return err
	}
	app.Log.Info("driving ratline", "version", cat.Version, "binary", app.Cfg.Ratline.Binary,
		"commands", len(cat.Leaves))
	if missing := rl.UnclassifiedMutations(cat); len(missing) > 0 {
		// Not a failure: the default already puts them behind a super admin. But
		// somebody should know, because it means this panel is older than the
		// ratline it is driving.
		app.Log.Warn("this ratline has commands the panel has no policy for; they are super-admin only",
			"count", len(missing), "first", missing[0])
	}

	jm := jobs.New(st, client, app.Log, app.Cfg.Jobs.OutputLimit, app.Cfg.Jobs.Retain)
	if err := jm.Start(ctx, app.Cfg.Jobs.Concurrency); err != nil {
		return err
	}
	defer jm.Stop()

	srv, err := httpapi.New(app.Cfg, st, client, jm, app.Log)
	if err != nil {
		return err
	}
	ui, err := web.Handler()
	if err != nil {
		return err
	}
	srv.UI = ui

	go app.housekeep(ctx, st)

	if n, err := st.CountAccounts(ctx); err == nil && n == 0 {
		app.Log.Warn("nobody has set this panel up yet",
			"url", app.Cfg.PublicURL(),
			"note", "the first person to reach it becomes the super admin; do it now")
	}
	return srv.Serve(ctx)
}

// housekeep removes what has expired.
//
// Not on every request, which would put a delete in the path of a page load, and not
// only at startup, which would let a long-running panel accumulate months of expired
// sessions and sign-in attempts.
func (app *App) housekeep(ctx context.Context, st *store.Store) {
	tick := time.NewTicker(30 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			now := time.Now().UTC()
			if n, err := st.PurgeExpiredSessions(ctx, now); err != nil {
				app.Log.Debug("could not purge expired sessions", "err", err)
			} else if n > 0 {
				app.Log.Debug("purged expired sessions", "count", n)
			}
			if _, err := st.PurgeLoginAttempts(ctx, now.Add(-24*time.Hour)); err != nil {
				app.Log.Debug("could not purge sign-in attempts", "err", err)
			}
			// A week past expiry: long enough that "did that invitation ever
			// arrive?" is still answerable, short enough that the listing is
			// about who is waiting.
			if _, err := st.PurgeInvites(ctx, now.Add(-7*24*time.Hour)); err != nil {
				app.Log.Debug("could not purge invitations", "err", err)
			}
		}
	}
}

// openStore is the shared opener for the commands that touch the database directly.
func (app *App) openStore() (*store.Store, error) {
	st, err := store.Open(app.Cfg.Paths.StateDB)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodePrecondition, "opening the panel database").
			WithHint("is the panel installed? try 'ratline-panel install'")
	}
	return st, nil
}

func (app *App) printf(format string, a ...any) {
	if app.Quiet || app.JSON {
		return
	}
	fmt.Fprintf(app.Stdout, format, a...)
}
