// Command backend is the Linux ops platform management service. Phase 1 has
// no authentication and must only run on an isolated management network.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"linux-ops-platform/backend/internal/agentclient"
	"linux-ops-platform/backend/internal/api"
	"linux-ops-platform/backend/internal/config"
	"linux-ops-platform/backend/internal/database"
	"linux-ops-platform/backend/internal/logstore"
	"linux-ops-platform/backend/internal/scheduler"
	"linux-ops-platform/backend/internal/ws"

	"github.com/gin-gonic/gin"
)

func main() {
	cfgPath := flag.String("config", "configs/backend.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db, err := database.Open(cfg)
	if err != nil {
		log.Fatalf("database: %v", err)
	}

	hub := ws.NewHub(cfg.WebSocket.ReplayBufferLen, cfg.WebSocket.MaxConnections)
	store := logstore.New(cfg.LogDir, db)
	agentCli := agentclient.New(time.Duration(cfg.Scheduler.AgentRequestTimeoutS) * time.Second)
	sched := scheduler.New(cfg, db, agentCli, store, hub)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	api.NewHandlers(cfg, db, sched, store, hub, agentCli).Register(r)

	// Optionally serve the built frontend from the same process.
	if cfg.FrontendDir != "" {
		serveFrontend(r, cfg.FrontendDir)
	}

	ctx, cancel := context.WithCancel(context.Background())
	sched.Start(ctx)

	// No WriteTimeout: WebSocket connections are intentionally long-lived.
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Printf("backend listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	cancel()
	sched.Stop()
	log.Println("stopped")
}

func serveFrontend(r *gin.Engine, dir string) {
	index := filepath.Join(dir, "index.html")
	if _, err := os.Stat(index); err != nil {
		log.Printf("frontend dir %s has no index.html; static serving disabled", dir)
		return
	}
	if assets := filepath.Join(dir, "assets"); dirExists(assets) {
		r.Static("/assets", assets)
	}
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "not found"})
			return
		}
		c.File(index)
	})
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
