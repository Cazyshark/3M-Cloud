#!/bin/bash
set -e

echo "=== Multi-Ops Build Script ==="

# Build master
echo "[1/3] Building master server..."
go build -o bin/master ./cmd/master/

# Build gateway
echo "[2/3] Building gateway server..."
go build -o bin/gateway ./cmd/gateway/

# Build agent
echo "[3/3] Building agent client..."
CGO_ENABLED=0 go build -o bin/agent ./cmd/agent/

echo ""
echo "Build complete! Binaries in ./bin/"
echo "  master  - 主控服务器 (看板 + API)"
echo "  gateway - 网关跳板服务器"
echo "  agent   - 被管理机器客户端"
