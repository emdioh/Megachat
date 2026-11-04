//go:build !linux

package rpc

import "net"

// peerUID: unsupported here; permissions alone protect the socket.
func peerUID(net.Conn) (int, bool) { return 0, false }
