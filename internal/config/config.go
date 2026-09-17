package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Exit holds settings for one VPN exit container.
type Exit struct {
	Name       string
	Username   string
	Password   string
	ConfigDir  string
	ConfigPath string // explicit file; empty = first *.ovpn in ConfigDir
	HTTPPort   int
	SOCKSPort  int
	APIPort    int
	Bind       string
	ConnectTimeoutSec int
	HealthIntervalSec int
	IPCheckURL  string
	KillSwitch  bool
	AutoReconnect bool
}

// Registry holds settings for the pool aggregator.
type Registry struct {
	APIPort int
	Bind    string
	// Exits is name|healthURL|publicHTTP[|publicSOCKS]
	Exits []RegistryExit
	PollIntervalSec int
}

type RegistryExit struct {
	Name       string
	HealthURL  string
	PublicHTTP string
	PublicSOCKS string
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
		ConfigPath:        strings.TrimSpace(os.Getenv("VPN_CONFIG")),
		HTTPPort:          envInt("HTTP_PORT", 8888),
		SOCKSPort:         envInt("SOCKS_PORT", 1080),
		APIPort:           envInt("API_PORT", 8000),
		Bind:              firstNonEmpty(os.Getenv("BIND"), "0.0.0.0"),
		ConnectTimeoutSec: envInt("CONNECT_TIMEOUT_SEC", 75),
		HealthIntervalSec: envInt("HEALTH_INTERVAL_SEC", 30),
		IPCheckURL:        firstNonEmpty(os.Getenv("IP_CHECK_URL"), "https://api.ipify.org"),
		KillSwitch:        envBool("KILL_SWITCH", false),
		AutoReconnect:     envBool("AUTO_RECONNECT", true),
	}
	if cfg.Name == "" {
		cfg.Name = "exit"
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("SURFSHARK_USERNAME and SURFSHARK_PASSWORD are required")
	}
	if cfg.ConfigPath == "" {
		p, err := findConfig(cfg.ConfigDir)
		if err != nil {
			return nil, err
		}
		cfg.ConfigPath = p
	}
	return cfg, nil
}

func findConfig(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read VPN_CONFIG_DIR %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".ovpn") {
			return filepath.Join(dir, name), nil
		}
	}
	return "", fmt.Errorf("no .ovpn files in %s (place Surfshark OpenVPN configs there)", dir)
}

// LoadRegistry reads registry-mode config from the environment.
func LoadRegistry() (*Registry, error) {
	r := &Registry{
		APIPort:           envInt("API_PORT", 8100),
		Bind:              firstNonEmpty(os.Getenv("BIND"), "0.0.0.0"),
		PollIntervalSec:   envInt("POLL_INTERVAL_SEC", 10),
	}
	raw := strings.TrimSpace(os.Getenv("REGISTRY_EXITS"))
	if raw == "" {
		return nil, fmt.Errorf("REGISTRY_EXITS is required in registry mode (name|healthURL|publicHTTP[|publicSOCKS],...)")
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, "|")
		if len(fields) < 3 {
			return nil, fmt.Errorf("invalid REGISTRY_EXITS entry %q", part)
		}
		e := RegistryExit{
			Name:       strings.TrimSpace(fields[0]),
			HealthURL:  strings.TrimSpace(fields[1]),
			PublicHTTP: strings.TrimSpace(fields[2]),
		}
		if len(fields) >= 4 {
			e.PublicSOCKS = strings.TrimSpace(fields[3])
		}
		if e.Name == "" || e.HealthURL == "" || e.PublicHTTP == "" {
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
