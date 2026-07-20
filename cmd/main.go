// Agent 是运行在远程 VPS 上的 Xray 管理代理
// 职责：
//   - 启动并管理 Xray-core 进程（embedded 或 external 模式）
//   - 通过 gRPC 接收主控的配置管理指令
//   - 定时采集流量/速度数据并上报主控
//   - 支持 WebSocket/HTTP 降级通信
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// Config Agent 启动配置
type Config struct {
	Mode             string `yaml:"mode"`              // master, remote (agent模式固定为remote)
	MasterURL        string `yaml:"master_url"`        // 主控地址
	Token            string `yaml:"token"`             // 认证令牌
	ConnectionMode   string `yaml:"connection_mode"`   // auto, websocket, http, pull
	ListenPort       int    `yaml:"listen_port"`       // gRPC/HTTP 监听端口
	XrayMode         string `yaml:"xray_mode"`         // embedded, external
	XrayConfigPath   string `yaml:"xray_config_path"`  // Xray 配置文件路径
	MasterPublicKey  string `yaml:"master_public_key"` // 主控 Ed25519 公钥(base64)
	TrafficInterval  int    `yaml:"traffic_interval"`   // 流量上报间隔(秒)
	SpeedInterval    int    `yaml:"speed_interval"`     // 速度上报间隔(秒)
	HeartbeatInterval int   `yaml:"heartbeat_interval"` // 心跳间隔(秒)
}

func main() {
	configPath := flag.String("c", "config.yaml", "配置文件路径")
	flag.Parse()

	// 加载配置
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 设置默认值
	if cfg.ListenPort == 0 {
		cfg.ListenPort = 23889
	}
	if cfg.TrafficInterval == 0 {
		cfg.TrafficInterval = 60
	}
	if cfg.SpeedInterval == 0 {
		cfg.SpeedInterval = 3
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 30
	}
	if cfg.ConnectionMode == "" {
		cfg.ConnectionMode = "auto"
	}
	if cfg.XrayMode == "" {
		cfg.XrayMode = "external"
	}

	log.Printf("=== Xray Panel Agent v0.1.0 ===")
	log.Printf("Mode: %s | Xray: %s | Connection: %s", cfg.Mode, cfg.XrayMode, cfg.ConnectionMode)
	log.Printf("Master: %s | Listen: :%d", cfg.MasterURL, cfg.ListenPort)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动 gRPC 服务器（主控调用 Agent 的接口）
	grpcLis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.ListenPort))
	if err != nil {
		log.Fatalf("Failed to listen on gRPC port %d: %v", cfg.ListenPort, err)
	}
	go func() {
		log.Printf("[gRPC] Listening on :%d", cfg.ListenPort)
		// TODO: 注册 AgentService gRPC server
		// agentSvc := grpcsvc.NewAgentService(cfg)
		// agentpb.RegisterAgentServiceServer(grpcServer, agentSvc)
		// grpcServer.Serve(grpcLis)
		_ = grpcLis
	}()

	// 启动 HTTP 健康检查端点
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"version": "0.1.0",
			"time":    time.Now().Unix(),
		})
	})

	go func() {
		log.Printf("[HTTP] Health check on :%d/health", cfg.ListenPort+1)
		if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.ListenPort+1), nil); err != nil {
			log.Printf("[HTTP] Server error: %v", err)
		}
	}()

	// 连接主控
	go func() {
		log.Printf("[Connection] Connecting to master: %s", cfg.MasterURL)
		// TODO: 实现三种连接模式的自动回退
		// connectToMaster(ctx, cfg)
	}()

	// 初始化 Xray
	if cfg.XrayMode == "embedded" {
		log.Printf("[Xray] Starting embedded Xray-core...")
		// TODO: 启动内联 Xray-core
		// xray.StartServer(xrayConfig)
	} else {
		log.Printf("[Xray] Using external Xray at %s", cfg.XrayConfigPath)
		// TODO: 管理外部 Xray 进程
	}

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("Shutting down...")
	cancel()
	_ = ctx
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

	// 环境变量覆盖
	if v := os.Getenv("MMWX_MASTER_URL"); v != "" {
		cfg.MasterURL = v
	}
	if v := os.Getenv("MMWX_MASTER_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("MMWX_CONNECTION_MODE"); v != "" {
		cfg.ConnectionMode = v
	}
	if v := os.Getenv("MMWX_LISTEN_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.ListenPort)
	}

	return &cfg, nil
}
