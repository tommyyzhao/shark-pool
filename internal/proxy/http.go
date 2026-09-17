package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTPProxy is a minimal HTTP/HTTPS proxy (CONNECT + absolute-URI).
type HTTPProxy struct {
	Addr       string
	BindDevice string // e.g. tun0 — pin outbound sockets to this iface

	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{}
}

func NewHTTP(addr string) *HTTPProxy {
	return &HTTPProxy{Addr: addr, conns: make(map[net.Conn]struct{})}
}

// NewHTTPBindDevice creates an HTTP proxy that dials out via a specific network device.
func NewHTTPBindDevice(addr, bindDevice string) *HTTPProxy {
	p := NewHTTP(addr)
	p.BindDevice = bindDevice
	return p
}

func (p *HTTPProxy) Start() error {
	ln, err := net.Listen("tcp", p.Addr)
	if err != nil {
		return fmt.Errorf("http proxy listen %s: %w", p.Addr, err)
	}
	p.mu.Lock()
	p.listener = ln
	p.mu.Unlock()
	go p.serve(ln)
	log.Printf("http proxy listening on %s", p.Addr)
	return nil
}

func (p *HTTPProxy) Close() error {
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

func (p *HTTPProxy) track(c net.Conn) {
	p.mu.Lock()
	p.conns[c] = struct{}{}
	p.mu.Unlock()
}

func (p *HTTPProxy) untrack(c net.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
}

func (p *HTTPProxy) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		p.track(c)
		go p.handle(c)
	}
}

func (p *HTTPProxy) handle(c net.Conn) {
	defer func() {
		p.untrack(c)
		_ = c.Close()
	}()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))

	req, err := http.ReadRequest(newBufReader(c))
	if err != nil {
		return
	}

	if req.Method == http.MethodConnect {
		p.handleConnect(c, req)
		return
	}
	p.handleHTTP(c, req)
}

func (p *HTTPProxy) handleConnect(c net.Conn, req *http.Request) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	up, err := dialContext(context.Background(), "tcp", host, p.BindDevice, 15*time.Second)
	if err != nil {
		_, _ = c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer up.Close()
	_, _ = c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	_ = c.SetDeadline(time.Time{})
	_ = up.SetDeadline(time.Time{})
	bidirectional(c, up)
}

func (p *HTTPProxy) handleHTTP(c net.Conn, req *http.Request) {
	if !req.URL.IsAbs() {
		_, _ = c.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"))
		return
	}
	outReq, err := http.NewRequestWithContext(context.Background(), req.Method, req.URL.String(), req.Body)
	if err != nil {
		_, _ = c.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"))
		return
	}
	outReq.Header = req.Header.Clone()
	removeHopByHop(outReq.Header)
	if outReq.Header.Get("Host") == "" {
		outReq.Host = req.URL.Host
	}

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       60 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialContext(ctx, network, addr, p.BindDevice, 15*time.Second)
			},
		},
	}
	resp, err := client.Do(outReq)
	if err != nil {
		_, _ = c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n"))
		return
	}
	defer resp.Body.Close()
	removeHopByHop(resp.Header)

	status := resp.Status
	if status == "" {
		status = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	if _, err := fmt.Fprintf(c, "HTTP/1.1 %s\r\n", status); err != nil {
		return
	}
	if err := resp.Header.Write(c); err != nil {
		return
	}
	if _, err := fmt.Fprintf(c, "\r\n"); err != nil {
		return
	}
	if req.Method != http.MethodHead {
		_, _ = io.Copy(c, resp.Body)
	}
}

func removeHopByHop(h http.Header) {
	for _, k := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive",
		"Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		h.Del(k)
	}
}

func bidirectional(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if t, ok := dst.(*net.TCPConn); ok {
			_ = t.CloseWrite()
		} else if t, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = t.CloseWrite()
		}
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}

// ParseProxyURL is a small helper for tests / callers.
func ParseProxyURL(raw string) (*url.URL, error) {
	return url.Parse(raw)
}
