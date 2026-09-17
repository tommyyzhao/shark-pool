package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tommyyzhao/shark-pool/internal/api"
	"github.com/tommyyzhao/shark-pool/internal/config"
	"github.com/tommyyzhao/shark-pool/internal/proxy"
	"github.com/tommyyzhao/shark-pool/internal/registry"
	"github.com/tommyyzhao/shark-pool/internal/vpn"
)

type closer interface{ Close() error }

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	mode := "exit"
	if len(os.Args) > 1 {
		mode = strings.ToLower(os.Args[1])
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch mode {
	case "exit", "vpn":
		if err := runExit(ctx); err != nil {
			log.Fatalf("exit mode failed: %v", err)
		}
	case "registry", "pool":
		if err := runRegistry(ctx); err != nil {
			log.Fatalf("registry mode failed: %v", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "usage: %s [exit|registry]\n", os.Args[0])
		os.Exit(2)
	}
}

func runExit(ctx context.Context) error {
	cfg, err := config.LoadExit()
	if err != nil {
		return err
	}
	log.SetPrefix(fmt.Sprintf("[%s] ", cfg.Name))
	log.Printf("starting country pool: %d locations, http base=:%d socks base=:%d api=:%d",
		len(cfg.ConfigPaths), cfg.HTTPPortBase, cfg.SOCKSPortBase, cfg.APIPort)

	var units []*api.Unit
	var proxies []closer

	for i, path := range cfg.ConfigPaths {
		label := config.ServerLabel(path)
		httpPort := cfg.HTTPPortBase + i
		socksPort := cfg.SOCKSPortBase + i
		dev := fmt.Sprintf("tun%d", i)

		httpProxy := proxy.NewHTTPBindDevice(net.JoinHostPort(cfg.Bind, strconv.Itoa(httpPort)), dev)
		if err := httpProxy.Start(); err != nil {
			shutdownAll(proxies)
			return fmt.Errorf("http proxy %s: %w", label, err)
		}
		socks := proxy.NewSOCKS5BindDevice(net.JoinHostPort(cfg.Bind, strconv.Itoa(socksPort)), dev)
		if err := socks.Start(); err != nil {
			_ = httpProxy.Close()
			shutdownAll(proxies)
			return fmt.Errorf("socks %s: %w", label, err)
		}
		proxies = append(proxies, httpProxy, socks)

		unitCfg := *cfg
		unitCfg.ConfigPath = path
		// Unique local UDP port so concurrent OpenVPNs don't mix replies.
		lport := 20000 + i
		sup := vpn.NewSupervisorDev(&unitCfg, dev, lport)

		units = append(units, &api.Unit{
			Label:     label,
			Config:    path,
			HTTPPort:  httpPort,
			SOCKSPort: socksPort,
			Sup:       sup,
		})
		log.Printf("location %s → %s http=:%d socks=:%d", label, dev, httpPort, socksPort)
	}

	srv := api.NewServer(cfg, units, cfg.PublicHost)
	if err := srv.Start(); err != nil {
		shutdownAll(proxies)
		return err
	}

	// Connect with limited concurrency so we don't stampede OpenVPN.
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i, u := range units {
		i, u := i, u
		wg.Add(1)
		go func() {
			defer wg.Done()
			// stagger starts slightly
			time.Sleep(time.Duration(i) * 200 * time.Millisecond)
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.ConnectTimeoutSec)*time.Second+15*time.Second)
			defer cancel()
			if err := u.Sup.Start(cctx); err != nil {
				log.Printf("[%s] connect failed (API stays up): %v", u.Label, err)
			} else {
				srv.RefreshIP(u)
			}
		}()
	}
	wg.Wait()
	log.Printf("initial connect phase done: %d locations", len(units))

	go func() {
		t := time.NewTicker(time.Duration(cfg.HealthIntervalSec) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				for _, u := range units {
					u := u
					if u.Sup.Connected() {
						srv.RefreshIP(u)
						continue
					}
					if !cfg.AutoReconnect {
						continue
					}
					log.Printf("[%s] tunnel down — reconnecting", u.Label)
					cctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ConnectTimeoutSec)*time.Second+10*time.Second)
					if err := u.Sup.Start(cctx); err != nil {
						log.Printf("[%s] reconnect failed: %v", u.Label, err)
					} else {
						srv.RefreshIP(u)
					}
					cancel()
				}
			}
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	for _, u := range units {
		_ = u.Sup.Stop()
	}
	shutdownAll(proxies)
	return nil
}

func shutdownAll(proxies []closer) {
	for _, p := range proxies {
		_ = p.Close()
	}
}

func runRegistry(ctx context.Context) error {
	cfg, err := config.LoadRegistry()
	if err != nil {
		return err
	}
	log.SetPrefix("[registry] ")
	srv := registry.New(cfg)
	if err := srv.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	log.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
