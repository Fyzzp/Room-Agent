# Room-Agent — Xray 子节点代理

Room-Agent 是 [Room](https://github.com/Fyzzp/Room) 主控面板的子节点代理程序，运行在远程 VPS 上，负责：

- 管理与运行 Xray-core（embedded 或 external 模式）
- 通过 gRPC/WebSocket/HTTP 与主控通信
- 定时上报流量/速度数据
- 接收并应用主控下发的配置更新

## 一键安装

```bash
curl -fsSL https://raw.githubusercontent.com/Fyzzp/Room-Agent/main/scripts/install.sh | bash -s -- \
    --master https://your-master.example.com \
    --token YOUR_SERVER_TOKEN
```

## 架构

```
主控(Room) ←→ Agent(Room-Agent) ──管理── Xray-core
     │              │
     │ gRPC/WS/HTTP │ 采集流量、应用配置
     │ (加密+回退)  │
```

## 连接模式

| 模式 | 说明 |
|---|---|
| **auto**（推荐） | 自动回退：WebSocket → HTTP → Pull |
| **websocket** | 全双工双向通信 |
| **http** | 定时 POST 上报 |
| **pull** | 主控主动拉取 |

## 配置

```yaml
mode: remote
master_url: "https://master.example.com"
token: "your-server-token"
connection_mode: auto
xray_mode: external  # 或 embedded
xray_config_path: /usr/local/etc/xray/config.json
listen_port: 23889
```

## 开发

```bash
go build -o room-agent ./cmd
./room-agent -c config.yaml
```

## 许可证

MIT License
