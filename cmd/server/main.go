package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"frontendandbackend1317/internal/config"
	"frontendandbackend1317/internal/httpserver"
	"frontendandbackend1317/internal/push"
	"frontendandbackend1317/internal/realtime"
	"frontendandbackend1317/internal/reminders"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	workingDir, err := os.Getwd()
	if err != nil {
		logger.Error("failed to determine working directory", "error", err)
		return 1
	}

	cfg := config.Load().WithBaseDir(workingDir)
	flag.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	flag.StringVar(&cfg.WebDir, "web-dir", cfg.WebDir, "directory with frontend assets")
	flag.StringVar(&cfg.CertFile, "cert-file", cfg.CertFile, "TLS certificate path")
	flag.StringVar(&cfg.KeyFile, "key-file", cfg.KeyFile, "TLS private key path")
	flag.BoolVar(&cfg.AllowHTTP, "allow-http", cfg.AllowHTTP, "allow HTTP fallback when certificates are missing")
	flag.StringVar(&cfg.VAPIDSubject, "vapid-subject", cfg.VAPIDSubject, "VAPID subscriber contact, for example mailto:student@example.com")
	flag.Parse()
	cfg = cfg.WithBaseDir(workingDir)

	pushService, err := push.NewService(cfg, logger)
	if err != nil {
		logger.Error("failed to initialize push service", "error", err)
		return 1
	}

	reminderManager := reminders.NewManager(logger, pushService)
	defer reminderManager.Shutdown()

	var hub *realtime.Hub
	hub = realtime.NewHub(logger, func(task realtime.TaskPayload) {
		hub.Broadcast(realtime.Envelope{
			Type:    "taskAdded",
			Payload: task,
		})

		if err := pushService.NotifyTask(task); err != nil {
			logger.Error("failed to send push notifications", "error", err)
		}
	})
	defer hub.Shutdown()

	handler, err := httpserver.NewHandler(cfg, logger, hub, pushService, reminderManager)
	if err != nil {
		logger.Error("failed to build HTTP handler", "error", err)
		return 1
	}

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		logger.Info("shutting down server")
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
		hub.Shutdown()
	}()

	useTLS, reason := tlsReady(cfg.CertFile, cfg.KeyFile)
	if !useTLS && !cfg.AllowHTTP {
		logger.Error("TLS certificate files are required for the default secure launch mode",
			"certFile", cfg.CertFile,
			"keyFile", cfg.KeyFile,
			"hint", "Create certificates with mkcert or re-run with -allow-http for a limited fallback mode.",
			"details", reason,
		)
		return 1
	}

	if useTLS {
		logger.Info("starting HTTPS server",
			"url", displayURL(cfg.Addr, true),
			"webDir", cfg.WebDir,
			"certFile", cfg.CertFile,
			"keyFile", cfg.KeyFile,
			"vapidPublicKeyFile", filepath.Base(cfg.VAPIDPublicKeyPath),
		)

		if err := server.ListenAndServeTLS(cfg.CertFile, cfg.KeyFile); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTPS server failed", "error", err)
			return 1
		}
		return 0
	}

	logger.Warn("starting HTTP fallback mode; final PWA/push checks should be done over HTTPS",
		"url", displayURL(cfg.Addr, false),
		"reason", reason,
	)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("HTTP server failed", "error", err)
		return 1
	}

	return 0
}

func tlsReady(certFile, keyFile string) (bool, string) {
	if _, err := os.Stat(certFile); err != nil {
		return false, fmt.Sprintf("certificate not found: %v", err)
	}
	if _, err := os.Stat(keyFile); err != nil {
		return false, fmt.Sprintf("private key not found: %v", err)
	}
	return true, "certificates found"
}

func displayURL(addr string, secure bool) string {
	scheme := "http"
	if secure {
		scheme = "https"
	}

	if addr == "" {
		return scheme + "://localhost:3000"
	}
	if addr[0] == ':' {
		return scheme + "://localhost" + addr
	}
	return scheme + "://" + addr
}
