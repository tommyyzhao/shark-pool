package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Exit holds settings for one country pool container.
// Each .ovpn in ConfigDir becomes one concurrent tunnel + HTTP + SOCKS5.
type Exit struct {
	Name              string
	Username          string
	Password          string
	ConfigDir         string
	ConfigPaths       []string // all selected .ovpn files
	ConfigPath        string   // single tunnel target (set per unit copy)
	HTTPPortBase      int      // server i listens on HTTPPortBase+i
	SOCKSPortBase     int
	APIPort           int
	Bind              string
	PublicHost        string
	ConnectTimeoutSec int
	HealthIntervalSec int
	IPCheckURL        string
	KillSwitch        bool
	AutoReconnect     bool
	MaxServers        int // 0 = all configs
}

// Registry holds settings for the pool aggregator.
type Registry struct {
	APIPort           int
	Bind              string
	Exits             []RegistryExit
	PollIntervalSec   int
}

// RegistryExit points at a country container's control API.
// Proxy URLs come from that container's /api/v1/status payload.
type RegistryExit struct {
	Name      string
	HealthURL string
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// LoadExit reads exit-mode config from the environment.
func LoadExit() (*Exit, error) {
	cfg := &Exit{
		Name:              strings.TrimSpace(os.Getenv("EXIT_NAME")),
		Username:          strings.TrimSpace(os.Getenv("SURFSHARK_USERNAME")),
		Password:          strings.TrimSpace(os.Getenv("SURFSHARK_PASSWORD")),
		ConfigDir:         firstNonEmpty(os.Getenv("VPN_CONFIG_DIR"), "/vpn/configs"),
		HTTPPortBase:      envInt("HTTP_PORT", 8888),
		SOCKSPortBase:     envInt("SOCKS_PORT", 1080),
		APIPort:           envInt("API_PORT", 8000),
		Bind:              firstNonEmpty(os.Getenv("BIND"), "0.0.0.0"),
		PublicHost:        firstNonEmpty(os.Getenv("PUBLIC_HOST"), "127.0.0.1"),
		ConnectTimeoutSec: envInt("CONNECT_TIMEOUT_SEC", 75),
		HealthIntervalSec: envInt("HEALTH_INTERVAL_SEC", 30),
		IPCheckURL:        firstNonEmpty(os.Getenv("IP_CHECK_URL"), "https://api.ipify.org"),
		KillSwitch:        envBool("KILL_SWITCH", false),
		AutoReconnect:     envBool("AUTO_RECONNECT", true),
		MaxServers:        envInt("MAX_SERVERS", 0),
	}
	if cfg.Name == "" {
		cfg.Name = "exit"
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("SURFSHARK_USERNAME and SURFSHARK_PASSWORD are required")
	}

	explicit := strings.TrimSpace(os.Getenv("VPN_CONFIG"))
	if explicit != "" {
		cfg.ConfigPaths = []string{explicit}
	} else {
		paths, err := listConfigs(cfg.ConfigDir)
		if err != nil {
			return nil, err
		}
		if cfg.MaxServers > 0 && len(paths) > cfg.MaxServers {
			paths = paths[:cfg.MaxServers]
		}
		cfg.ConfigPaths = paths
	}
	return cfg, nil
}

func listConfigs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read VPN_CONFIG_DIR %s: %w", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".ovpn") {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no .ovpn files in %s (place Surfshark OpenVPN configs there)", dir)
	}
	sort.Strings(paths)
	return paths, nil
}

// LoadRegistry reads registry-mode config from the environment.
// Format: name|healthURL,name|healthURL
func LoadRegistry() (*Registry, error) {
	r := &Registry{
		APIPort:         envInt("API_PORT", 8100),
		Bind:            firstNonEmpty(os.Getenv("BIND"), "0.0.0.0"),
		PollIntervalSec: envInt("POLL_INTERVAL_SEC", 10),
	}
	raw := strings.TrimSpace(os.Getenv("REGISTRY_EXITS"))
	if raw == "" {
		return nil, fmt.Errorf("REGISTRY_EXITS is required (name|healthURL,...)")
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, "|")
		if len(fields) < 2 {
			return nil, fmt.Errorf("invalid REGISTRY_EXITS entry %q", part)
		}
		e := RegistryExit{
			Name:      strings.TrimSpace(fields[0]),
			HealthURL: strings.TrimSpace(fields[1]),
		}
		if e.Name == "" || e.HealthURL == "" {
			return nil, fmt.Errorf("invalid REGISTRY_EXITS entry %q", part)
		}
		r.Exits = append(r.Exits, e)
	}
	if len(r.Exits) == 0 {
		return nil, fmt.Errorf("REGISTRY_EXITS produced no exits")
	}
	return r, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ServerLabel derives a short city/id label from an .ovpn filename.
func ServerLabel(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	// us-nyc.prod.surfshark.com_udp → us-nyc
	if i := strings.Index(base, ".prod."); i > 0 {
		base = base[:i]
	}
	return base
}
