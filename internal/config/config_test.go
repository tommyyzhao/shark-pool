package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExitRequiresCreds(t *testing.T) {
	os.Unsetenv("SURFSHARK_USERNAME")
	os.Unsetenv("SURFSHARK_PASSWORD")
	if _, err := LoadExit(); err == nil {
		t.Fatal("expected error without credentials")
	}
}

func TestLoadExitFindsOvpn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "us.ovpn"), []byte("dev tun\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SURFSHARK_USERNAME", "u")
	t.Setenv("SURFSHARK_PASSWORD", "p")
	t.Setenv("VPN_CONFIG_DIR", dir)
	t.Setenv("VPN_CONFIG", "")
	t.Setenv("EXIT_NAME", "us")
	cfg, err := LoadExit()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConfigPath != filepath.Join(dir, "us.ovpn") {
		t.Fatalf("config path = %s", cfg.ConfigPath)
	}
	if cfg.HTTPPort != 8888 || cfg.SOCKSPort != 1080 {
		t.Fatalf("ports http=%d socks=%d", cfg.HTTPPort, cfg.SOCKSPort)
	}
}

func TestLoadRegistry(t *testing.T) {
	t.Setenv("REGISTRY_EXITS", "us|http://us:8000|http://127.0.0.1:8888|socks5://127.0.0.1:1080,uk|http://uk:8000|http://127.0.0.1:8889")
	r, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Exits) != 2 {
		t.Fatalf("exits = %d", len(r.Exits))
	}
	if r.Exits[0].PublicHTTP != "http://127.0.0.1:8888" {
		t.Fatalf("public http = %s", r.Exits[0].PublicHTTP)
	}
	if r.Exits[1].PublicSOCKS != "" {
		t.Fatalf("expected empty socks for uk, got %q", r.Exits[1].PublicSOCKS)
	}
}
