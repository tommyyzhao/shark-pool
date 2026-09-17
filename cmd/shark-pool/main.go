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
	"syscall"
	"time"

	"github.com/tommyyzhao/shark-pool/internal/api"
	"github.com/tommyyzhao/shark-pool/internal/config"
	"github.com/tommyyzhao/shark-pool/internal/proxy"
	"github.com/tommyyzhao/shark-pool/internal/registry"
	"github.com/tommyyzhao/shark-pool/internal/vpn"
)

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
	log.Printf("starting exit server=%s http=:%d socks=:%d api=:%d",
		fileName(cfg.ConfigPath), cfg.HTTPPort, cfg.SOCKSPort, cfg.APIPort)

	httpProxy := proxy.NewHTTP(net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.HTTPPort)))
	socks := proxy.NewSOCKS5(net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.SOCKSPort)))
	if err := httpProxy.Start(); err != nil {
		return err
	}
	if err := socks.Start(); err != nil {
		_ = httpProxy.Close()
		return err
	}

	sup := vpn.NewSupervisor(cfg)
	srv := api.NewServer(cfg, sup, os.Getenv("PUBLIC_HOST"))
	if err := srv.Start(); err != nil {
		return err
	}

	connectCtx, cancelConnect := context.WithTimeout(ctx, time.Duration(cfg.ConnectTimeoutSec)*time.Second+15*time.Second)
	err = sup.Start(connectCtx)
	cancelConnect()
	if err != nil {
		log.Printf("initial connect failed (API stays up): %v", err)
	} else {
		srv.RefreshIP()
	}

	// background health / reconnect
	go func() {
		t := time.NewTicker(time.Duration(cfg.HealthIntervalSec) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if sup.Connected() {
					srv.RefreshIP()
					continue
				}
				if !cfg.AutoReconnect {
					continue
				}
				log.Printf("tunnel down — attempting reconnect")
				cctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ConnectTimeoutSec)*time.Second+10*time.Second)
				if err := sup.Start(cctx); err != nil {
					log.Printf("reconnect failed: %v", err)
				} else {
					srv.RefreshIP()
				}
				cancel()
			}
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = sup.Stop()
	_ = httpProxy.Close()
	_ = socks.Close()
	return nil
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

func fileName(p string) string {
	parts := strings.Split(p, "/")
	return parts[len(parts)-1]
}
