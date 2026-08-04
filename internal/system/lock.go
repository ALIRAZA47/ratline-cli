package system

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// LockHolder is written into the lock file so a blocked invocation can say who
// is holding it rather than just "try again".
type LockHolder struct {
	PID     int       `json:"pid"`
	Command string    `json:"command"`
	Since   time.Time `json:"since"`
}

// Lock is an exclusive advisory lock held for the duration of a mutating
// command. Two concurrent provisioning runs would race on nginx configs and
// systemd units, so the second one fails fast instead.
type Lock struct {
	path string
	f    *os.File
}

// AcquireLock takes the lock, waiting up to wait for a holder to finish.
func AcquireLock(path string, wait time.Duration, command string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "creating the lock directory for %s", path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "opening the lock file %s", path)
	}

	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			l := &Lock{path: path, f: f}
			l.writeHolder(command)
			return l, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "locking %s", path)
		}
		if !time.Now().Before(deadline) {
			holder := readHolder(f)
			f.Close()
			e := rlerr.Lockedf("another ratline command is already running")
			if holder != nil {
				e = rlerr.Lockedf("another ratline command is already running: %q (pid %d, started %s ago)",
					holder.Command, holder.PID, time.Since(holder.Since).Round(time.Second))
			}
			return nil, e.WithHint("wait for it to finish, or check with: systemctl status; ps -p <pid>")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Release drops the lock. The file itself is left in place: removing it would
// race with another process that has already opened it.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = l.f.Truncate(0)
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	cerr := l.f.Close()
	l.f = nil
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "unlocking %s", l.path)
	}
	if cerr != nil {
		return rlerr.Wrap(cerr, rlerr.CodeGeneric, "closing %s", l.path)
	}
	return nil
}

func (l *Lock) writeHolder(command string) {
	h := LockHolder{PID: os.Getpid(), Command: command, Since: time.Now().UTC()}
	b, err := json.Marshal(h)
	if err != nil {
		return
	}
	if err := l.f.Truncate(0); err != nil {
		return
	}
	if _, err := l.f.WriteAt(b, 0); err != nil {
		return
	}
	_ = l.f.Sync()
}

func readHolder(f *os.File) *LockHolder {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	var h LockHolder
	if err := json.Unmarshal(b, &h); err != nil {
		return nil
	}
	return &h
}
