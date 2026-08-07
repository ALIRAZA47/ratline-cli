package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/mongo"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline db dump` and `ratline db restore`.
//
// `ratline backup` archives a site's files and says plainly it holds nothing else. That
// left a site with a database backed up by two mechanisms, one of which did not exist — so
// an operator who ran `backup` before a risky change had the code and not the data, which
// is the half that cannot be rebuilt from git.

func newDBDumpCommand(g *Globals) *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:   "dump <database>",
		Short: "Write a compressed archive of one database",
		Args:  cobra.ExactArgs(1),
		Long: "One gzipped archive file, scoped to the named database, written 0600.\n\n" +
			"It holds every document in the database, so where it goes afterwards is your\n" +
			"responsibility — the same warning `ratline backup` carries about the .env.\n\n" +
			"The connection string never appears in the argument list. /proc is world-readable,\n" +
			"and an admin URI on a command line is the password for every database on the\n" +
			"server, visible to every account on it for as long as the dump runs.",
		Example: "  ratline db dump app_example_com\n" +
			"  ratline db dump app_example_com --out /mnt/backups",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]
			if err := validate.DatabaseName(name); err != nil {
				return err
			}
			mgr, st, err := g.dbManager(ctx)
			if err != nil {
				return err
			}
			// Refuse a database ratline does not know about, rather than dumping whatever
			// happens to answer to that name. `--live` on `db list` is how you look at the
			// rest of the server.
			if _, err := st.GetDatabase(ctx, name); err != nil {
				return err
			}
			if outDir == "" {
				outDir = filepath.Join(g.Cfg.Paths.BackupDir, "databases")
			}
			res, err := mgr.Dump(ctx, name, outDir)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{
					"database": res.Database, "path": res.Path, "bytes": res.Bytes,
				})
			}
			if g.DryRun {
				g.Printf("would write %s\n", res.Path)
				return nil
			}
			g.Printf("Wrote %s (%s)\n", res.Path, validate.FormatSize(res.Bytes))
			g.Printf("\nIt holds every document in %s and is readable only by root.\n", res.Database)
			g.Printf("Put it back with:\n    ratline db restore %s\n", res.Path)
			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "", "Directory to write into (default <backup_dir>/databases)")
	return Mutating(cmd)
}

func newDBRestoreCommand(g *Globals) *cobra.Command {
	var into string
	var drop bool
	cmd := &cobra.Command{
		Use:   "restore <archive>",
		Short: "Load an archive back into a database",
		Args:  cobra.ExactArgs(1),
		Long: "Restores what `ratline db dump` wrote.\n\n" +
			"By default it goes back into the database it came from, which the filename\n" +
			"records. --into loads it somewhere else, which is how a production dump gets\n" +
			"loaded into staging without editing the archive.\n\n" +
			"Documents already there are left alone unless --drop says otherwise, so a\n" +
			"restore over a live database merges rather than replaces. That is the safer\n" +
			"default and rarely the one you want: --drop is what makes it a restore.",
		Example: "  ratline db restore /var/backups/ratline/databases/app_example_com-20260807T120000Z.archive.gz\n" +
			"  ratline db restore app.archive.gz --into app_staging\n" +
			"  ratline db restore app.archive.gz --drop     # replace what is there",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			archive := args[0]

			from := mongo.ArchiveDatabase(archive)
			target := into
			if target == "" {
				target = from
			}
			if target == "" {
				return rlerr.Usagef("cannot tell which database this archive belongs to").
					WithHint("name it with --into; ratline reads the database from the " +
						"filename it writes, and this one was not written by ratline")
			}
			if err := validate.DatabaseName(target); err != nil {
				return err
			}

			mgr, _, err := g.dbManager(ctx)
			if err != nil {
				return err
			}
			// Dropping is the destructive half, and it is the flag people reach for
			// without reading. Confirmed like every other destructive operation.
			if drop && !g.DryRun {
				if err := g.ConfirmTyped(target,
					"Every document in "+target+" will be replaced."); err != nil {
					return err
				}
			}
			if err := mgr.Restore(ctx, archive, from, target, drop); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{
					"archive": archive, "database": target, "dropped": drop,
				})
			}
			g.Printf("Restored %s into %s\n", filepath.Base(archive), target)
			if !drop {
				g.Printf("\nDocuments already in %s were kept. --drop replaces them instead.\n", target)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&into, "into", "", "Restore into this database instead of the one it came from")
	f.BoolVar(&drop, "drop", false, "Replace what is there rather than merging into it")
	return Mutating(cmd)
}
