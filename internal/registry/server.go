package registry

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/tommyyzhao/shark-pool/internal/config"
)

// Pool is a 9router-ready proxy pool entry from one exit.
type Pool struct {
	Name        string `json:"name"`
	ProxyURL    string `json:"proxyUrl"`
	Type        string `json:"type"`
	NoProxy     string `json:"noProxy"`
	IsActive    bool   `json:"isActive"`
	StrictProxy bool   `json:"strictProxy"`
}

type ExitSnapshot struct {
	Name       string `json:"name"`
	Connected  bool   `json:"connected"`
	ExitIP     string `json:"exit_ip,omitempty"`
	Server     string `json:"server,omitempty"`
	HTTPProxy  string `json:"http_proxy"`
	SOCKSProxy string `json:"socks_proxy,omitempty"`
	Healthy    bool   `json:"healthy"`
	Error      string `json:"error,omitempty"`
}

type Server struct {
	cfg *config.Registry

	mu     sync.Mutex
	snaps  []ExitSnapshot
	httpSrv *http.Server
}

func New(cfg *config.Registry) *Server {
	return &Server{cfg: cfg}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/v1/pools", s.handlePools)
	mux.HandleFunc("/api/v1/9router/export", s.handlePools)
	mux.HandleFunc("/api/v1/9router/pools", s.handlePools)
	mux.HandleFunc("/api/v1/status", s.handleStatus)

	s.httpSrv = &http.Server{
		Addr:              net.JoinHostPort(s.cfg.Bind, strconv.Itoa(s.cfg.APIPort)),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("registry API on http://%s", s.httpSrv.Addr)

	go s.pollLoop(ctx)

	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("registry api error: %v", err)
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

func (s *Server) pollLoop(ctx context.Context) {
	t := time.NewTicker(time.Duration(s.cfg.PollIntervalSec) * time.Second)
	defer t.Stop()
	s.pollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.pollOnce(ctx)
		}
	}
}

func (s *Server) pollOnce(ctx context.Context) {
	client := &http.Client{Timeout: 5 * time.Second}
	snaps := make([]ExitSnapshot, 0, len(s.cfg.Exits))
	for _, e := range s.cfg.Exits {
		snap := ExitSnapshot{
			Name:       e.Name,
			HTTPProxy:  e.PublicHTTP,
			SOCKSProxy: e.PublicSOCKS,
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.HealthURL+"/api/v1/status", nil)
		if err != nil {
			snap.Error = err.Error()
			snaps = append(snaps, snap)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			snap.Error = err.Error()
			snaps = append(snaps, snap)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
		_ = resp.Body.Close()
		if err != nil {
			snap.Error = err.Error()
			snaps = append(snaps, snap)
			continue
		}
		var st struct {
			Connected bool   `json:"connected"`
			ExitIP    string `json:"exit_ip"`
			Server    string `json:"server"`
		}
		if err := json.Unmarshal(body, &st); err != nil {
			snap.Error = "bad status payload"
			snaps = append(snaps, snap)
			continue
		}
		snap.Connected = st.Connected
		snap.ExitIP = st.ExitIP
		snap.Server = st.Server
		snap.Healthy = st.Connected
		snaps = append(snaps, snap)
	}
	s.mu.Lock()
	s.snaps = snaps
	s.mu.Unlock()
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	snaps := s.snaps
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"exits": snaps,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handlePools(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	snaps := s.snaps
	s.mu.Unlock()

	pools := make([]Pool, 0, len(snaps))
	for _, sn := range snaps {
		pools = append(pools, Pool{
			Name:        "shark-" + sn.Name,
			ProxyURL:    sn.HTTPProxy,
			Type:        "http",
			NoProxy:     "",
			IsActive:    sn.Connected,
			StrictProxy: false,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pools": pools,
		"note":  "Paste pools into 9router → Dashboard → Proxy Pools (type http).",
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
