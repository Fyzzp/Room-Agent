// Package grpc 提供 Agent gRPC 服务端实现
// 主控通过 gRPC 调用这些接口管理远程 Xray
package grpc

import (
	"context"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AgentServer 实现 AgentService gRPC 接口
type AgentServer struct {
	// pb.UnimplementedAgentServiceServer // TODO: 等 proto 生成后取消注释
	configPath string // Xray 配置文件路径
	xrayRunning bool
}

// NewAgentServer 创建 Agent gRPC 服务
func NewAgentServer(configPath string) *AgentServer {
	return &AgentServer{
		configPath: configPath,
	}
}

// HealthCheck 健康检查实现
func (s *AgentServer) HealthCheck(ctx context.Context, req interface{}) (interface{}, error) {
	log.Println("[gRPC] HealthCheck called")
	return map[string]interface{}{
		"ok":       true,
		"version":  "0.1.0",
		"xray_running": s.xrayRunning,
	}, nil
}

// AddInbound 添加入站
func (s *AgentServer) AddInbound(ctx context.Context, req interface{}) (interface{}, error) {
	log.Println("[gRPC] AddInbound called")
	// TODO: 通过 HandlerService gRPC 调用 Xray API
	// 1. 解析请求中的 InboundConfig
	// 2. 构造 core.InboundHandlerConfig
	// 3. 调用 client.AddInbound(ctx, &command.AddInboundRequest{...})
	return map[string]interface{}{
		"success": true,
	}, nil
}

// RemoveInbound 删除入站
func (s *AgentServer) RemoveInbound(ctx context.Context, req interface{}) (interface{}, error) {
	// TODO: 调用 client.RemoveInbound(ctx, &command.RemoveInboundRequest{Tag: tag})
	return map[string]interface{}{
		"success": true,
	}, nil
}

// AddOutbound 添加出站
func (s *AgentServer) AddOutbound(ctx context.Context, req interface{}) (interface{}, error) {
	return map[string]interface{}{
		"success": true,
	}, nil
}

// GetTrafficStats 获取流量统计
func (s *AgentServer) GetTrafficStats(ctx context.Context, req interface{}) (interface{}, error) {
	// TODO: 调用 StatsService.QueryStats
	return nil, status.Error(codes.Unimplemented, "TODO: traffic stats")
}

// GetSystemInfo 获取系统信息
func (s *AgentServer) GetSystemInfo(ctx context.Context, req interface{}) (interface{}, error) {
	return map[string]interface{}{
		"os":      "linux",
		"version": "0.1.0",
	}, nil
}
