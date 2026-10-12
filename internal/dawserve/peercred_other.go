//go:build !darwin && !linux

package dawserve

import (
	"errors"
	"net"
)

// peerUID is unsupported here, so serve sockets are refused.
func peerUID(*net.UnixConn) (int, error) {
	return -1, errors.New("dawserve: peer credentials are not supported on this platform")
}
