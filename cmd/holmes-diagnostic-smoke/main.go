package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	monitorconfig "erlang-monitor/internal/config"
	"erlang-monitor/internal/sshprobe"
)

func main() {
	configPath := flag.String("config", "config/servers.native.yml", "server inventory")
	serverID := flag.String("server", "", "stable server ID from inventory")
	tool := flag.String("tool", "list_erlang_nodes", "get_host_snapshot, list_erlang_nodes, get_node_snapshot, get_node_connections, get_scheduler_hotspots, or get_process_hotspots")
	node := flag.String("node", "", "currently discovered Erlang node")
	metric := flag.String("metric", "reductions", "reductions, memory, or message_queue_len")
	topN := flag.Int("top-n", 10, "bounded Top N, maximum 20")
	windowMS := flag.Int("window-ms", 1000, "bounded sample window, maximum 5000 ms")
	execute := flag.Bool("execute", false, "confirm one real read-only diagnostic call")
	flag.Parse()
	if !*execute {
		fmt.Fprintln(os.Stderr, "refusing to connect without -execute; this command never writes remote state")
		os.Exit(2)
	}
	if *serverID == "" || *topN < 1 || *topN > 20 || *windowMS < 100 || *windowMS > 5000 {
		fmt.Fprintln(os.Stderr, "invalid bounded diagnostic arguments")
		os.Exit(2)
	}
	cfg, err := monitorconfig.LoadExporter(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "server inventory rejected")
		os.Exit(1)
	}
	var server *monitorconfig.Server
	for index := range cfg.Servers {
		if cfg.Servers[index].ID == *serverID && cfg.Servers[index].IsEnabled() {
			server = &cfg.Servers[index]
			break
		}
	}
	if server == nil {
		fmt.Fprintln(os.Stderr, "server ID is not enabled in inventory")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	collector := sshprobe.NewDiagnosticCollector()
	var result sshprobe.DiagnosticResult
	switch *tool {
	case "get_host_snapshot":
		result = collector.HostSnapshot(ctx, *server)
	case "list_erlang_nodes":
		result = collector.ListNodes(ctx, *server)
	case "get_node_snapshot":
		if *node == "" {
			failNode()
		}
		result = collector.NodeSnapshot(ctx, *server, *node)
	case "get_node_connections":
		if *node == "" {
			failNode()
		}
		result = collector.NodeConnections(ctx, *server, *node)
	case "get_scheduler_hotspots":
		if *node == "" {
			failNode()
		}
		result = collector.SchedulerHotspots(ctx, *server, *node, *topN, *windowMS)
	case "get_process_hotspots":
		if *node == "" || (*metric != "reductions" && *metric != "memory" && *metric != "message_queue_len") {
			failNode()
		}
		result = collector.ProcessHotspots(ctx, *server, *node, *metric, *topN, *windowMS)
	default:
		fmt.Fprintln(os.Stderr, "tool is not in the fixed diagnostic whitelist")
		os.Exit(2)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(result)
	if result.Stages.ErrorCode != "" {
		os.Exit(1)
	}
}

func failNode() {
	fmt.Fprintln(os.Stderr, "a valid node and metric are required")
	os.Exit(2)
}
