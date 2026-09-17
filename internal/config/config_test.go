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

func TestLoadExitListsAllOvpn(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"us-nyc.prod.surfshark.com_udp.ovpn", "us-lax.prod.surfshark.com_udp.ovpn", "us-chi.prod.surfshark.com_udp.ovpn"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("dev tun\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// non-ovpn ignored
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SURFSHARK_USERNAME", "u")
	t.Setenv("SURFSHARK_PASSWORD", "p")
	t.Setenv("VPN_CONFIG_DIR", dir)
	t.Setenv("VPN_CONFIG", "")
	t.Setenv("EXIT_NAME", "us")
	t.Setenv("MAX_SERVERS", "0")
	t.Setenv("HTTP_PORT", "8888")
	t.Setenv("SOCKS_PORT", "1080")
	cfg, err := LoadExit()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ConfigPaths) != 3 {
		t.Fatalf("want 3 configs, got %d: %v", len(cfg.ConfigPaths), cfg.ConfigPaths)
	}
	if ServerLabel(cfg.ConfigPaths[0]) != "us-chi" {
		t.Fatalf("sorted label[0]=%s", ServerLabel(cfg.ConfigPaths[0]))
	}
}

func TestLoadExitMaxServers(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.ovpn", "b.ovpn", "c.ovpn"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("dev tun\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SURFSHARK_USERNAME", "u")
	t.Setenv("SURFSHARK_PASSWORD", "p")
	t.Setenv("VPN_CONFIG_DIR", dir)
	t.Setenv("VPN_CONFIG", "")
	t.Setenv("MAX_SERVERS", "2")
	cfg, err := LoadExit()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ConfigPaths) != 2 {
		t.Fatalf("want 2, got %d", len(cfg.ConfigPaths))
	}
}

func TestServerLabel(t *testing.T) {
	got := ServerLabel("/vpn/configs/us-nyc.prod.surfshark.com_udp.ovpn")
	if got != "us-nyc" {
		t.Fatalf("got %q", got)
	}
}

func TestLoadRegistry(t *testing.T) {
	t.Setenv("REGISTRY_EXITS", "us|http://us:8000,uk|http://uk:8000")
	r, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Exits) != 2 || r.Exits[0].Name != "us" || r.Exits[1].HealthURL != "http://uk:8000" {
		t.Fatalf("exits=%+v", r.Exits)
	}
}
