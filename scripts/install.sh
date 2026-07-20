#!/bin/bash
# ==============================================
#  Room-Agent — 子节点一键安装脚本 v0.0.1
#  在主控面板添加服务器后生成使用:
#  curl -fsSL https://raw.githubusercontent.com/Fyzzp/Room-Agent/main/install.sh | bash -s -- --master https://master.example.com --token YOUR_TOKEN
# ==============================================
set -e

GITHUB_REPO="Fyzzp/Room-Agent"
VERSION="${VERSION:-v0.0.1}"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

MASTER_URL=""
TOKEN=""
XRAY_MODE="external"
LISTEN_PORT="23889"

# 解析参数
while [ $# -gt 0 ]; do
    case $1 in
        --master) MASTER_URL="$2"; shift 2 ;;
        --token)  TOKEN="$2"; shift 2 ;;
        --mode)   XRAY_MODE="$2"; shift 2 ;;
        --port)   LISTEN_PORT="$2"; shift 2 ;;
        *) shift ;;
    esac
done

if [ "$EUID" -ne 0 ]; then echo -e "${RED}请使用 root 权限${NC}"; exit 1; fi

ARCH=$(uname -m)
case $ARCH in
    x86_64)  ARCH_NAME="amd64" ;;
    aarch64|arm64) ARCH_NAME="arm64" ;;
    *) echo -e "${RED}不支持的架构: $ARCH${NC}"; exit 1 ;;
esac

echo -e "${GREEN}=== Room-Agent v${VERSION} 安装 ===${NC}"
echo "主控: $MASTER_URL"
echo "模式: $XRAY_MODE"
echo "端口: $LISTEN_PORT"
echo ""

# === 1. 停止旧服务 ===
echo -e "${YELLOW}[1/5]${NC} 停止旧服务..."
systemctl stop room-agent 2>/dev/null || true
systemctl disable room-agent 2>/dev/null || true

# === 2. 下载 Agent 二进制（只下载二进制，不拉源码）===
echo -e "${YELLOW}[2/5]${NC} 下载 Room-Agent..."
BIN_URL="https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/room-agent-linux-${ARCH_NAME}"

# 镜像链 — GitHub 优先，失败降级
for url in \
    "$BIN_URL" \
    "https://gh-proxy.com/$BIN_URL" \
    "https://mirror.ghproxy.com/$BIN_URL"; do
    echo "  尝试: $url"
    if command -v curl >/dev/null 2>&1; then
        if curl -fsSL --connect-timeout 10 --max-time 180 -o /tmp/room-agent "$url"; then
            break
        fi
    else
        if wget -q --connect-timeout=10 --read-timeout=180 -O /tmp/room-agent "$url"; then
            break
        fi
    fi
done

if [ ! -f /tmp/room-agent ] || [ ! -s /tmp/room-agent ]; then
    echo -e "${RED}下载失败，所有镜像不可达${NC}"
    exit 1
fi

chmod +x /tmp/room-agent
mv /tmp/room-agent /usr/local/bin/room-agent

# === 3. 创建配置 ===
echo -e "${YELLOW}[3/5]${NC} 创建配置..."
mkdir -p /etc/room-agent /var/lib/room-agent

cat > /etc/room-agent/config.yaml << EOF
mode: remote
master_url: "${MASTER_URL}"
token: "${TOKEN}"
connection_mode: auto
xray_mode: ${XRAY_MODE}
xray_config_path: /usr/local/etc/xray/config.json
listen_port: ${LISTEN_PORT}
traffic_interval: 60
speed_interval: 3
heartbeat_interval: 30
EOF

# === 4. 安装 Xray（仅 external 模式）===
if [ "$XRAY_MODE" = "external" ] && ! command -v xray >/dev/null 2>&1; then
    echo -e "${YELLOW}[4/5]${NC} 安装 Xray-core..."
    bash -c "$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install
else
    echo -e "${YELLOW}[4/5]${NC} Xray 已安装或使用 embedded 模式，跳过"
fi

# === 5. 创建 systemd 服务 ===
echo -e "${YELLOW}[5/5]${NC} 创建服务..."
cat > /etc/systemd/system/room-agent.service << EOF
[Unit]
Description=Room Agent - Xray Panel Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/room-agent -c /etc/room-agent/config.yaml
Restart=always
RestartSec=5
WorkingDirectory=/var/lib/room-agent

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable room-agent
systemctl start room-agent

echo ""
echo -e "${GREEN}=== Room-Agent 安装完成！===${NC}"
echo "检查状态: systemctl status room-agent"
echo "查看日志: journalctl -u room-agent -f"
echo ""
echo "管理命令:"
echo "  启动:   systemctl start room-agent"
echo "  停止:   systemctl stop room-agent"
echo "  重启:   systemctl restart room-agent"
echo "  卸载:   systemctl disable room-agent && rm -f /usr/local/bin/room-agent /etc/systemd/system/room-agent.service /etc/room-agent -rf"
