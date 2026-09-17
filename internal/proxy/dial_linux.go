package proxy

import (
	"context"
	"net"
	"syscall"
	"time"
)

// newDialer returns a dialer that optionally pins outbound sockets to a device (e.g. tun0).
func newDialer(bindDevice string, timeout time.Duration) *net.Dialer {
	d := &net.Dialer{Timeout: timeout}
	if bindDevice == "" {
		return d
	}
	d.Control = func(network, address string, c syscall.RawConn) error {
		var opErr error
		if err := c.Control(func(fd uintptr) {
			opErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, bindDevice)
		}); err != nil {
			return err
		}
		return opErr
	}
	return d
}

func dialContext(ctx context.Context, network, address, bindDevice string, timeout time.Duration) (net.Conn, error) {
	return newDialer(bindDevice, timeout).DialContext(ctx, network, address)
}
