package vpn

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tommyyzhao/shark-pool/internal/config"
)

// Supervisor runs OpenVPN as a child process and tracks connection state.
type Supervisor struct {
	cfg     *config.Exit
	devName string // e.g. tun0
	lport   int    // unique local UDP port; 0 = default

	mu        sync.Mutex
	cmd       *exec.Cmd
	authFile  string
	cfgFile   string
	connected bool
	startedAt time.Time
	lastErr   string
	server    string
	wantRun   bool
	done      chan struct{}
}

func NewSupervisor(cfg *config.Exit) *Supervisor {
	return &Supervisor{cfg: cfg, server: filepath.Base(cfg.ConfigPath), devName: "tun"}
}

func NewSupervisorDev(cfg *config.Exit, devName string, lport int) *Supervisor {
	if devName == "" {
		devName = "tun"
	}
	return &Supervisor{cfg: cfg, server: filepath.Base(cfg.ConfigPath), devName: devName, lport: lport}
}

func (s *Supervisor) DevName() string { return s.devName }

func (s *Supervisor) Server() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.server
}

func (s *Supervisor) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

func (s *Supervisor) LastError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *Supervisor) Uptime() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected || s.startedAt.IsZero() {
		return 0
	}
	return time.Since(s.startedAt)
}

// Start launches OpenVPN and waits until the tunnel looks up (or timeout).
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.cmd != nil && s.cmd.Process != nil {
		s.mu.Unlock()
		return fmt.Errorf("already running")
	}
	s.wantRun = true
	s.mu.Unlock()

	if err := s.writeAuth(); err != nil {
		return err
	}

	cfgPath := s.cfg.ConfigPath
	if s.lport > 0 {
		patched, err := s.writePatchedConfig()
		if err != nil {
			return err
		}
		cfgPath = patched
	}

	cmd := exec.Command("openvpn",
		"--config", cfgPath,
		"--auth-user-pass", s.authFile,
		"--auth-nocache",
		"--client",
		"--pull",
		"--dev", s.devName,
		"--script-security", "2",
	)
	if s.lport > 0 {
		cmd.Args = append(cmd.Args, "--lport", strconv.Itoa(s.lport))
	}
	cmd.Stdout = log.Writer()
	cmd.Stderr = log.Writer()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		s.setErr(err.Error())
		return fmt.Errorf("start openvpn: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.done = make(chan struct{})
	s.connected = false
	s.mu.Unlock()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	go s.reap(waitCh)

	deadline := time.Now().Add(time.Duration(s.cfg.ConnectTimeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		if s.tunnelUp() {
			s.mu.Lock()
			s.connected = true
			s.startedAt = time.Now()
			s.lastErr = ""
			s.mu.Unlock()
			log.Printf("[%s] openvpn connected (%s)", s.cfg.Name, s.server)
			return nil
		}
		select {
		case err := <-waitCh:
			msg := fmt.Sprintf("openvpn exited during connect: %v", err)
			s.setErr(msg)
			s.clearProc()
			return fmt.Errorf("%s", msg)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	_ = s.Stop()
	msg := fmt.Sprintf("connect timeout after %ds", s.cfg.ConnectTimeoutSec)
	s.setErr(msg)
	return fmt.Errorf("%s", msg)
}

func (s *Supervisor) reap(waitCh <-chan error) {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return
	}
	err := <-waitCh
	s.mu.Lock()
	s.connected = false
	if s.wantRun && err != nil {
		s.lastErr = fmt.Sprintf("openvpn exited: %v", err)
		log.Printf("[%s] %s", s.cfg.Name, s.lastErr)
	} else if s.wantRun {
		s.lastErr = "openvpn exited unexpectedly"
		log.Printf("[%s] openvpn exited unexpectedly", s.cfg.Name)
	}
	s.cmd = nil
	close(done)
	s.mu.Unlock()
}

// Stop terminates OpenVPN.
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	s.wantRun = false
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-func() chan struct{} {
		s.mu.Lock()
		d := s.done
		s.mu.Unlock()
		if d == nil {
			ch := make(chan struct{})
			close(ch)
			return ch
		}
		return d
	}():
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
	}
	s.mu.Lock()
	s.connected = false
	s.cmd = nil
	s.mu.Unlock()
	return nil
}

// Reconnect stops then starts again.
func (s *Supervisor) Reconnect(ctx context.Context) error {
	_ = s.Stop()
	time.Sleep(500 * time.Millisecond)
	return s.Start(ctx)
}

func (s *Supervisor) clearProc() {
	s.mu.Lock()
	s.cmd = nil
	s.connected = false
	s.mu.Unlock()
}

func (s *Supervisor) setErr(msg string) {
	s.mu.Lock()
	s.lastErr = msg
	s.mu.Unlock()
}

func (s *Supervisor) writeAuth() error {
	f, err := os.CreateTemp("", "shark-auth-*")
	if err != nil {
		return err
	}
	content := s.cfg.Username + "\n" + s.cfg.Password + "\n"
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := os.Chmod(f.Name(), 0o600); err != nil {
		os.Remove(f.Name())
		return err
	}
	s.mu.Lock()
	if s.authFile != "" {
		_ = os.Remove(s.authFile)
	}
	s.authFile = f.Name()
	s.mu.Unlock()
	return nil
}

// writePatchedConfig copies the .ovpn without `nobind` so --lport can bind a unique UDP port.
func (s *Supervisor) writePatchedConfig() (string, error) {
	raw, err := os.ReadFile(s.cfg.ConfigPath)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "nobind" || strings.HasPrefix(trim, "nobind ") || strings.HasPrefix(trim, "nobind\t") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	f, err := os.CreateTemp("", "shark-cfg-*.ovpn")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	s.mu.Lock()
	if s.cfgFile != "" {
		_ = os.Remove(s.cfgFile)
	}
	s.cfgFile = f.Name()
	s.mu.Unlock()
	return f.Name(), nil
}

func (s *Supervisor) tunnelUp() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	want := s.devName
	for _, iface := range ifaces {
		if want != "tun" && want != "" && iface.Name != want {
			continue
		}
		if want == "tun" || want == "" {
			if !strings.HasPrefix(iface.Name, "tun") && !strings.HasPrefix(iface.Name, "wg") {
				continue
			}
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return true
			}
		}
	}
	return false
}
