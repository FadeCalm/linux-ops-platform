// Command agent is the per-host execution agent. It must not run as root by
// default. Phase 1 has no authentication: bind to a private management network.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"linux-ops-platform/agent/internal/api"
	"linux-ops-platform/agent/internal/config"
	"linux-ops-platform/agent/internal/exec"
)

// version can be overridden at build time:
//
//	go build -ldflags "-X main.version=1.2.3"
var version = "0.2.0"

func main() {
	cfgPath := flag.String("config", "configs/agent.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	// One startup line so operators can see which address "auto" picked; it is
	// what they must register in the backend (or check with ss -lntp).
	if cfg.ListenAuto {
		log.Printf("listen_addr: auto -> %s", cfg.ListenAddr)
	}

	engine := exec.New(cfg)
	srv := api.New(cfg, engine, version)

	httpSrv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			engine.Cleanup()
		}
	}()

	go func() {
		// Routine starts/stops are intentionally not logged: the agent writes
		// only errors, so journald stays small on managed hosts.
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
