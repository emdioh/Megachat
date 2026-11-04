package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Port of server/lock.py: single-instance lock file holding the owner's pid,
// check-and-write serialised by flock on <lock>.guard.

// Lock is a single-instance lock.
type Lock struct{ Path string }

// NewLock returns a Lock on path.
func NewLock(path string) *Lock { return &Lock{Path: path} }

func (l *Lock) guard() func() {
	f, err := os.OpenFile(l.Path+".guard", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return func() {}
	}
	return func() { f.Close() } // closing releases the flock
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func (l *Lock) readPID() (int, bool) {
	data, err := os.ReadFile(l.Path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

// Holder returns the pid in the lock file if that process is alive.
func (l *Lock) Holder() (int, bool) {
	pid, ok := l.readPID()
	if !ok || !pidAlive(pid) {
		return 0, false
	}
	return pid, true
}

// Acquire takes the lock. It returns false when another live process holds
// it. Any unexpected failure allows startup (returns true), as in Python.
func (l *Lock) Acquire() bool {
	defer l.guard()()
	if _, err := os.Stat(l.Path); err == nil {
		if pid, ok := l.readPID(); ok && pid != os.Getpid() && pidAlive(pid) {
			return false
		}
	}
	if err := os.WriteFile(l.Path, []byte(fmt.Sprint(os.Getpid())), 0o644); err != nil {
		return true
	}
	return true
}

// Release removes the lock file if it belongs to this process.
func (l *Lock) Release() {
	if pid, ok := l.readPID(); ok && pid == os.Getpid() {
		_ = os.Remove(l.Path)
	}
}
