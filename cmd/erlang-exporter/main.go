package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"erlang-monitor/internal/config"
	"erlang-monitor/internal/dingtalk"
	"erlang-monitor/internal/exporter"
	runtimestatus "erlang-monitor/internal/runtime"
	"erlang-monitor/internal/sshprobe"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	configPath := flag.String("config", "config/servers.yml", "server configuration file")
	listenAddress := flag.String("listen", ":20903", "HTTP listen address")
	statusPath := flag.String("status-file", "data/exporter-status.json", "persisted runtime status file")
	dingtalkStatusPath := flag.String("dingtalk-status-file", "data/dingtalk-status.json", "persisted DingTalk adapter status file")
	dingtalkTitlePrefix := flag.String("dingtalk-title-prefix", dingtalk.DefaultTitlePrefix, "DingTalk message title prefix")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.LoadExporter(*configPath)
	if err != nil {
		logger.Error("configuration rejected", "event", "config-invalid", "error", err)
		os.Exit(1)
	}
	if *checkConfig {
		logger.Info("configuration valid", "event", "config-valid", "servers", len(cfg.Servers))
		return
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	metrics := exporter.NewMetrics(registry)
	var dingtalkAdapter *dingtalk.Adapter
	webhookURL, err := dingtalk.ReadSecret("DINGTALK_WEBHOOK_URL", "DINGTALK_WEBHOOK_URL_FILE")
	if err != nil {
		logger.Error("DingTalk webhook secret rejected", "event", "config-invalid", "error", err)
		os.Exit(1)
	}
	if webhookURL != "" {
		signingSecret, secretErr := dingtalk.ReadSecret("DINGTALK_SECRET", "DINGTALK_SECRET_FILE")
		if secretErr != nil {
			logger.Error("DingTalk signing secret rejected", "event", "config-invalid", "error", secretErr)
			os.Exit(1)
		}
		atMobiles, recipientErr := dingtalk.ReadRecipients("DINGTALK_AT_MOBILES", "DINGTALK_AT_MOBILES_FILE")
		if recipientErr != nil {
			logger.Error("DingTalk mobile recipients rejected", "event", "config-invalid", "error", recipientErr)
			os.Exit(1)
		}
		atUserIDs, recipientErr := dingtalk.ReadRecipients("DINGTALK_AT_USER_IDS", "DINGTALK_AT_USER_IDS_FILE")
		if recipientErr != nil {
			logger.Error("DingTalk user recipients rejected", "event", "config-invalid", "error", recipientErr)
			os.Exit(1)
		}
		if len(atMobiles) == 0 && len(atUserIDs) == 0 {
			logger.Warn("DingTalk alerts have no configured @ recipients", "event", "dingtalk-recipients-missing", "action", "configure DINGTALK_AT_MOBILES_FILE or DINGTALK_AT_USER_IDS_FILE")
		}
		dingtalkAdapter, err = dingtalk.NewAdapter(dingtalk.Config{
			WebhookURL: webhookURL, Secret: signingSecret, TitlePrefix: *dingtalkTitlePrefix,
			AtMobiles: atMobiles, AtUserIDs: atUserIDs,
			IgnoredNodes: cfg.AlertFilters.IgnoredNodes,
			Timeout:      10 * time.Second, StatusFile: *dingtalkStatusPath,
		}, dingtalk.NewMetrics(registry), logger)
		if err != nil {
			logger.Error("DingTalk configuration rejected", "event", "config-invalid", "error", err)
			os.Exit(1)
		}
		logger.Info("integrated DingTalk adapter enabled", "event", "dingtalk-enabled")
	} else {
		logger.Warn("integrated DingTalk adapter disabled", "event", "dingtalk-disabled", "reason", "webhook-not-configured")
	}
	status := runtimestatus.NewStore(*statusPath)
	poller := exporter.NewPoller(cfg, sshprobe.NewCollector(), metrics, status, logger)
	reloader, err := config.NewHotReloader(*configPath, config.DefaultReloadInterval, cfg, func(updated config.Exporter) {
		poller.ApplyConfig(updated)
		if dingtalkAdapter != nil {
			dingtalkAdapter.UpdateIgnoredNodes(updated.AlertFilters.IgnoredNodes)
		}
	}, logger)
	if err != nil {
		logger.Error("configuration hot reload initialization failed", "event", "config-invalid", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	poller.Start(ctx)
	reloader.Start(ctx)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", exporter.HealthHandler(status))
	mux.HandleFunc("/status", exporter.StatusHandler(status))
	mux.HandleFunc("/config/status", reloader.StatusHandler())
	mux.HandleFunc("/schedule", poller.ScheduleHandler())
	mux.HandleFunc("/collect", poller.CollectHandler())
	if dingtalkAdapter != nil {
		mux.HandleFunc("/alertmanager", dingtalkAdapter.WebhookHandler())
		mux.HandleFunc("/dingtalk/healthz", dingtalkAdapter.HealthHandler())
	}
	server := &http.Server{Addr: *listenAddress, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}

	go func() {
		logger.Info("exporter listening", "event", "http-listen", "address", *listenAddress)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server failed", "event", "http-server-failed", "error", err)
			cancel()
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	reloader.Wait()
	poller.Wait()
	logger.Info("exporter stopped", "event", "shutdown-complete")
}
