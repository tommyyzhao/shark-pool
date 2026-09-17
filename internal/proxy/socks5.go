package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

// SOCKS5Proxy implements CONNECT-only SOCKS5 (no auth).
type SOCKS5Proxy struct {
	Addr string

	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{}
}

func NewSOCKS5(addr string) *SOCKS5Proxy {
	return &SOCKS5Proxy{Addr: addr, conns: make(map[net.Conn]struct{})}
}

func (p *SOCKS5Proxy) Start() error {
	ln, err := net.Listen("tcp", p.Addr)
	if err != nil {
		return fmt.Errorf("socks5 listen %s: %w", p.Addr, err)
	}
	p.mu.Lock()
	p.listener = ln
	p.mu.Unlock()
	go p.serve(ln)
	log.Printf("socks5 proxy listening on %s", p.Addr)
	return nil
}

func (p *SOCKS5Proxy) Close() error {
	p.mu.Lock()
	ln := p.listener
	p.listener = nil
	for c := range p.conns {
		_ = c.Close()
	}
	p.conns = make(map[net.Conn]struct{})
	p.mu.Unlock()
	if ln != nil {
		return ln.Close()
	}
	return nil
}

func (p *SOCKS5Proxy) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		p.conns[c] = struct{}{}
		p.mu.Unlock()
		go p.handle(c)
	}
}

func (p *SOCKS5Proxy) handle(c net.Conn) {
	defer func() {
		p.mu.Lock()
		delete(p.conns, c)
		p.mu.Unlock()
		_ = c.Close()
	}()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))

	// greeting: VER NMETHODS METHODS...
	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil {
		return
	}
	if head[0] != 0x05 {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	// no-auth only
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// request: VER CMD RSV ATYP ADDR PORT
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return
	}
	if req[0] != 0x05 || req[1] != 0x01 { // CONNECT only
		p.reply(c, 0x07, net.IPv4zero, 0)
		return
	}

	var host string
	switch req[3] {
	case 0x01: // IPv4
		ip := make([]byte, 4)
		if _, err := io.ReadFull(c, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 0x03: // domain
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return
		}
		name := make([]byte, int(l[0]))
		if _, err := io.ReadFull(c, name); err != nil {
			return
		}
		host = string(name)
	case 0x04: // IPv6
		ip := make([]byte, 16)
		if _, err := io.ReadFull(c, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	default:
		p.reply(c, 0x08, net.IPv4zero, 0)
		return
	}

	portB := make([]byte, 2)
	if _, err := io.ReadFull(c, portB); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portB)
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))

	up, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		p.reply(c, 0x05, net.IPv4zero, 0)
		return
	}
	defer up.Close()

	if err := p.reply(c, 0x00, net.IPv4zero, 0); err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	_ = up.SetDeadline(time.Time{})
	bidirectional(c, up)
}

func (p *SOCKS5Proxy) reply(c net.Conn, rep byte, ip net.IP, port uint16) error {
	ip4 := ip.To4()
	if ip4 == nil {
		ip4 = net.IPv4zero.To4()
	}
	out := []byte{0x05, rep, 0x00, 0x01}
	out = append(out, ip4...)
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], port)
	out = append(out, pb[:]...)
	_, err := c.Write(out)
	return err
}
