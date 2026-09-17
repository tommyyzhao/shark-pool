package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestHTTPProxyCONNECTAndAbsolute(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello-from-backend"))
	}))
	defer backend.Close()

	p := NewHTTP("127.0.0.1:0")
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// grab actual listen addr
	addr := p.listener.Addr().String()

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustURL(t, "http://"+addr)),
		},
	}
	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello-from-backend" {
		t.Fatalf("body = %q", body)
	}
}

func TestSOCKS5Dial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte("socks-ok"))
	}()

	p := NewSOCKS5("127.0.0.1:0")
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	proxyAddr := p.listener.Addr().String()

	// minimal SOCKS5 CONNECT client
	conn, err := net.DialTimeout("tcp", proxyAddr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// greeting
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil || resp[1] != 0x00 {
		t.Fatalf("auth reply %v %v", resp, err)
	}
	// CONNECT to backend
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	ip := net.ParseIP(host).To4()
	pnum, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	req := []byte{0x05, 0x01, 0x00, 0x01}
	req = append(req, ip...)
	req = append(req, byte(pnum>>8), byte(pnum&0xff))
	if _, err := conn.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("connect reply %v", reply)
	}
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "socks-ok" {
		t.Fatalf("payload %q", buf)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
