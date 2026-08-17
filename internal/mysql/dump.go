package mysql

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Dump and restore, mirroring internal/mongo/dump.go. The output is streamed through
// gzip to a 0600 file rather than captured in memory — a dump can be gigabytes, and the
// Runner caps its output buffers — and the admin credentials travel in the defaults-file,
// never in argv.

// DumpResult reports where a dump landed.
type DumpResult struct {
	Database string `json:"database"`
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
}

// Dump writes one database to a gzip file under outDir and returns where it landed.
func (m *Manager) Dump(ctx context.Context, database, outDir string) (*DumpResult, error) {
	if err := validate.MySQLDatabaseName(database); err != nil {
		return nil, err
	}
	if m.Bins != nil && !m.Bins.Available("mysqldump") {
		return nil, rlerr.Preconditionf("mysqldump is not installed").
			WithHint("apt-get install mysql-client (or mariadb-client)")
	}
	defaults, err := m.AdminDefaultsFile()
	if err != nil {
		return nil, err
	}
	if outDir == "" {
		outDir = filepath.Join(m.Cfg.Paths.BackupDir, "databases")
	}
	if _, err := system.EnsureDir(outDir, 0o700, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}
	path := filepath.Join(outDir, database+".sql.gz")

	if m.DryRun {
		m.Log.Info("would dump the database", "database", database, "path", path)
		return &DumpResult{Database: database, Path: path}, nil
	}

	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "creating %s", path)
	}
	gz := gzip.NewWriter(out)
	// --single-transaction takes a consistent snapshot without locking; --routines and
	// --triggers carry the stored programs a plain table dump would drop.
	_, runErr := m.Runner.Run(ctx, system.Cmd{
		Name: "mysqldump",
		Args: []string{
			"--defaults-extra-file=" + defaults,
			"--single-transaction", "--routines", "--triggers", database,
		},
		Stdout:  gz,
		Env:     system.MinimalEnv(),
		Timeout: 30 * time.Minute,
		Mutates: false, // reading the server; the write is the local file
		Label:   "mysqldump " + database,
	})
	gzErr := gz.Close()
	closeErr := out.Close()
	if runErr != nil {
		_ = os.Remove(path)
		return nil, rlerr.Wrap(runErr, rlerr.CodeExternal, "mysqldump failed")
	}
	if gzErr != nil {
		_ = os.Remove(path)
		return nil, rlerr.Wrap(gzErr, rlerr.CodeGeneric, "compressing the dump")
	}
	if closeErr != nil {
		return nil, rlerr.Wrap(closeErr, rlerr.CodeGeneric, "writing %s", path)
	}
	fi, _ := os.Stat(path)
	res := &DumpResult{Database: database, Path: path}
	if fi != nil {
		res.Bytes = fi.Size()
	}
	return res, nil
}

// Restore loads a gzip dump into a database, creating the target if needed.
func (m *Manager) Restore(ctx context.Context, archive, into string) error {
	if err := validate.MySQLDatabaseName(into); err != nil {
		return err
	}
	defaults, err := m.AdminDefaultsFile()
	if err != nil {
		return err
	}
	if m.DryRun {
		m.Log.Info("would restore the dump", "archive", archive, "into", into)
		return nil
	}
	if err := m.CreateDatabase(ctx, into); err != nil {
		return err
	}

	f, err := os.Open(archive)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "opening %s", archive)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "%s is not a gzip archive", archive)
	}
	defer gz.Close()

	// The target database is a validated identifier passed as an argument, so mysql runs
	// the dumped statements in its context; the dump itself carries no CREATE DATABASE.
	_, err = m.Runner.Run(ctx, system.Cmd{
		Name:    "mysql",
		Args:    []string{"--defaults-extra-file=" + defaults, into},
		Stdin:   gz,
		Env:     system.MinimalEnv(),
		Timeout: 30 * time.Minute,
		Mutates: true,
		Label:   "mysql restore " + into,
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "restoring into %s failed", into)
	}
	return nil
}
