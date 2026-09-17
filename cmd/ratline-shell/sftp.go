package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/sftp"
)

// serveSFTP answers the SFTP protocol on stdin and stdout, in this process, with the
// site directory as the whole of the filesystem the client can see.
//
// OpenSSH's sftp-server cannot confine a session: -d sets the starting directory and
// nothing more, so a client could `cd ..` into the tenant's home, read a sibling site's
// .env, or append an unrestricted key to ~/.ssh/authorized_keys and log in again with a
// shell — turning a "file transfer only" contractor key into the tenant's account. Every
// rsync, scp -O and git request is checked path by path in escapingPath; SFTP was the one
// door that was not, and a modern scp speaks SFTP by default.
//
// So the server is this binary. "/" is the site directory; every path a client names is
// resolved, symlinks included, and refused unless it lands inside; hard links and
// symlinks cannot be created at all. It runs as the tenant's UID like everything else the
// wrapper starts, so a bug here is bounded by what that UID could do anyway — the point
// is that a scoped key cannot do it.
func serveSFTP(siteDir string) int {
	fs := &siteFS{root: siteDir}
	srv := sftp.NewRequestServer(stdio{}, sftp.Handlers{FileGet: fs, FilePut: fs, FileCmd: fs, FileList: fs})
	err := srv.Serve()
	_ = srv.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "ratline-shell: sftp: %v\n", err)
		return exitDenied
	}
	return exitOK
}

// stdio is the SSH channel as sshd hands it to a subsystem: stdin in, stdout out.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return nil }

// siteFS serves one directory as the root of an SFTP session.
type siteFS struct {
	root string
}

// resolve maps a client's path onto the filesystem and refuses one that leaves the site.
//
// The request server has already made the path absolute and lexically clean, so ".." is
// gone; what remains is symlinks, which a tenant may legitimately have inside the site
// (current → releases/3) and which may point anywhere. The longest existing prefix is
// resolved through them and the result has to stay under the root — the same check
// escapingPath applies to rsync and scp arguments, so the two kinds of session cannot
// disagree about where the edge is.
func (fs *siteFS) resolve(virtual string) (string, error) {
	rel := strings.TrimPrefix(filepath.Clean("/"+virtual), "/")
	candidate := filepath.Join(fs.root, rel)
	if pathEscapes(fs.root, candidate) {
		return "", sftp.ErrSSHFxPermissionDenied
	}
	return candidate, nil
}

// resolveNoFollow is resolve for an operation on a directory entry itself rather than on
// what it refers to — Lstat, Readlink, Remove, Rename. The parent is resolved and must
// be inside the site; the final component is taken as it is, so a symlink that points
// outside can be inspected or removed but never followed.
func (fs *siteFS) resolveNoFollow(virtual string) (string, error) {
	clean := filepath.Clean("/" + virtual)
	if clean == "/" {
		return fs.root, nil
	}
	parent, err := fs.resolve(filepath.Dir(clean))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(clean)), nil
}

func (fs *siteFS) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	if !r.Pflags().Read {
		return nil, os.ErrInvalid
	}
	return fs.open(r)
}

func (fs *siteFS) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	if !r.Pflags().Write {
		return nil, os.ErrInvalid
	}
	return fs.open(r)
}

func (fs *siteFS) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) {
	return fs.open(r)
}

func (fs *siteFS) open(r *sftp.Request) (*os.File, error) {
	path, err := fs.resolve(r.Filepath)
	if err != nil {
		return nil, err
	}
	pf := r.Pflags()
	flags := os.O_RDONLY
	switch {
	case pf.Read && pf.Write:
		flags = os.O_RDWR
	case pf.Write:
		flags = os.O_WRONLY
	}
	if pf.Append {
		flags |= os.O_APPEND
	}
	if pf.Creat {
		flags |= os.O_CREATE
	}
	if pf.Trunc {
		flags |= os.O_TRUNC
	}
	if pf.Excl {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, requestedMode(r, 0o644))
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err == nil && fi.IsDir() {
		f.Close()
		return nil, syscall.EISDIR
	}
	return f, nil
}

func (fs *siteFS) Filecmd(r *sftp.Request) error {
	// Commands that act on an entry rather than through it take the entry as it is.
	var path string
	var err error
	switch r.Method {
	case "Rename", "PosixRename", "Rmdir", "Remove":
		path, err = fs.resolveNoFollow(r.Filepath)
	default:
		path, err = fs.resolve(r.Filepath)
	}
	if err != nil {
		return err
	}
	switch r.Method {
	case "Setstat":
		attrs, flags := r.Attributes(), r.AttrFlags()
		if attrs == nil {
			return nil
		}
		if flags.Size {
			if err := os.Truncate(path, int64(attrs.Size)); err != nil {
				return err
			}
		}
		if flags.Permissions {
			if err := os.Chmod(path, os.FileMode(attrs.Mode)&os.ModePerm); err != nil {
				return err
			}
		}
		if flags.Acmodtime {
			if err := os.Chtimes(path, time.Unix(int64(attrs.Atime), 0), time.Unix(int64(attrs.Mtime), 0)); err != nil {
				return err
			}
		}
		// UidGid is ignored rather than refused: this process is the tenant and could
		// not chown to anybody else anyway, and clients send it routinely on upload.
		return nil

	case "Rename", "PosixRename":
		target, err := fs.resolveNoFollow(r.Target)
		if err != nil {
			return err
		}
		if r.Method == "Rename" {
			// SFTP's rename must not replace an existing file; POSIX rename may.
			if _, err := os.Lstat(target); err == nil {
				return os.ErrExist
			}
		}
		return os.Rename(path, target)

	case "Rmdir":
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			return syscall.ENOTDIR
		}
		return os.Remove(path)

	case "Remove":
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return syscall.EISDIR
		}
		return os.Remove(path)

	case "Mkdir":
		return os.Mkdir(path, requestedMode(r, 0o755))

	case "Link", "Symlink":
		// A link is a path that means something else. A symlink pointing outside the
		// site would be refused on every later request here, but rsync and the
		// application itself would follow it; a hard link shares an inode with a file
		// that may be outside. Neither is something a file-transfer key needs.
		return sftp.ErrSSHFxPermissionDenied

	default:
		return sftp.ErrSSHFxOpUnsupported
	}
}

func (fs *siteFS) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	path, err := fs.resolve(r.Filepath)
	if err != nil {
		return nil, err
	}
	switch r.Method {
	case "List":
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		infos := make([]os.FileInfo, 0, len(entries))
		for _, e := range entries {
			if fi, err := e.Info(); err == nil {
				infos = append(infos, fi)
			}
		}
		return listerAt(infos), nil
	case "Stat":
		fi, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		return listerAt{fi}, nil
	default:
		return nil, sftp.ErrSSHFxOpUnsupported
	}
}

func (fs *siteFS) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	path, err := fs.resolveNoFollow(r.Filepath)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	return listerAt{fi}, nil
}

// Readlink returns a link's text. It is the tenant's own data, and a target outside the
// site is refused the moment a client asks for it, so nothing is resolved here.
func (fs *siteFS) Readlink(p string) (string, error) {
	path, err := fs.resolveNoFollow(p)
	if err != nil {
		return "", err
	}
	return os.Readlink(path)
}

// requestedMode is the permission bits a client asked for, or fallback when it sent
// none. Attributes can be absent even when the flag says otherwise — a client's
// Create sends the flag with no bytes behind it — so both are checked.
func requestedMode(r *sftp.Request, fallback os.FileMode) os.FileMode {
	if attrs := r.Attributes(); attrs != nil && r.AttrFlags().Permissions {
		return os.FileMode(attrs.Mode) & os.ModePerm
	}
	return fallback
}

type listerAt []os.FileInfo

func (l listerAt) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(ls, l[offset:])
	if n < len(ls) {
		return n, io.EOF
	}
	return n, nil
}
