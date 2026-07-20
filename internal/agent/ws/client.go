// Package ws 提供 Agent WebSocket 客户端实现
// 当 gRPC 不可用时，Agent 通过 WebSocket 与主控通信（降级方案）
package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Client WebSocket 客户端
type Client struct {
	masterURL  string
	token      string
	conn       *websocket.Conn
	mu         sync.Mutex
	connected  bool
	stopCh     chan struct{}
	handlers   map[string]MessageHandler
	reconnects int
}

// Message WS 消息格式
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// MessageHandler 消息处理函数
type MessageHandler func(payload json.RawMessage) error

// NewClient 创建 WebSocket 客户端
func NewClient(masterURL, token string) *Client {
	return &Client{
		masterURL: masterURL,
		token:     token,
		stopCh:    make(chan struct{}),
		handlers:  make(map[string]MessageHandler),
	}
}

// RegisterHandler 注册消息处理器
func (c *Client) RegisterHandler(msgType string, handler MessageHandler) {
	c.handlers[msgType] = handler
}

// Connect 建立 WebSocket 连接并认证
func (c *Client) Connect(ctx context.Context) error {
	u, err := url.Parse(c.masterURL)
	if err != nil {
		return fmt.Errorf("parse master url: %w", err)
	}

	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = "/api/agent/ws"

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	headers := http.Header{}
	headers.Set("User-Agent", "xray-panel-agent/0.1.0")

	conn, _, err := dialer.DialContext(ctx, u.String(), headers)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	// 发送认证消息
	authPayload, _ := json.Marshal(map[string]string{
		"token": c.token,
	})

	if err := c.send(Message{
		Type:    "auth",
		Payload: authPayload,
	}); err != nil {
		conn.Close()
		return fmt.Errorf("send auth: %w", err)
	}

	// 等待认证结果
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return fmt.Errorf("read auth result: %w", err)
	}

	var result struct {
		Type    string `json:"type"`
		Payload struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		} `json:"payload"`
	}

	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("parse auth result: %w", err)
	}

	if result.Type != "auth_result" || !result.Payload.Success {
		return fmt.Errorf("auth failed: %s", result.Payload.Message)
	}

	c.connected = true
	c.reconnects = 0
	log.Println("[WS] Connected and authenticated")

	return nil
}

// Run 启动消息循环
func (c *Client) Run(ctx context.Context) error {
	defer func() {
		c.mu.Lock()
		c.conn.Close()
		c.connected = false
		c.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.stopCh:
			return nil
		default:
		}

		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			return fmt.Errorf("connection lost")
		}

		conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read message: %w", err)
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[WS] Failed to parse message: %v", err)
			continue
		}

		if handler, ok := c.handlers[msg.Type]; ok {
			if err := handler(msg.Payload); err != nil {
				log.Printf("[WS] Handler error for %s: %v", msg.Type, err)
			}
		}
	}
}

// Send 发送消息
func (c *Client) Send(msgType string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.send(Message{
		Type:    msgType,
		Payload: data,
	})
}

func (c *Client) send(msg Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	return c.conn.WriteJSON(msg)
}

// IsConnected 检查连接状态
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Close 关闭连接
func (c *Client) Close() {
	close(c.stopCh)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
	}
}

// Reconnect 重连（指数退避）
func (c *Client) Reconnect(ctx context.Context, maxBackoff time.Duration) error {
	c.reconnects++
	backoff := time.Duration(c.reconnects) * 5 * time.Second
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	if backoff < 5*time.Second {
		backoff = 5 * time.Second
	}

	log.Printf("[WS] Reconnecting in %v (attempt %d)...", backoff, c.reconnects)

	timer := time.NewTimer(backoff)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}

	return c.Connect(ctx)
}
