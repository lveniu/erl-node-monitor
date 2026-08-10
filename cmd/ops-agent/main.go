package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	monitorconfig "erlang-monitor/internal/config"
	"erlang-monitor/internal/opsagent"
)

func main() {
	configPath := flag.String("config", "ops-agent/config.example.yml", "ops agent configuration")
	serverConfigPath := flag.String("servers", "config/servers.native.yml", "server inventory configuration")
	listenAddress := flag.String("listen", "127.0.0.1:20906", "listen address")
	tokenEnv := flag.String("token-env", "OPS_AGENT_TOOL_API_TOKEN", "Grafana proxy token environment variable")
	checkConfig := flag.Bool("check-config", false, "validate configuration and exit")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := opsagent.LoadConfig(*configPath)
	if err != nil {
		logger.Error("ops agent config invalid", "error", err)
		os.Exit(1)
	}
	inventory, err := monitorconfig.LoadExporter(*serverConfigPath)
	if err != nil {
		logger.Error("server inventory invalid", "error", err)
		os.Exit(1)
	}
	skills, err := opsagent.LoadSkills(cfg.SkillsDir)
	if err != nil {
		logger.Error("skills unavailable", "error", err)
		os.Exit(1)
	}
	if *checkConfig {
		logger.Info("ops agent config valid", "skills_dir", cfg.SkillsDir, "skills", len(skills.List()), "servers", len(inventory.Servers))
		return
	}
	apiKey, err := readSecret(cfg.Model.APIKeyEnv)
	if err != nil {
		logger.Error("model API key is unavailable", "env", cfg.Model.APIKeyEnv)
		os.Exit(1)
	}
	token, err := readSecret(*tokenEnv)
	if err != nil {
		logger.Error("Grafana proxy token is unavailable", "env", *tokenEnv)
		os.Exit(1)
	}
	var model opsagent.Model
	if cfg.Model.Protocol == "anthropic" {
		model = opsagent.NewAnthropicModel(cfg.Model, apiKey)
	} else {
		model = opsagent.NewOpenAIModel(cfg.Model, apiKey)
	}
	shell := opsagent.NewShellExecutor(cfg.LocalWorkdir)
	agent, err := opsagent.NewAgent(cfg, inventory, model, skills, shell, logger)
	if err != nil {
		logger.Error("ops agent initialization failed", "error", err)
		os.Exit(1)
	}
	defer agent.Close()
	handler, err := opsagent.NewHandler(agent, token)
	if err != nil {
		logger.Error("ops agent handler failed", "error", err)
		os.Exit(1)
	}
	server := &http.Server{Addr: *listenAddress, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 0, IdleTimeout: 90 * time.Second}
	go func() {
		logger.Info("ops agent listening", "listen", *listenAddress, "skills", len(skills.List()))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("ops agent stopped", "error", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}

func readSecret(envName string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return value, nil
	}
	if path := strings.TrimSpace(os.Getenv(envName + "_FILE")); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if value := strings.TrimSpace(string(data)); value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s or %s_FILE is required", envName, envName)
}
