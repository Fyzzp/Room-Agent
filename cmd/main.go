package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	wsclient "github.com/Fyzzp/Room-Agent/internal/agent/ws"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Mode              string `yaml:"mode"`
	MasterURL         string `yaml:"master_url"`
	Token             string `yaml:"token"`
	ConnectionMode    string `yaml:"connection_mode"`
	ListenPort        int    `yaml:"listen_port"`
	XrayMode          string `yaml:"xray_mode"`
	XrayConfigPath    string `yaml:"xray_config_path"`
	TrafficInterval   int    `yaml:"traffic_interval"`
	SpeedInterval     int    `yaml:"speed_interval"`
	HeartbeatInterval int    `yaml:"heartbeat_interval"`
}

func main() {
	configPath := flag.String("c", "config.yaml", "config path")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.ListenPort == 0 { cfg.ListenPort = 23889 }
	if cfg.HeartbeatInterval == 0 { cfg.HeartbeatInterval = 30 }
	if cfg.ConnectionMode == "" { cfg.ConnectionMode = "websocket" }
	if cfg.XrayMode == "" { cfg.XrayMode = "external" }

	log.Printf("=== Room Agent v0.0.1 ===")
	log.Printf("Master: %s | Mode: %s | Token: %s...", cfg.MasterURL, cfg.XrayMode, cfg.Token[:8])

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// HTTP 健康检查
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok", "version": "0.0.1", "time": time.Now().Unix(),
		})
	})
	go func() {
		addr := fmt.Sprintf(":%d", cfg.ListenPort+1)
		log.Printf("[HTTP] Health on %s", addr)
		http.ListenAndServe(addr, nil)
	}()

	// WebSocket 连接主控
	client := wsclient.NewClient(cfg.MasterURL, cfg.Token)
	go func() {
		for {
			select {
			case <-ctx.Done(): return
			default:
			}
			if err := client.Connect(ctx); err != nil {
				log.Printf("[WS] Connect failed: %v, retry in 5s...", err)
				time.Sleep(5 * time.Second)
				continue
			}
			log.Println("[WS] Connected to master")
			// 启动心跳
			go func() {
				ticker := time.NewTicker(time.Duration(cfg.HeartbeatInterval) * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done(): return
					case <-ticker.C:
						if !client.IsConnected() { return }
						client.Send("heartbeat", map[string]interface{}{
							"boot_time": time.Now().Unix(),
							"local_time": time.Now().Unix(),
						})
					}
				}
			}()
			// 运行消息循环（阻塞直到断开）
			if err := client.Run(ctx); err != nil {
				log.Printf("[WS] Disconnected: %v", err)
			}
			time.Sleep(5 * time.Second)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("Shutting down...")
	cancel()
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if v := os.Getenv("MMWX_MASTER_URL"); v != "" { cfg.MasterURL = v }
	if v := os.Getenv("MMWX_MASTER_TOKEN"); v != "" { cfg.Token = v }
	if v := os.Getenv("MMWX_CONNECTION_MODE"); v != "" { cfg.ConnectionMode = v }
	if v := os.Getenv("MMWX_LISTEN_PORT"); v != "" { fmt.Sscanf(v, "%d", &cfg.ListenPort) }
	return &cfg, nil
}
