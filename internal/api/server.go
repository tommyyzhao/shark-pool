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
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/tommyyzhao/shark-pool/internal/config"
	"github.com/tommyyzhao/shark-pool/internal/vpn"
)

// ServerStatus is the public status of one location inside a country pool.
type ServerStatus struct {
	ID         string `json:"id"`
	Exit       string `json:"exit"`
	Connected  bool   `json:"connected"`
	Server     string `json:"server"`
	ExitIP     string `json:"exit_ip"`
	LastError  string `json:"last_error,omitempty"`
	UptimeSec  int64  `json:"uptime_sec"`
	HTTPProxy  string `json:"http_proxy"`
	SOCKSProxy string `json:"socks_proxy"`
	HTTPPort   int    `json:"http_port"`
	SOCKSPort  int    `json:"socks_port"`
}

// PoolStatus is the country-container aggregate.
type PoolStatus struct {
	Exit          string         `json:"exit"`
	ServerCount   int            `json:"server_count"`
	ConnectedCount int           `json:"connected_count"`
	Servers       []ServerStatus `json:"servers"`
	UpdatedAt     string         `json:"updated_at"`
}

// NineRouterPool is a paste-ready 9router proxy-pool entry.
type NineRouterPool struct {
	Name        string `json:"name"`
	ProxyURL    string `json:"proxyUrl"`
	Type        string `json:"type"`
	NoProxy     string `json:"noProxy"`
	IsActive    bool   `json:"isActive"`
	StrictProxy bool   `json:"strictProxy"`
	Location    string `json:"location,omitempty"`
	ExitIP      string `json:"exitIp,omitempty"`
}

// Unit is one concurrent tunnel + proxies.
type Unit struct {
	Label     string
	Config    string
	HTTPPort  int
	SOCKSPort int
	Sup       *vpn.Supervisor

	mu sync.Mutex
	ip string
}

// Server is the country control API.
type Server struct {
	cfg        *config.Exit
	units      []*Unit
	publicHost string
	httpSrv    *http.Server
}

func NewServer(cfg *config.Exit, units []*Unit, publicHost string) *Server {
	if publicHost == "" {
		publicHost = cfg.PublicHost
	}
	if publicHost == "" {
		publicHost = "127.0.0.1"
	}
	return &Server{cfg: cfg, units: units, publicHost: publicHost}
}

func (s *Server) Units() []*Unit { return s.units }

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
	log.Printf("control API on http://%s (%d locations)", s.httpSrv.Addr, len(s.units))
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

func (s *Server) httpURL(port int) string {
	return fmt.Sprintf("http://%s:%d", s.publicHost, port)
}

func (s *Server) socksURL(port int) string {
	return fmt.Sprintf("socks5://%s:%d", s.publicHost, port)
}

// RefreshIP probes exit IP through a unit's local HTTP proxy.
func (s *Server) RefreshIP(u *Unit) {
	proxyURL := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(u.HTTPPort))}
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
		u.mu.Lock()
		u.ip = ip
		u.mu.Unlock()
	}
}

func (s *Server) unitStatus(u *Unit) ServerStatus {
	u.mu.Lock()
	ip := u.ip
	u.mu.Unlock()
	return ServerStatus{
		ID:         u.Label,
		Exit:       s.cfg.Name,
		Connected:  u.Sup.Connected(),
		Server:     u.Sup.Server(),
		ExitIP:     ip,
		LastError:  u.Sup.LastError(),
		UptimeSec:  int64(u.Sup.Uptime().Seconds()),
		HTTPProxy:  s.httpURL(u.HTTPPort),
		SOCKSProxy: s.socksURL(u.SOCKSPort),
		HTTPPort:   u.HTTPPort,
		SOCKSPort:  u.SOCKSPort,
	}
}

func (s *Server) Status() PoolStatus {
	servers := make([]ServerStatus, 0, len(s.units))
	connected := 0
	for _, u := range s.units {
		st := s.unitStatus(u)
		if st.Connected {
			connected++
		}
		servers = append(servers, st)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].ID < servers[j].ID })
	return PoolStatus{
		Exit:           s.cfg.Name,
		ServerCount:    len(servers),
		ConnectedCount: connected,
		Servers:        servers,
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	for _, u := range s.units {
		if u.Sup.Connected() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
	}
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
	id := r.URL.Query().Get("id")
	targets := s.units
	if id != "" {
		targets = nil
		for _, u := range s.units {
			if u.Label == id {
				targets = []*Unit{u}
				break
			}
		}
		if targets == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown id"})
			return
		}
	}
	for _, u := range targets {
		u := u
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.cfg.ConnectTimeoutSec)*time.Second+10*time.Second)
			defer cancel()
			if err := u.Sup.Reconnect(ctx); err != nil {
				log.Printf("[%s] reconnect failed: %v", u.Label, err)
			} else {
				s.RefreshIP(u)
			}
		}()
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "message": "reconnect started", "targets": len(targets)})
}

func (s *Server) handle9RouterExport(w http.ResponseWriter, r *http.Request) {
	st := s.Status()
	pools := make([]NineRouterPool, 0, len(st.Servers))
	for _, srv := range st.Servers {
		pools = append(pools, NineRouterPool{
			Name:        "shark-" + srv.ID,
			ProxyURL:    srv.HTTPProxy,
			Type:        "http",
			NoProxy:     "",
			IsActive:    srv.Connected,
			StrictProxy: false,
			Location:    srv.ID,
			ExitIP:      srv.ExitIP,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exit":   s.cfg.Name,
		"pools":  pools,
		"status": st,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
