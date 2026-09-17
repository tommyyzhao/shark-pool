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

// Pool is a 9router-ready proxy pool entry from one location.
type Pool struct {
	Name        string `json:"name"`
	ProxyURL    string `json:"proxyUrl"`
	Type        string `json:"type"`
	NoProxy     string `json:"noProxy"`
	IsActive    bool   `json:"isActive"`
	StrictProxy bool   `json:"strictProxy"`
	Location    string `json:"location,omitempty"`
	ExitIP      string `json:"exitIp,omitempty"`
	Country     string `json:"country,omitempty"`
}

type LocationSnap struct {
	Country    string `json:"country"`
	ID         string `json:"id"`
	Connected  bool   `json:"connected"`
	ExitIP     string `json:"exit_ip,omitempty"`
	HTTPProxy  string `json:"http_proxy"`
	SOCKSProxy string `json:"socks_proxy,omitempty"`
	Healthy    bool   `json:"healthy"`
}

type Server struct {
	cfg *config.Registry

	mu      sync.Mutex
	snaps   []LocationSnap
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
	client := &http.Client{Timeout: 8 * time.Second}
	var snaps []LocationSnap
	for _, e := range s.cfg.Exits {
		snaps = append(snaps, s.pollExit(ctx, client, e)...)
	}
	s.mu.Lock()
	s.snaps = snaps
	s.mu.Unlock()
}

func (s *Server) pollExit(ctx context.Context, client *http.Client, e config.RegistryExit) []LocationSnap {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.HealthURL+"/api/v1/status", nil)
	if err != nil {
		return []LocationSnap{{Country: e.Name, ID: e.Name, Healthy: false}}
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("poll %s: %v", e.Name, err)
		return []LocationSnap{{Country: e.Name, ID: e.Name, Healthy: false}}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return []LocationSnap{{Country: e.Name, ID: e.Name, Healthy: false}}
	}

	// New multi-server shape: { servers: [...] }
	var multi struct {
		Servers []struct {
			ID         string `json:"id"`
			Connected  bool   `json:"connected"`
			ExitIP     string `json:"exit_ip"`
			HTTPProxy  string `json:"http_proxy"`
			SOCKSProxy string `json:"socks_proxy"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(body, &multi); err == nil && len(multi.Servers) > 0 {
		out := make([]LocationSnap, 0, len(multi.Servers))
		for _, srv := range multi.Servers {
			out = append(out, LocationSnap{
				Country:    e.Name,
				ID:         srv.ID,
				Connected:  srv.Connected,
				ExitIP:     srv.ExitIP,
				HTTPProxy:  srv.HTTPProxy,
				SOCKSProxy: srv.SOCKSProxy,
				Healthy:    srv.Connected,
			})
		}
		return out
	}

	// Legacy single-server shape
	var single struct {
		Connected  bool   `json:"connected"`
		ExitIP     string `json:"exit_ip"`
		HTTPProxy  string `json:"http_proxy"`
		SOCKSProxy string `json:"socks_proxy"`
		Server     string `json:"server"`
	}
	if err := json.Unmarshal(body, &single); err != nil {
		return []LocationSnap{{Country: e.Name, ID: e.Name, Healthy: false}}
	}
	return []LocationSnap{{
		Country:    e.Name,
		ID:         e.Name,
		Connected:  single.Connected,
		ExitIP:     single.ExitIP,
		HTTPProxy:  single.HTTPProxy,
		SOCKSProxy: single.SOCKSProxy,
		Healthy:    single.Connected,
	}}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	snaps := s.snaps
	s.mu.Unlock()
	connected := 0
	for _, sn := range snaps {
		if sn.Connected {
			connected++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"locations":       snaps,
		"location_count":  len(snaps),
		"connected_count": connected,
		"updated_at":      time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handlePools(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	snaps := s.snaps
	s.mu.Unlock()

	pools := make([]Pool, 0, len(snaps))
	for _, sn := range snaps {
		pools = append(pools, Pool{
			Name:        "shark-" + sn.ID,
			ProxyURL:    sn.HTTPProxy,
			Type:        "http",
			NoProxy:     "",
			IsActive:    sn.Connected,
			StrictProxy: false,
			Location:    sn.ID,
			ExitIP:      sn.ExitIP,
			Country:     sn.Country,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pools": pools,
		"count": len(pools),
		"note":  "Import into 9router → Proxy Pools (type http). Enable rotation across ≥2 pools.",
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
