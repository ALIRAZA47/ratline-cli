package system

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// maxLinkHops bounds symlink resolution in OpenDirNoFollow, the same way the kernel
// bounds it, so a loop of root-owned links is an error rather than a hang.
const maxLinkHops = 40

// OpenDirNoFollow opens a directory without resolving any path component through a
// symbolic link that root did not create, and returns a descriptor for it.
//
// This is the primitive every root operation inside a tenant-owned tree is built on.
// The kernel resolves a path component by component and follows every symlink it meets,
// so a tenant who owns /home/acme can rename a real subdirectory away and put a link to
// /etc in its place between any two syscalls ratline makes by path — the classic
// check-then-use race. Checking with Lstat and then acting by path does not close it;
// only acting through a descriptor obtained before the check can be invalidated does.
//
// The walk opens each component relative to the previous one with O_NOFOLLOW, so a
// link anywhere along the way fails to open rather than being followed. Links owned by
// root are then followed by hand: /var/run → /run and /bin → usr/bin are part of the
// filesystem's own layout, and an attacker who can create a root-owned symlink already
// is root. Anybody else's link is refused by name.
//
// The returned *os.File is a directory; use its descriptor with the *at family of
// syscalls so that what was verified here is what is operated on.
func OpenDirNoFollow(path string) (*os.File, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return nil, rlerr.Genericf("OpenDirNoFollow needs an absolute path, got %q", path)
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "opening /")
	}
	cur := "/"
	pending := splitPath(clean)
	hops := 0
	for len(pending) > 0 {
		comp := pending[0]
		pending = pending[1:]
		if comp == "" || comp == "." {
			continue
		}
		next := filepath.Join(cur, comp)
		nfd, err := unix.Openat(fd, comp, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err == nil {
			unix.Close(fd)
			fd, cur = nfd, next
			continue
		}
		openErr := err

		// The open failed. Linux reports a symlink met under O_NOFOLLOW as ELOOP and
		// macOS as ENOTDIR, so the component is classified by asking what it is rather
		// than by which errno came back.
		var st unix.Stat_t
		if serr := unix.Fstatat(fd, comp, &st, unix.AT_SYMLINK_NOFOLLOW); serr != nil {
			unix.Close(fd)
			return nil, rlerr.Wrap(serr, rlerr.CodeGeneric, "opening %s", next)
		}
		if st.Mode&unix.S_IFMT != unix.S_IFLNK {
			unix.Close(fd)
			if st.Mode&unix.S_IFMT != unix.S_IFDIR {
				return nil, rlerr.Wrap(openErr, rlerr.CodePrecondition, "%s is not a directory", next)
			}
			return nil, rlerr.Wrap(openErr, rlerr.CodeGeneric, "opening %s", next)
		}

		// A symbolic link. Whose?
		if st.Uid != 0 {
			unix.Close(fd)
			return nil, rlerr.Preconditionf("refusing to operate under %s: %s is a symlink owned by uid %d", clean, next, st.Uid).
				WithHint("ratline never resolves a path through a symlink it did not find root owning; " +
					"a directory it manages was replaced with a link to somewhere else")
		}
		hops++
		if hops > maxLinkHops {
			unix.Close(fd)
			return nil, rlerr.Preconditionf("refusing to operate under %s: too many levels of symbolic links", clean)
		}
		target, err := readlinkat(fd, comp)
		if err != nil {
			unix.Close(fd)
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the symlink %s", next)
		}
		if filepath.IsAbs(target) {
			unix.Close(fd)
			fd, err = unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			if err != nil {
				return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "opening /")
			}
			cur = "/"
		}
		pending = append(splitPath(target), pending...)
	}
	return os.NewFile(uintptr(fd), clean), nil
}

func splitPath(p string) []string {
	return strings.Split(strings.Trim(filepath.Clean(p), string(filepath.Separator)), string(filepath.Separator))
}

func readlinkat(dirfd int, name string) (string, error) {
	for size := 256; size <= 1<<16; size *= 2 {
		buf := make([]byte, size)
		n, err := unix.Readlinkat(dirfd, name, buf)
		if err != nil {
			return "", err
		}
		if n < size {
			return string(buf[:n]), nil
		}
	}
	return "", errors.New("symlink target is too long")
}

// openAt opens name relative to a directory descriptor with O_NOFOLLOW, refusing a
// final component that is a symlink.
func openAt(dir *os.File, name string, flags int, perm uint32) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, perm)
	if err != nil {
		return nil, err
	}
	// O_NONBLOCK is passed by readers so that a FIFO put where a file should be does
	// not park a root process for ever; a regular file does not care either way, and
	// Go's poller does not want it set on one.
	if flags&unix.O_NONBLOCK != 0 {
		_ = unix.SetNonblock(fd, false)
	}
	return os.NewFile(uintptr(fd), filepath.Join(dir.Name(), name)), nil
}

// createTempAt creates a fresh, exclusively-owned 0600 file in a directory descriptor.
func createTempAt(dir *os.File, prefix string) (*os.File, string, error) {
	for range 10000 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, "", rlerr.Wrap(err, rlerr.CodeGeneric, "generating a temporary name")
		}
		name := prefix + hex.EncodeToString(b[:])
		f, err := openAt(dir, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL, 0o600)
		if err == nil {
			return f, name, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return nil, "", rlerr.Wrap(err, rlerr.CodeGeneric, "creating a temporary file in %s", dir.Name())
		}
	}
	return nil, "", rlerr.Genericf("could not find a free temporary name in %s", dir.Name())
}

// ReadFileNoFollow reads a regular file of at most max bytes, resolving no path
// component through a symlink root did not create and refusing a final component
// that is a symlink, a FIFO, a device or anything but a regular file.
//
// owner, when not KeepUnchanged, is the uid the file must belong to. A file ratline
// reads as root on a tenant's behalf — a site's .env, a log — lives in a directory
// that tenant owns, so the tenant can put anything there: a link to another tenant's
// .env, a hard link to something of root's, a FIFO that blocks the reader for ever.
// Reading only a regular file the tenant owns is what makes "root reads it for you"
// mean no more than "you could have read it yourself".
//
// A missing file is reported with an error for which errors.Is(err, fs.ErrNotExist)
// holds, so callers that treat absence as empty can keep doing so.
func ReadFileNoFollow(path string, max int64, owner int) ([]byte, error) {
	f, err := OpenFileNoFollow(path, owner)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	clean := f.Name()
	fi, err := f.Stat()
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", clean)
	}
	if fi.Size() > max {
		return nil, rlerr.Preconditionf("%s is %d bytes, which exceeds the %d-byte limit", clean, fi.Size(), max)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", clean)
	}
	if int64(len(data)) > max {
		return nil, rlerr.Preconditionf("%s exceeds the %d-byte limit", clean, max)
	}
	return data, nil
}

// OpenFileNoFollow is ReadFileNoFollow's open: the same walk, the same refusals, and a
// descriptor for the regular file rather than its contents, for a caller that streams
// or follows it. The file is opened read-only.
func OpenFileNoFollow(path string, owner int) (*os.File, error) {
	clean := filepath.Clean(path)
	dir, err := OpenDirNoFollow(filepath.Dir(clean))
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	f, err := openAt(dir, filepath.Base(clean), unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, rlerr.Preconditionf("refusing to read %s: it is a symlink", clean).
				WithHint("ratline reads only regular files here; the link was put there by somebody else")
		}
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "opening %s", clean)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", clean)
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, rlerr.Preconditionf("refusing to read %s: it is not a regular file", clean)
	}
	if owner != KeepUnchanged {
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			f.Close()
			return nil, rlerr.Genericf("cannot read ownership of %s on this platform", clean)
		}
		if int(st.Uid) != owner {
			f.Close()
			return nil, rlerr.Preconditionf("refusing to read %s: it is owned by uid %d, not uid %d", clean, st.Uid, owner).
				WithHint("a file ratline reads on a tenant's behalf has to belong to that tenant")
		}
	}
	return f, nil
}

// notExist reports whether an error, however wrapped, means a path does not exist.
func notExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
