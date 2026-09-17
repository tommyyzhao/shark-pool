package proxy

import (
	"bufio"
	"net"
)

func newBufReader(c net.Conn) *bufio.Reader {
	return bufio.NewReader(c)
}
