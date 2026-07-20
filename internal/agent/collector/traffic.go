// Package collector 提供 Xray 流量数据的采集功能
// 通过 Xray 的 /debug/vars HTTP 端点或 gRPC StatsService 获取流量统计
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"net/http"
	"time"
)

// TrafficData 表示单个流量统计项
type TrafficData struct {
	Uplink   int64 `json:"uplink"`   // 上行字节数
	Downlink int64 `json:"downlink"` // 下行字节数
}

// XrayStats 完整流量统计
type XrayStats struct {
	Inbound  map[string]TrafficData `json:"inbound"`  // key: tag
	Outbound map[string]TrafficData `json:"outbound"` // key: tag
	User     map[string]TrafficData `json:"user"`     // key: email
}

// Collector Xray 流量采集器
type Collector struct {
	metricsAddr string        // Xray metrics HTTP 地址
	httpClient  *http.Client
	lastStats   *XrayStats // 上一次采集结果，用于计算差值
}

// New 创建新的采集器
func New(metricsAddr string) *Collector {
	if metricsAddr == "" {
		metricsAddr = "127.0.0.1:38889"
	}
	return &Collector{
		metricsAddr: metricsAddr,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// FetchStats 从 Xray /debug/vars 获取流量统计
func (c *Collector) FetchStats(ctx context.Context) (*XrayStats, error) {
	url := fmt.Sprintf("http://%s/debug/vars", c.metricsAddr)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch stats: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return parseStats(body)
}

// parseStats 解析 /debug/vars 返回的 JSON 数据
// 格式参考 Xray stats 命名规范
func parseStats(data []byte) (*XrayStats, error) {
	var raw struct {
		Stats map[string]int64 `json:"stats"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	stats := &XrayStats{
		Inbound:  make(map[string]TrafficData),
		Outbound: make(map[string]TrafficData),
		User:     make(map[string]TrafficData),
	}

	for name, value := range raw.Stats {
		// name 格式: "inbound>>>[tag]>>>traffic>>>uplink"
		// 或 "user>>>[email]>>>traffic>>>downlink"
		parts := splitStatsName(name)
		if len(parts) != 4 {
			continue
		}

		category, key, _, direction := parts[0], parts[1], parts[2], parts[3]

		switch category {
		case "inbound":
			td := stats.Inbound[key]
			if direction == "uplink" {
				td.Uplink = value
			} else {
				td.Downlink = value
			}
			stats.Inbound[key] = td

		case "outbound":
			td := stats.Outbound[key]
			if direction == "uplink" {
				td.Uplink = value
			} else {
				td.Downlink = value
			}
			stats.Outbound[key] = td

		case "user":
			td := stats.User[key]
			if direction == "uplink" {
				td.Uplink = value
			} else {
				td.Downlink = value
			}
			stats.User[key] = td
		}
	}

	return stats, nil
}

// splitStatsName 按 ">>>" 分割 stats 项名称
func splitStatsName(name string) []string {
	var parts []string
	start := 0
	const sep = ">>>"
	for {
		idx := indexOf(name[start:], sep)
		if idx == -1 {
			parts = append(parts, name[start:])
			break
		}
		parts = append(parts, name[start:start+idx])
		start = start + idx + len(sep)
	}
	return parts
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// GetMetricsPortFromConfig 从 Xray 配置文件的 api 段读取 metrics 端口
func GetMetricsPortFromConfig(configPath string) (string, error) {
	// 简化实现：默认端口
	// TODO: 解析 Xray 配置文件中的 api.listen 字段
	return "127.0.0.1:38889", nil
}

// SpeedCollector 系统级网速采集（通过 /proc/net/dev）
type SpeedCollector struct {
	lastRxBytes    int64
	lastTxBytes    int64
	lastSampleTime time.Time
}

// CollectSpeed 计算当前上传/下载速度 (bytes/s)
func (sc *SpeedCollector) CollectSpeed() (upload, download int64) {
	rxBytes, txBytes := readNetDev()

	now := time.Now()

	if !sc.lastSampleTime.IsZero() && sc.lastRxBytes > 0 {
		elapsed := now.Sub(sc.lastSampleTime).Seconds()
		if elapsed > 0 {
			upload = int64(float64(txBytes-sc.lastTxBytes) / elapsed)
			download = int64(float64(rxBytes-sc.lastRxBytes) / elapsed)
			if upload < 0 {
				upload = 0
			}
			if download < 0 {
				download = 0
			}
		}
	}

	sc.lastRxBytes = rxBytes
	sc.lastTxBytes = txBytes
	sc.lastSampleTime = now

	return
}

// readNetDev 读取 /proc/net/dev 获取网络接口累计字节数
func readNetDev() (rxTotal, txTotal int64) {
	data, err := readFile("/proc/net/dev")
	if err != nil {
		log.Printf("[Collector] Failed to read /proc/net/dev: %v", err)
		return 0, 0
	}

	lines := splitLines(string(data))
	for _, line := range lines {
		line = trimSpace(line)
		// 跳过标题行和 lo 接口
		if len(line) == 0 || startsWith(line, "Inter") || startsWith(line, "face") || startsWith(line, "lo:") {
			continue
		}

		// 格式: "eth0: rx_bytes rx_packets ... tx_bytes tx_packets ..."
		colonIdx := indexOfByte(line, ':')
		if colonIdx == -1 {
			continue
		}

		fields := splitFields(line[colonIdx+1:])
		if len(fields) < 10 {
			continue
		}

		rx := parseInt64(fields[0])
		tx := parseInt64(fields[8])
		rxTotal += rx
		txTotal += tx
	}

	return rxTotal, txTotal
}

// 简化的字符串工具函数（避免额外依赖）
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func indexOfByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func splitFields(s string) []string {
	var fields []string
	inField := false
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			if inField {
				fields = append(fields, s[start:i])
				inField = false
			}
		} else {
			if !inField {
				start = i
				inField = true
			}
		}
	}
	if inField {
		fields = append(fields, s[start:])
	}
	return fields
}

func parseInt64(s string) int64 {
	var n int64
	for i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		n = n*10 + int64(s[i]-'0')
	}
	return n
}
