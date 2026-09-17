package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/tommyyzhao/shark-pool/internal/config"
	"github.com/tommyyzhao/shark-pool/internal/vpn"
)

// ExitStatus is the public status payload for one exit.
type ExitStatus struct {
	Exit       string `json:"exit"`
	Connected  bool   `json:"connected"`
	Server     string `json:"server"`
	ExitIP     string `json:"exit_ip"`
	LastError  string `json:"last_error,omitempty"`
	UptimeSec  int64  `json:"uptime_sec"`
	HTTPProxy  string `json:"http_proxy"`
	SOCKSProxy string `json:"socks_proxy"`
	UpdatedAt  string `json:"updated_at"`
}

// NineRouterPool is a paste-ready 9router proxy-pool entry.
type NineRouterPool struct {
	Name        string `json:"name"`
	ProxyURL    string `json:"proxyUrl"`
	Type        string `json:"type"`
	NoProxy     string `json:"noProxy"`
	IsActive    bool   `json:"isActive"`
	StrictProxy bool   `json:"strictProxy"`
}

// Server is the per-exit control API.
type Server struct {
	cfg *config.Exit
	sup *vpn.Supervisor
	// Public host used in 9router export (defaults to 127.0.0.1).
	publicHost string

	mu   sync.Mutex
	ip   string
	httpSrv *http.Server
}

func NewServer(cfg *config.Exit, sup *vpn.Supervisor, publicHost string) *Server {
	if publicHost == "" {
		publicHost = "127.0.0.1"
	}
	return &Server{cfg: cfg, sup: sup, publicHost: publicHost}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/api/v1/status", s.handleStatus)
	mux.HandleFunc("/api/v1/reconnect", s.handleReconnect)
	mux.HandleFunc("/api/v1/9router/export", s.handle9RouterExport)
	mux.HandleFunc("/api/v1/9router/pools", s.handle9RouterExport)

	s.httpSrv = &http.Server{
		Addr:              net.JoinHostPort(s.cfg.Bind, strconv.Itoa(s.cfg.APIPort)),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("control API on http://%s", s.httpSrv.Addr)
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("api error: %v", err)
		}
	}()
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) HTTPProxyAddr() string {
	return net.JoinHostPort(s.cfg.Bind, strconv.Itoa(s.cfg.HTTPPort))
}

func (s *Server) SOCKSAddr() string {
	return net.JoinHostPort(s.cfg.Bind, strconv.Itoa(s.cfg.SOCKSPort))
}

func (s *Server) HTTPProxyURL() string {
	return fmt.Sprintf("http://%s:%d", s.publicHost, s.cfg.HTTPPort)
}

func (s *Server) SOCKSURL() string {
	return fmt.Sprintf("socks5://%s:%d", s.publicHost, s.cfg.SOCKSPort)
}

// RefreshIP probes exit IP through the local HTTP proxy.
func (s *Server) RefreshIP() {
	proxyURL := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(s.cfg.HTTPPort))}
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}
	resp, err := client.Get(s.cfg.IPCheckURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return
	}
	ip := string(body)
	if net.ParseIP(ip) != nil {
		s.mu.Lock()
		s.ip = ip
		s.mu.Unlock()
	}
}

func (s *Server) Status() ExitStatus {
	s.mu.Lock()
	ip := s.ip
	s.mu.Unlock()
	return ExitStatus{
		Exit:       s.cfg.Name,
		Connected:  s.sup.Connected(),
		Server:     s.sup.Server(),
		ExitIP:     ip,
		LastError:  s.sup.LastError(),
		UptimeSec:  int64(s.sup.Uptime().Seconds()),
		HTTPProxy:  s.HTTPProxyURL(),
		SOCKSProxy: s.SOCKSURL(),
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.sup.Connected() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	// API is up even if tunnel is down — useful for registry polling.
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("vpn down"))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Status())
}

func (s *Server) handleReconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.cfg.ConnectTimeoutSec)*time.Second+10*time.Second)
		defer cancel()
		if err := s.sup.Reconnect(ctx); err != nil {
			log.Printf("[%s] reconnect failed: %v", s.cfg.Name, err)
		} else {
			s.RefreshIP()
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"ok": "true", "message": "reconnect started"})
}

func (s *Server) handle9RouterExport(w http.ResponseWriter, r *http.Request) {
	st := s.Status()
	active := st.Connected
	writeJSON(w, http.StatusOK, map[string]any{
		"pools": []NineRouterPool{
			{
				Name:        "shark-" + s.cfg.Name,
				ProxyURL:    s.HTTPProxyURL(),
				Type:        "http",
				NoProxy:     "",
				IsActive:    active,
				StrictProxy: false,
			},
		},
		"status": st,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
