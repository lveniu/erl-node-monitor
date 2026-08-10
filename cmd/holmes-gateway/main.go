package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	monitorconfig "erlang-monitor/internal/config"
	"erlang-monitor/internal/holmesgateway"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	gatewayConfigPath := flag.String("config", "holmes/gateway.example.yml", "Holmes gateway configuration file")
	serverConfigPath := flag.String("servers", "config/servers.yml", "server inventory configuration file")
	listenAddress := flag.String("listen", "127.0.0.1:20904", "gateway listen address")
	dataDirectory := flag.String("data-dir", "data/holmes", "session and audit data directory")
	checkConfig := flag.Bool("check-config", false, "validate non-secret configuration and exit")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := holmesgateway.LoadConfig(*gatewayConfigPath)
	if err != nil {
		logger.Error("Holmes gateway configuration rejected", "event", "config-invalid", "error", err)
		os.Exit(1)
	}
	servers, err := monitorconfig.LoadExporter(*serverConfigPath)
	if err != nil {
		logger.Error("server inventory rejected", "event", "config-invalid", "error", err)
		os.Exit(1)
	}
	if *checkConfig {
		logger.Info("Holmes gateway non-secret configuration valid", "event", "config-valid", "holmes_version", cfg.HolmesVersion, "models", len(cfg.Models), "servers", len(servers.Servers))
		return
	}

	holmesAPIKey, err := holmesgateway.ReadSecret("HOLMES_API_KEY", "HOLMES_API_KEY_FILE")
	if err != nil {
		logger.Error("Holmes API secret is unavailable", "event", "secret-missing", "error", err)
		os.Exit(1)
	}
	toolToken, err := holmesgateway.ReadSecret("HOLMES_TOOL_API_TOKEN", "HOLMES_TOOL_API_TOKEN_FILE")
	if err != nil {
		logger.Error("gateway internal token is unavailable", "event", "secret-missing", "error", err)
		os.Exit(1)
	}
	store, err := holmesgateway.NewStore(filepath.Join(*dataDirectory, "sessions"), cfg.Limits.SessionRetention.Duration, cfg.Limits.MaxSessions)
	if err != nil {
		logger.Error("session store initialization failed", "event", "store-failed", "error", err)
		os.Exit(1)
	}
	holmes := holmesgateway.NewHTTPHolmesClient(cfg.HolmesURL, holmesAPIKey)
	tools := holmesgateway.NewDiagnosticToolExecutor(servers)
	gateway, err := holmesgateway.NewGateway(cfg, servers, store, holmes, tools, toolToken, logger)
	if err != nil {
		logger.Error("gateway initialization failed", "event", "gateway-failed", "error", err)
		os.Exit(1)
	}
	auditor, err := holmesgateway.NewFileAuditor(filepath.Join(*dataDirectory, "audit", "audit.jsonl"))
	if err != nil {
		logger.Error("audit store initialization failed", "event", "audit-failed", "error", err)
		os.Exit(1)
	}
	gateway.SetAuditor(auditor)
	registry := prometheus.NewRegistry()
	gateway.SetMetrics(holmesgateway.NewMetrics(registry))

	server := &http.Server{
		Addr:              *listenAddress,
		Handler:           gateway.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       90 * time.Second,
	}
	go func() {
		logger.Info("Holmes gateway listening", "event", "gateway-started", "listen", *listenAddress, "holmes_version", cfg.HolmesVersion)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Holmes gateway stopped unexpectedly", "event", "gateway-stopped", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownContext)
}
