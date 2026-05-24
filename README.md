# Multi-Ops

Multi-Ops 是一个多机器远程运维平台，采用 Go 编写，支持通过 Web 看板管理、监控和在远程机器上执行命令。使用 WebSocket 实时通信的三层架构。

## 架构

```
  Browser (Dashboard)
         |
    HTTP + WebSocket
         |
   +-----------+
   |  Master   |  内网 :8080 -- 控制面，Web 看板、REST API、JWT 认证、审计日志
   +-----------+
         ↑
         │  Gateway 主动连接 Master (WebSocket 出站)
         │  Master 无需公网 IP
   +-----------+
   | Gateway   |  公网 :8081 -- 代理层，Token 认证，双向消息路由
   +-----------+
         ↑
         │  Agent 主动连接 Gateway (WebSocket 出站)
         │
  +------+------+
  |             |
Agent A1    Agent A2   ...  -- 部署在目标机器上的轻量级客户端
  |             |
(PTY)       (Shell)
```

### 网络拓扑

- **Master** 部署在**内网**，无需公网 IP。Gateway 通过 WebSocket 主动连接 Master。
- **Gateway** 部署在**公网**（云服务器/DMZ），暴露 8081 端口供 Agent 连接。
- **Agent** 部署在各目标机器上，通过 WebSocket 主动连接公网 Gateway。

> 核心设计：所有连接都是**出站**的（Gateway→Master，Agent→Gateway），Master 和 Agent 都不需要公网 IP。

### 组件

- **Master** -- 控制面，提供 Web 看板 (HTTP + WebSocket)、REST API、JWT 认证 + TOTP 双因素、机器清单、脚本执行历史、文件管理、审计日志
- **Gateway** -- 中间代理层，通过 Token 认证 Agent，双向转发 Master 与 Agent 之间的消息。支持网络隔离，Agent 只需能访问 Gateway
- **Agent** -- 部署在每台目标机器上的轻量客户端，定期上报系统信息和实时指标，执行 Shell 脚本，提供交互式 PTY 终端，处理文件上传下载

## 功能

- **机器看板** -- 实时展示所有被管机器的 hostname、OS、CPU/内存/磁盘使用率、网络流量、负载、TCP 连接数，每 5 秒刷新
- **远程终端** -- 通过浏览器进行交互式 PTY Shell 访问，支持动态窗口缩放
- **批量脚本执行** -- 在一台、多台或全部在线机器上同时执行 Shell 脚本，保存执行历史
- **文件管理** -- 向远程 Agent 上传文件、从 Agent 下载文件
- **机器标签与分组** -- 按标签和分组组织机器，支持过滤操作
- **脚本模板** -- 内置常用任务模板（系统信息、磁盘分析、网络检查、进程列表、安全检查、Docker 状态、系统更新）
- **用户认证** -- JWT 认证 + 可选 TOTP (2FA) 双因素验证
- **角色访问控制** -- admin 和 viewer 两种角色，viewer 只能查看不能执行操作
- **审计日志** -- JSONL 格式的审计记录，追踪所有 API 请求
- **安全防护** -- 每 IP 速率限制、登录接口严格限流、IP 白名单、安全响应头、CSP 策略

## 快速开始

### 编译

```bash
make build
# 或
./build.sh
```

产出三个二进制：

| 文件 | 说明 |
|---|---|
| `bin/master` | 主控服务器 |
| `bin/gateway` | 网关代理服务器 |
| `bin/agent` | 被管机器客户端 |

### 启动 Master

Master 是控制面，必须显式设置 `JWT_SECRET` 和 `ADMIN_PASSWORD`，否则拒绝启动：

```bash
JWT_SECRET=$(openssl rand -hex 32) \
ADMIN_PASSWORD=$(openssl rand -base64 24) \
AUDIT_DIR=/var/log/multi-ops \
IP_WHITELIST=10.0.0.1,10.0.0.2 \
./bin/master
```

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `MASTER_LISTEN` | `:8080` | HTTP 监听地址 |
| `JWT_SECRET` | **必须设置** | JWT 签名密钥，使用强随机字符串 |
| `ADMIN_PASSWORD` | **必须设置** | 默认 admin 用户的密码 |
| `AUDIT_DIR` | `/tmp/multi-ops-audit` | 审计日志目录（权限 0700） |
| `IP_WHITELIST` | *(空)* | 允许的 IP 列表，逗号分隔（空 = 不限） |

启动后访问 `http://localhost:8080`，使用 admin / `$ADMIN_PASSWORD` 登录。

### 启动 Gateway

Gateway 未配置 `AGENT_TOKENS` 或 `MASTER_SECRET` 时会自动生成安全的随机值并打印到日志，也可以手动指定：

```bash
GATEWAY_LISTEN=:8081 \
MASTER_WS_URL=ws://192.168.1.100:8080/ws/gateway \
AGENT_TOKENS=token-web-servers,token-db-servers \
MASTER_SECRET=shared-secret-with-master \
./bin/gateway
```

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `GATEWAY_LISTEN` | `:8081` | Gateway HTTP 监听地址 |
| `MASTER_WS_URL` | `ws://localhost:8080/ws/gateway` | Master WebSocket 地址 |
| `AGENT_TOKENS` | 自动生成安全随机值 | 逗号分隔的 Agent Token 列表，Agent 连接时需提供匹配的 Token |
| `MASTER_SECRET` | 自动生成安全随机值 | Master 到 Gateway 的认证密钥 |

> 注意：Agent Token 和 Master Secret 必须非空。未设置时会自动生成安全的随机值并打印到日志。

### 在目标机器上运行 Agent

```bash
# 自动生成 Agent ID（基于 hostname）
GATEWAY_URL=ws://192.168.1.50:8081/connect \
AGENT_TOKEN=token-web-servers \
./bin/agent

# 指定 Agent ID
GATEWAY_URL=ws://192.168.1.50:8081/connect \
AGENT_TOKEN=token-web-servers \
AGENT_ID=web-server-01 \
./bin/agent
```

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `AGENT_ID` | `{hostname}-{随机数}` | 机器唯一标识 |
| `GATEWAY_URL` | `ws://localhost:8081/connect` | Gateway WebSocket 地址 |
| `AGENT_TOKEN` | *(空)* | Gateway 认证 Token，必须匹配 Gateway 配置的 `AGENT_TOKENS` |

> 注意：Agent 上的文件上传/下载仅允许操作 `/tmp/` 和 `/opt/multi-ops/` 目录下的文件，且拒绝 setuid/setgid 权限位。

### 使用看板

打开 `http://localhost:8080` 登录。

1. **查看机器** -- Agent 连接后自动出现在看板上，显示主机名、OS、CPU/内存/磁盘、负载、网络等。指标每 5 秒刷新
2. **远程终端** -- 点击机器打开交互式 Shell，支持动态窗口缩放
3. **批量执行** -- 编写或选择脚本模板，选择目标机器（单个、按分组、按标签、全部在线），执行并查看结果
4. **文件分发** -- 上传文件到远程 Agent 或从 Agent 下载文件
5. **机器管理** -- 设置标签和分组，用于过滤操作
6. **审计日志** -- 查看所有 API 活动记录

### 部署 Agent 到生产环境 (systemd)

```bash
# 1. 复制 agent 二进制到目标机器
scp bin/agent root@target-server:/opt/multi-ops/agent

# 2. 运行安装脚本
curl -sSL http://MASTER_IP:8080/install-agent.sh | bash -s -- \
  --gateway ws://GATEWAY_IP:8081/connect \
  --token token-web-servers \
  --id web-server-01
```

安装脚本会创建：
- `/opt/multi-ops/agent.env` -- 配置文件
- `/etc/systemd/system/multi-ops-agent.service` -- Systemd 服务

```bash
systemctl status multi-ops-agent   # 查看状态
journalctl -u multi-ops-agent -f   # 查看日志
systemctl restart multi-ops-agent  # 重启
```

## 部署方式

### Docker Compose（单机开发/测试）

同一台机器上同时运行 Master 和 Gateway，自带健康检查、自动重启和审计日志持久化。适合开发、测试或内网使用。

**1. 准备环境变量**

```bash
cp .env.example .env

# 生成安全随机值
sed -i "s/JWT_SECRET=.*/JWT_SECRET=$(openssl rand -hex 32)/" .env
sed -i "s|ADMIN_PASSWORD=.*|ADMIN_PASSWORD=$(openssl rand -base64 24)|" .env
sed -i "s/AGENT_TOKENS=.*/AGENT_TOKENS=$(openssl rand -hex 16)/" .env
sed -i "s/MASTER_SECRET=.*/MASTER_SECRET=$(openssl rand -hex 32)/" .env
```

**2. 启动**

```bash
docker compose up -d --build
```

服务启动后：
- Master: `http://localhost:8080`
- Gateway: `http://localhost:8081`
- 健康检查: `docker compose ps`

**3. 查看日志**

```bash
docker compose logs -f master     # Master 日志
docker compose logs -f gateway    # Gateway 日志
```

**4. 更新**

```bash
git pull
docker compose down
docker compose up -d --build
```

> 注意：Agent 仍需在目标机器上单独运行，通过 Gateway 公网 IP 的 8081 端口连接。

### 手动部署（开发）

```bash
# 终端 1
make dev-master
# 注意：需要设置 JWT_SECRET 和 ADMIN_PASSWORD 环境变量

# 终端 2
AGENT_TOKENS=dev-token-1 \
MASTER_SECRET=dev-secret \
MASTER_WS_URL=ws://localhost:8080/ws/gateway \
make dev-gateway

# 终端 3
GATEWAY_URL=ws://localhost:8081/connect \
AGENT_TOKEN=dev-token-1 \
make dev-agent
```

### 生产环境部署

Gateway 部署在公网（云服务器），Master 部署在内网。Gateway 通过 WebSocket **主动连接** Master，因此 Master 无需公网 IP。

```
  公网                           内网
 ┌──────────────┐              ┌──────────────────────────┐
 │              │              │                          │
 │   Browser ◄──┼── HTTP/WS ──►│  Master (内网 :8080)     │
 │              │              │    ▲                      │
 │  +--------+  │              │    │                      │
 │  │Gateway │  │              │    │ WebSocket (出站)     │
 │  │ 公网:8081│◄─┼── WS ──────┼────┘                      │
 │  +--------+  │              │                          │
 │   ▲     ▲    │              │                          │
 │   │     │    │              └──────────────────────────┘
 │   │     │    │
 │ Agent1 Agent2│  (各目标机器上的 Agent 主动连接 Gateway)
 │              │
 └──────────────┘
```

**部署步骤：**

1. **内网 Master**：在内部网络的一台机器上运行 Master（Docker 或二进制），监听内网地址。
2. **公网 Gateway**：在云服务器上运行 Gateway，配置 `MASTER_WS_URL` 指向内网 Master 的地址（通过专线/VPN/内网穿透）。
3. **Agent 连接**：各目标机器上的 Agent 配置 `GATEWAY_URL` 指向公网 Gateway 地址。

生产环境检查清单：

### 安全配置
- [ ] 设置强随机的 `JWT_SECRET`（如 `openssl rand -hex 32`）
- [ ] 设置强 `ADMIN_PASSWORD`
- [ ] 设置具体的 `AGENT_TOKENS`（不使用自动生成的值）
- [ ] 设置 `MASTER_SECRET` 用于 Gateway 到 Master 的认证
- [ ] 为 admin 账户启用 TOTP：`POST /api/setup-totp`
- [ ] 配置 `IP_WHITELIST` 限制 Master 访问来源
- [ ] 文件上传/下载路径被限制在 `/tmp/` 和 `/opt/multi-ops/`

### Docker 部署
- [ ] `.env` 文件已创建且权限设为 `0600`（`chmod 600 .env`）
- [ ] `.env` 已加入 `.gitignore`，不会泄露到代码仓库
- [ ] 审计日志卷 `master-audit` 正常工作（`docker compose ps` 查看）
- [ ] 健康检查通过（`docker compose ps` 中 Master/Gateway 状态为 healthy）
- [ ] 容器非 root 运行（Dockerfile 已设置 `USER nobody`）
- [ ] 使用 nginx 反向代理并启用 TLS
- [ ] Docker 主机防火墙仅开放 8080（Master）和 8081（Gateway）端口

### 手动/裸机部署
- [ ] 使用 nginx 反向代理并启用 TLS
- [ ] 设置 `AUDIT_DIR` 到持久化且可备份的目录（权限 0700）
- [ ] 审计日志文件权限 0600
- [ ] Agent 以 systemd 服务运行，非 root 用户（推荐创建 `multi-ops` 用户）

## 角色说明

| 角色 | 权限 |
|---|---|
| `admin` | 全部权限：查看机器、执行脚本、上传/下载文件、远程命令、管理标签/分组、TOTP 设置 |
| `viewer` | 只读权限：查看机器列表和详情、查看执行历史、查看审计日志 |

## API 使用示例

所有受保护的端点需要 JWT。先登录获取 Token：

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"YOUR_PASSWORD"}' | jq -r '.token')
```

后续请求使用 `Authorization: Bearer $TOKEN` 头。

### 机器管理

```bash
# 查看所有机器
curl -s http://localhost:8080/api/machines -H "Authorization: Bearer $TOKEN"

# 按分组或标签过滤
curl -s "http://localhost:8080/api/machines?group=web-servers" \
  -H "Authorization: Bearer $TOKEN"
curl -s "http://localhost:8080/api/machines?tag=production" \
  -H "Authorization: Bearer $TOKEN"

# 机器详情
curl -s "http://localhost:8080/api/machine/detail?id=<agent-id>" \
  -H "Authorization: Bearer $TOKEN"

# 设置标签（仅 admin）
curl -s -X PUT "http://localhost:8080/api/machine/tags?id=<agent-id>" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"tags":["production","web-server"]}'

# 设置分组（仅 admin）
curl -s -X PUT "http://localhost:8080/api/machine/group?id=<agent-id>&group=web-servers" \
  -H "Authorization: Bearer $TOKEN"

# 查看所有分组 / 标签
curl -s http://localhost:8080/api/groups -H "Authorization: Bearer $TOKEN"
curl -s http://localhost:8080/api/machine/tags -H "Authorization: Bearer $TOKEN"
```

### 脚本执行（仅 admin）

```bash
# 在指定机器上执行脚本
curl -s -X POST http://localhost:8080/api/exec/batch \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "script": "uname -a && uptime",
    "timeout": 30,
    "agent_ids": ["<agent-id-1>", "<agent-id-2>"]
  }'

# 在所有在线机器上执行
curl -s -X POST http://localhost:8080/api/exec/batch \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"script": "df -h", "timeout": 10}'

# 在指定分组上执行
curl -s -X POST "http://localhost:8080/api/exec/batch?group=web-servers" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"script": "systemctl status nginx", "timeout": 10}'

# 查看执行历史（列表仅返回截断预览）
curl -s "http://localhost:8080/api/exec/history?limit=20&offset=0" \
  -H "Authorization: Bearer $TOKEN"

# 查看执行详情（包含完整脚本）
curl -s "http://localhost:8080/api/exec/detail?id=<request-id>" \
  -H "Authorization: Bearer $TOKEN"
```

### 脚本模板

```bash
curl -s http://localhost:8080/api/script-templates -H "Authorization: Bearer $TOKEN"
# 返回 sysinfo, disk, net, process, security, docker, update, mem_detail 等模板
```

### 文件上传/下载（仅 admin）

文件路径仅允许 `/tmp/` 和 `/opt/multi-ops/` 目录：

```bash
# 上传文件
curl -s -X POST http://localhost:8080/api/file/upload \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "agent_ids": ["<agent-id>"],
    "path": "/tmp/config.yml",
    "content": "key: value\nfoo: bar",
    "mode": "0644",
    "overwrite": true
  }'

# 请求下载文件（异步操作，结果通过 WebSocket 返回）
curl -s "http://localhost:8080/api/file/download?agent_id=<agent-id>&path=/tmp/config.yml" \
  -H "Authorization: Bearer $TOKEN"
```

### 远程命令（仅 admin）

```bash
curl -s -X POST http://localhost:8080/api/command \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"agent_id":"<agent-id>","command":"restart"}'
# command 可选值: restart, shutdown, reconnect
```

### 状态与审计

```bash
# 服务状态（无需认证）
curl -s http://localhost:8080/api/status

# 审计日志（仅 admin）
curl -s "http://localhost:8080/api/audit?n=100" -H "Authorization: Bearer $TOKEN"
```

### TOTP 双因素认证（仅 admin）

```bash
# 1. 启用 TOTP
curl -s -X POST http://localhost:8080/api/setup-totp \
  -H "Authorization: Bearer $TOKEN"
# 返回 secret 和 otpauth_url，扫码或手动输入到认证器应用

# 2. 登录后续登录需要 TOTP 码
curl -s -X POST http://localhost:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"YOUR_PASSWORD","totp_code":"123456"}'
```

### WebSocket

- **看板连接**：`ws://localhost:8080/ws/dashboard?token=<jwt>`，提供实时机器数据和终端会话
- **Gateway 连接**：Gateway 内部连接 `ws://localhost:8080/ws/gateway`

## 安全说明

- 密码使用 bcrypt 哈希存储
- 所有认证错误返回泛型 "unauthorized"，不泄露具体原因
- 登录接口严格限流（5 次/分钟/IP）
- WebSocket 仅允许同源连接
- 文件上传/下载限制在 `/tmp/` 和 `/opt/multi-ops/` 目录，拒绝 setuid/setgid 权限位
- 审计日志目录权限 0700，文件权限 0600
- 仅当请求来自私有 IP 时才信任 `X-Forwarded-For` 头
- CDN 资源使用 SRI 校验
- 终端会话 ID 使用 `crypto.randomUUID()` 生成

## 技术栈

- **语言**：Go 1.25
- **WebSocket**：gorilla/websocket
- **PTY**：creack/pty
- **密码哈希**：bcrypt (golang.org/x/crypto)
- **认证**：自定义 JWT (HMAC-SHA256) + TOTP (RFC 6238, HMAC-SHA1)
- **存储**：内存存储（带 RWMutex 的线程安全 Map）
- **前端**：`embed.FS` 嵌入的静态文件 + xterm.js
