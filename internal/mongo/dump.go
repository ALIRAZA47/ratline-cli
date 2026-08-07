package mongo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// Dumping and restoring a database.
//
// `backup` archives a site's files and says plainly that it holds nothing else. Until now
// that left no ratline-native way to take a restore point of the data ratline itself
// provisioned — so a site with a database had two halves backed up by two different
// mechanisms, one of which did not exist.
//
// The connection string never reaches argv. mongodump takes `--config`, a YAML file it
// reads the URI out of, which is the same mechanism the admin URI already uses: 0600, owned
// by root, in ratline's own directory. /proc/PID/cmdline is world-readable, and a URI on a
// command line is the admin password for every database on the server, visible to every
// account on it for as long as the dump runs.

// DumpResult is what a dump produced.
type DumpResult struct {
	Path     string
	Bytes    int64
	Database string
}

// Dump writes an archive of one database.
func (m *Manager) Dump(ctx context.Context, database, outDir string) (*DumpResult, error) {
	if !m.Bins.Available("mongodump") {
		return nil, missingTools("mongodump")
	}
	uri, err := m.AdminURI()
	if err != nil {
		return nil, err
	}

	stamp := time.Now().UTC().Format("20060102T150405Z")
	path := filepath.Join(outDir, fmt.Sprintf("%s-%s.archive.gz", database, stamp))

	if m.DryRun {
		m.Log.Info("would dump", "database", database, "to", path)
		return &DumpResult{Path: path, Database: database}, nil
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "creating %s", outDir)
	}

	cfg, cleanup, err := m.stageToolConfig(uri)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// --archive with --gzip gives one file rather than a directory tree, which is what
	// somebody wants to copy off the server. --db scopes it: dumping the whole server by
	// accident because a flag was forgotten is not a mistake worth making available.
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "mongodump",
		Args: []string{
			"--config", cfg,
			"--db", database,
			"--gzip",
			"--archive=" + path,
			"--quiet",
		},
		Mutates: false,
		Label:   "dump " + database,
	}); err != nil {
		// A partial archive is worse than none: it looks like a backup.
		_ = os.Remove(path)
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "dumping %s", database)
	}

	fi, err := os.Stat(path)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "the dump wrote nothing to %s", path)
	}
	// Root-only. A dump is every row in the database, and it lands next to whatever else
	// is in that directory.
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "securing %s", path)
	}
	return &DumpResult{Path: path, Bytes: fi.Size(), Database: database}, nil
}

// Restore puts an archive back.
//
// into may name a different database from the one the archive came from, which is how a
// production dump gets loaded into staging without editing the archive.
func (m *Manager) Restore(ctx context.Context, archive, from, into string, drop bool) error {
	if !m.Bins.Available("mongorestore") {
		return missingTools("mongorestore")
	}
	if !system.Exists(archive) {
		// Precondition rather than usage, matching `ratline restore`, which exits 3 for
		// exactly this. Two commands answering "the archive is not there" with different
		// codes is the kind of inconsistency automation branches on and gets wrong.
		return rlerr.Preconditionf("no such archive: %s", archive)
	}
	uri, err := m.AdminURI()
	if err != nil {
		return err
	}
	if m.DryRun {
		m.Log.Info("would restore", "archive", archive, "into", into, "drop", drop)
		return nil
	}

	cfg, cleanup, err := m.stageToolConfig(uri)
	if err != nil {
		return err
	}
	defer cleanup()

	args := []string{"--config", cfg, "--gzip", "--archive=" + archive, "--quiet"}
	if from != "" && into != "" && from != into {
		// mongorestore renames with a namespace mapping rather than a --db flag when the
		// archive knows which database it came from.
		args = append(args, "--nsFrom="+from+".*", "--nsTo="+into+".*")
	} else if into != "" {
		args = append(args, "--nsInclude="+into+".*")
	}
	if drop {
		args = append(args, "--drop")
	}

	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "mongorestore", Args: args,
		Mutates: true, Label: "restore into " + into,
	}); err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "restoring %s", archive)
	}
	return nil
}

// stageToolConfig writes the URI where mongodump can read it and argv cannot carry it.
//
// The same reasoning as the admin URI file it came from: /proc/PID/cmdline is
// world-readable, so a connection string passed as --uri is the admin password for the
// whole server, readable by every account on it for as long as the command runs. The
// database tools take --config precisely so that it does not have to be.
func (m *Manager) stageToolConfig(uri string) (path string, cleanup func(), err error) {
	dir := filepath.Join(m.Cfg.Paths.RunDir, "staging")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, rlerr.Wrap(err, rlerr.CodeGeneric, "creating %s", dir)
	}
	f, err := os.CreateTemp(dir, "tools-*.yaml")
	if err != nil {
		return "", nil, rlerr.Wrap(err, rlerr.CodeGeneric, "staging the tool config")
	}
	name := f.Name()
	cleanup = func() { _ = os.Remove(name) }

	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, rlerr.Wrap(err, rlerr.CodeGeneric, "securing %s", name)
	}
	// A YAML scalar, quoted, because a URI routinely contains characters YAML would
	// otherwise read as structure — a password with a colon in it being the obvious one.
	if _, err := fmt.Fprintf(f, "uri: %q\n", uri); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, rlerr.Wrap(err, rlerr.CodeGeneric, "writing %s", name)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, rlerr.Wrap(err, rlerr.CodeGeneric, "closing %s", name)
	}
	return name, cleanup, nil
}

func missingTools(which string) error {
	return rlerr.Preconditionf("%s is not installed", which).
		WithHint("the database tools ship separately from the shell: " +
			"apt-get install mongodb-database-tools")
}

// ArchiveDatabase reads which database an archive holds, from its filename.
//
// mongodump's archive format records this internally, but reading it means parsing a
// binary header. The filename ratline writes carries it, and an archive from elsewhere can
// be told to restore into a named database explicitly.
func ArchiveDatabase(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".archive.gz")
	// name-20260807T120000Z
	if i := strings.LastIndex(base, "-"); i > 0 {
		stamp := base[i+1:]
		if len(stamp) == 16 && strings.HasSuffix(stamp, "Z") {
			return base[:i]
		}
	}
	return ""
}
