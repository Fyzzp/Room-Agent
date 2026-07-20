// Package xray 管理 Xray-core 进程和配置
package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
)

// Manager 管理 Xray-core 的生命周期
type Manager struct {
	mu         sync.Mutex
	mode       string     // "embedded" 或 "external"
	configPath string     // 外部模式下的配置文件路径
	running    bool
	cmd        *exec.Cmd  // 外部模式下的进程
	cancel     context.CancelFunc
}

// NewManager 创建 Xray 管理器
func NewManager(mode, configPath string) *Manager {
	return &Manager{
		mode:       mode,
		configPath: configPath,
	}
}

// IsRunning 检查 Xray 是否在运行
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// Start 启动 Xray
func (m *Manager) Start(ctx context.Context, config interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("xray is already running")
	}

	switch m.mode {
	case "embedded":
		return m.startEmbedded(ctx, config)
	case "external":
		return m.startExternal(ctx, config)
	default:
		return fmt.Errorf("unknown xray mode: %s", m.mode)
	}
}

// Stop 停止 Xray
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	switch m.mode {
	case "embedded":
		return m.stopEmbedded()
	case "external":
		return m.stopExternal()
	default:
		return nil
	}
}

// Restart 重启 Xray（用于配置热更新）
func (m *Manager) Restart(ctx context.Context, config interface{}) error {
	if err := m.Stop(); err != nil {
		log.Printf("[Xray] Stop before restart: %v", err)
	}
	return m.Start(ctx, config)
}

// SaveConfig 将 Xray 配置写入文件（外部模式）
func (m *Manager) SaveConfig(config interface{}) error {
	if m.mode != "external" {
		return nil
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(m.configPath, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	log.Printf("[Xray] Config written to %s", m.configPath)
	return nil
}

func (m *Manager) startEmbedded(ctx context.Context, config interface{}) error {
	log.Println("[Xray] Starting embedded Xray-core...")
	// TODO: 内联 Xray-core 启动
	// import "github.com/xtls/xray-core/core"
	// server, err := core.New(config)
	// server.Start()
	m.running = true
	_ = ctx
	_ = config
	return nil
}

func (m *Manager) stopEmbedded() error {
	log.Println("[Xray] Stopping embedded Xray-core...")
	m.running = false
	return nil
}

func (m *Manager) startExternal(ctx context.Context, config interface{}) error {
	// 先保存配置
	if err := m.SaveConfig(config); err != nil {
		return err
	}

	// 检查 Xray 二进制是否存在
	xrayPath := "/usr/local/bin/xray"
	if _, err := os.Stat(xrayPath); os.IsNotExist(err) {
		xrayPath = "/usr/bin/xray"
	}
	if _, err := os.Stat(xrayPath); os.IsNotExist(err) {
		return fmt.Errorf("xray binary not found")
	}

	// 测试配置有效性
	testCmd := exec.CommandContext(ctx, xrayPath, "test", "-config", m.configPath)
	if output, err := testCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("xray config test failed: %w\n%s", err, string(output))
	}

	// 启动 Xray 进程
	ctx2, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	cmd := exec.CommandContext(ctx2, xrayPath, "run", "-config", m.configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start xray: %w", err)
	}

	m.cmd = cmd
	m.running = true

	log.Printf("[Xray] External Xray started (PID: %d)", cmd.Process.Pid)

	// 后台监控 Xray 进程退出
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("[Xray] Process exited: %v", err)
		}
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
	}()

	return nil
}

func (m *Manager) stopExternal() error {
	if m.cancel != nil {
		m.cancel()
	}
	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Signal(os.Interrupt)
	}
	m.running = false
	log.Println("[Xray] External Xray stopped")
	return nil
}
