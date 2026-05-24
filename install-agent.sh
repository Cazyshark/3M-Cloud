#!/bin/bash
# Multi-Ops Agent 安装脚本
# 在被管理的 Ubuntu 机器上运行:
#   curl -sSL http://MASTER_IP:8080/install-agent.sh | bash -s -- --gateway ws://GATEWAY_IP:8081/connect --token YOUR_TOKEN

set -e

GATEWAY_URL=""
AGENT_TOKEN=""
AGENT_ID=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --gateway) GATEWAY_URL="$2"; shift 2 ;;
        --token) AGENT_TOKEN="$2"; shift 2 ;;
        --id) AGENT_ID="$2"; shift 2 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

if [ -z "$GATEWAY_URL" ]; then
    echo "Usage: $0 --gateway ws://GATEWAY_IP:8081/connect [--token TOKEN] [--id AGENT_ID]"
    exit 1
fi

echo "=== Multi-Ops Agent Installer ==="
echo "Gateway: $GATEWAY_URL"

# Install dependencies
echo "[1/4] Installing dependencies..."
apt-get update -qq
apt-get install -y -qq curl lsb-release

# Download agent binary
echo "[2/4] Downloading agent binary..."
mkdir -p /opt/multi-ops
# In production, download from your release server
# curl -sSL http://RELEASE_SERVER/agent -o /opt/multi-ops/agent
echo "Note: Please copy the agent binary to /opt/multi-ops/agent"

# Create config
echo "[3/4] Creating configuration..."
cat > /opt/multi-ops/agent.env <<EOF
GATEWAY_URL=${GATEWAY_URL}
AGENT_TOKEN=${AGENT_TOKEN}
AGENT_ID=${AGENT_ID:-$(hostname)}
EOF

# Create systemd service
echo "[4/4] Installing systemd service..."
cat > /etc/systemd/system/multi-ops-agent.service <<EOF
[Unit]
Description=Multi-Ops Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/opt/multi-ops/agent.env
ExecStart=/opt/multi-ops/agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable multi-ops-agent
systemctl start multi-ops-agent

echo ""
echo "=== Agent installed successfully! ==="
echo "Status: systemctl status multi-ops-agent"
echo "Logs:   journalctl -u multi-ops-agent -f"
