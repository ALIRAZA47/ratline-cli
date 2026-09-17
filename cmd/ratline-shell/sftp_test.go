package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

// pipeRWC is one end of an in-process SSH channel: the server reads what the client
// writes and vice versa.
type pipeRWC struct {
	io.Reader
	io.WriteCloser
}

// newSFTPClient wires a real SFTP client to serveSFTP's handlers over pipes, so the
// tests exercise the protocol's own path handling rather than only the Go functions.
func newSFTPClient(t *testing.T, root string) *sftp.Client {
	t.Helper()
	srvIn, cliOut := io.Pipe()
	cliIn, srvOut := io.Pipe()
	fs := &siteFS{root: root}
	server := sftp.NewRequestServer(pipeRWC{srvIn, srvOut},
		sftp.Handlers{FileGet: fs, FilePut: fs, FileCmd: fs, FileList: fs})
	go func() {
		// Serve returns when the client closes its side; closing ours is what lets the
		// client's receive loop see EOF and its Close return. In production sshd owns
		// both ends and the process exits, so the wrapper's stdio Close does nothing.
		_ = server.Serve()
		_ = srvOut.Close()
	}()
	client, err := sftp.NewClientPipe(cliIn, cliOut)
	if err != nil {
		t.Fatalf("connecting the client: %v", err)
	}
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return client
}

// A site directory with a file, a public/ subdirectory, a symlink that stays inside
// the site and a symlink that escapes it.
func sftpFixture(t *testing.T) (root string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "example.com")
	for _, d := range []string{"public", "app"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "public", "index.html"), []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("public", filepath.Join(root, "current")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink("/", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	// Something outside the site that a confined session must never reach: the
	// tenant's ~/.ssh, one level up.
	if err := os.MkdirAll(filepath.Join(base, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".ssh", "authorized_keys"), []byte("ssh-ed25519 AAAA real\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// The whole point: a scoped key that only has SFTP could previously `cd ..` to the
// tenant's home and append an unrestricted key to ~/.ssh/authorized_keys. Now "/" is
// the site, and no spelling of a path reaches above it.
func TestSFTPCannotLeaveTheSiteDirectory(t *testing.T) {
	root := sftpFixture(t)
	c := newSFTPClient(t, root)

	if wd, err := c.Getwd(); err != nil || wd != "/" {
		t.Fatalf("Getwd = %q, %v; want the site as /", wd, err)
	}
	for _, p := range []string{
		"/../.ssh/authorized_keys",
		"../.ssh/authorized_keys",
		"/../../../../etc/passwd",
		"/.ssh/authorized_keys",
	} {
		if _, err := c.Stat(p); err == nil {
			t.Errorf("Stat(%q) succeeded; the path resolved outside the site", p)
		}
		if f, err := c.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND); err == nil {
			f.Close()
			t.Errorf("OpenFile(%q) for append succeeded; a scoped key could add itself a shell", p)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), ".ssh", "authorized_keys")); err != nil {
		t.Fatalf("the fixture's authorized_keys should be untouched: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(filepath.Dir(root), ".ssh", "authorized_keys"))
	if string(data) != "ssh-ed25519 AAAA real\n" {
		t.Errorf("authorized_keys was modified through SFTP: %q", data)
	}
}

// A symlink inside the site that points outside is the other way out, and the one
// `-d` never closed either. Following it is refused; a link that stays inside works.
func TestSFTPRefusesASymlinkThatEscapesAndFollowsOneThatDoesNot(t *testing.T) {
	root := sftpFixture(t)
	c := newSFTPClient(t, root)

	if _, err := c.Stat("/escape/etc"); err == nil {
		t.Error("Stat through a symlink to / succeeded")
	}
	if _, err := c.ReadDir("/escape"); err == nil {
		t.Error("ReadDir through a symlink to / succeeded")
	}
	if _, err := c.Open("/escape/etc/hostname"); err == nil {
		t.Error("Open through a symlink to / succeeded")
	}
	entries, err := c.ReadDir("/current")
	if err != nil {
		t.Fatalf("a symlink that stays inside the site should be followed: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "index.html" {
		t.Errorf("ReadDir(/current) = %v", entries)
	}
	f, err := c.Open("/current/index.html")
	if err != nil {
		t.Fatalf("Open through an inside link: %v", err)
	}
	body, _ := io.ReadAll(f)
	f.Close()
	if string(body) != "<h1>hi</h1>" {
		t.Errorf("read %q", body)
	}
}

// The ordinary deploy workflow has to keep working: upload, list, rename, mkdir,
// remove, chmod. Otherwise the confinement would be achieved by breaking the feature.
func TestSFTPServesTheSiteNormally(t *testing.T) {
	root := sftpFixture(t)
	c := newSFTPClient(t, root)

	f, err := c.Create("/public/new.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.Write([]byte("uploaded")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if data, err := os.ReadFile(filepath.Join(root, "public", "new.txt")); err != nil || string(data) != "uploaded" {
		t.Fatalf("the upload did not land in the site: %q, %v", data, err)
	}
	if err := c.Chmod("/public/new.txt", 0o600); err != nil {
		t.Errorf("Chmod: %v", err)
	}
	if fi, _ := os.Stat(filepath.Join(root, "public", "new.txt")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode after Chmod = %04o", fi.Mode().Perm())
	}
	if err := c.Rename("/public/new.txt", "/public/moved.txt"); err != nil {
		t.Errorf("Rename: %v", err)
	}
	// SFTP's rename must not clobber; an existing target is an error.
	if err := c.Rename("/public/moved.txt", "/public/index.html"); err == nil {
		t.Error("Rename onto an existing file succeeded")
	}
	if err := c.Mkdir("/public/assets"); err != nil {
		t.Errorf("Mkdir: %v", err)
	}
	entries, err := c.ReadDir("/public")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, ","); got != "assets,index.html,moved.txt" {
		t.Errorf("ReadDir(/public) = %s", got)
	}
	if err := c.RemoveDirectory("/public/assets"); err != nil {
		t.Errorf("RemoveDirectory: %v", err)
	}
	if err := c.Remove("/public/moved.txt"); err != nil {
		t.Errorf("Remove: %v", err)
	}
	if err := c.Remove("/public"); err == nil {
		t.Error("Remove on a directory succeeded; that is Rmdir's job")
	}
	// Relative paths resolve against the site, exactly as sftp-server -d did.
	if _, err := c.Stat("public/index.html"); err != nil {
		t.Errorf("a relative path did not resolve against the site: %v", err)
	}
}

// Links are refused: a symlink pointing outside would be followed by rsync and by the
// application even though this server refuses it, and a hard link shares an inode with
// a file that may be anywhere.
func TestSFTPRefusesToCreateLinks(t *testing.T) {
	root := sftpFixture(t)
	c := newSFTPClient(t, root)

	if err := c.Symlink("/", "/public/out"); err == nil {
		t.Error("Symlink was allowed")
	}
	if err := c.Link("/public/index.html", "/public/hard"); err == nil {
		t.Error("Link was allowed")
	}
	if _, err := os.Lstat(filepath.Join(root, "public", "out")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a symlink was created on disk")
	}
	// Reading a link's text is fine — it is the tenant's own data — but the target is
	// refused the moment a client tries to use it.
	if target, err := c.ReadLink("/escape"); err != nil || target != "/" {
		t.Errorf("ReadLink(/escape) = %q, %v", target, err)
	}
	// An escaping link that already exists can be seen and removed — it is the
	// tenant's own entry — but never followed.
	if fi, err := c.Lstat("/escape"); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("Lstat(/escape) = %v, %v; want the link itself", fi, err)
	}
	if err := c.Remove("/escape"); err != nil {
		t.Errorf("Remove(/escape) = %v; removing an escaping link is harmless", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the link was not removed")
	}
}

func TestSiteFSResolveKeepsEveryPathUnderTheRoot(t *testing.T) {
	root := sftpFixture(t)
	fs := &siteFS{root: root}
	for virtual, want := range map[string]string{
		"/":                   root,
		"/public":             filepath.Join(root, "public"),
		"public/index.html":   filepath.Join(root, "public", "index.html"),
		"/../../etc/passwd":   filepath.Join(root, "etc", "passwd"),
		"/does/not/exist/yet": filepath.Join(root, "does", "not", "exist", "yet"),
	} {
		got, err := fs.resolve(virtual)
		if err != nil || got != want {
			t.Errorf("resolve(%q) = %q, %v; want %q", virtual, got, err, want)
		}
	}
	if _, err := fs.resolve("/escape/etc/passwd"); !errors.Is(err, sftp.ErrSSHFxPermissionDenied) {
		t.Errorf("a path through an escaping symlink resolved: %v", err)
	}
}
