//go:build !linux

package proxy

import (
	"context"
	"net"
	"time"
)

func newDialer(bindDevice string, timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout}
}

func dialContext(ctx context.Context, network, address, bindDevice string, timeout time.Duration) (net.Conn, error) {
	return (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, address)
}
