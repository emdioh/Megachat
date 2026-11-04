package rpc

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ErrLocked means another live process holds the single-instance lock.
var ErrLocked = errors.New("another instance is already running")

// LockHolder returns the pid in the lock file if that process is alive.
func LockHolder(lockFile string) (int, bool) {
	b, err := os.ReadFile(lockFile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	if err := syscall.Kill(pid, 0); err != nil && err != syscall.EPERM {
		return 0, false
	}
	return pid, true
}

// AcquireLock ports server/lock.py _acquire_lock: check-and-write of the pid
// file, serialised by flock on <lock>.guard. It returns a release function
// (removes the file if it still holds our pid). Like Python, an unexpected
// failure lets the caller start anyway (nil release is never returned with
// a nil error).
func AcquireLock(lockFile string) (release func(), err error) {
	guard, gerr := os.OpenFile(lockFile+".guard", os.O_CREATE|os.O_RDWR, 0o600)
	if gerr == nil {
		defer guard.Close() // closing releases the flock
		_ = syscall.Flock(int(guard.Fd()), syscall.LOCK_EX)
	}
	if pid, ok := LockHolder(lockFile); ok && pid != os.Getpid() {
		return nil, fmt.Errorf("%w (pid %d)", ErrLocked, pid)
	}
	mine := strconv.Itoa(os.Getpid())
	if werr := os.WriteFile(lockFile, []byte(mine), 0o644); werr != nil {
		return func() {}, nil
	}
	return func() {
		if b, rerr := os.ReadFile(lockFile); rerr == nil && strings.TrimSpace(string(b)) == mine {
			_ = os.Remove(lockFile)
		}
	}, nil
}
