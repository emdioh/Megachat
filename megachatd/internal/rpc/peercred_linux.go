//go:build linux

package rpc

import (
	"net"
	"syscall"
)

// peerUID returns the peer uid via SO_PEERCRED; -1 when the lookup failed.
func peerUID(c net.Conn) (int, bool) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, true
	}
	uid := -1
	cerr := raw.Control(func(fd uintptr) {
		cred, e := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if e == nil {
			uid = int(cred.Uid)
		}
	})
	if cerr != nil {
		return -1, true
	}
	return uid, true
}
