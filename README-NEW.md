# 2S-UI 二开版部署与使用说明

本文档说明当前二开版 2S-UI 的安装、部署、节点接入和日常使用方式。

当前版本同时支持两种节点架构：

1. **原生 2S-UI 节点**：VPS 上安装完整 2S-UI，由中心面板通过节点 API 管理。
2. **裸 sing-box + singbox-agent 节点**：VPS 上只运行裸 sing-box 和轻量 Agent，不安装完整 2S-UI，由中心面板统一管理。

两种节点可以在同一个中心面板中混合使用。

> 本项目仅用于个人学习和技术交流。生产环境部署前，请自行完成安全审计、备份和权限隔离。

---

## 一、整体架构

### 1. 原生 2S-UI 多节点模式

```text
                 ┌────────────────────────┐
                 │      中心 2S-UI         │
                 │ 用户、节点、订阅、统计   │
                 └───────────┬────────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
      ┌───────▼───────┐ ┌────▼────────┐ ┌───▼────────┐
      │ VPS 1          │ │ VPS 2       │ │ VPS 3      │
      │ 2S-UI+sing-box │ │ 2S-UI+      │ │ 2S-UI+     │
      │                 │ │ sing-box    │ │ sing-box   │
      └─────────────────┘ └─────────────┘ └────────────┘
```

适合新部署、希望每台节点都具备完整本地面板的场景。

### 2. 裸 sing-box + Agent 模式

```text
                 ┌────────────────────────┐
                 │      中心 2S-UI         │
                 │ 用户、节点、订阅、统计   │
                 └───────────┬────────────┘
                             │ HTTPS/WireGuard/Tailscale
              ┌──────────────┼──────────────┐
              │              │              │
      ┌───────▼────────┐ ┌───▼──────────┐ ┌─▼───────────┐
      │ VPS 1           │ │ VPS 2        │ │ VPS 3       │
      │ 裸 sing-box     │ │ 裸 sing-box  │ │ 裸 sing-box │
      │ singbox-agent   │ │ Agent        │ │ Agent       │
      └─────────────────┘ └──────────────┘ └─────────────┘
```

Agent 是独立的 systemd 服务，不是 sing-box 子进程，负责：

- 读取和解析裸 sing-box 配置；
- 管理 VLESS、Trojan、Hysteria2 入站用户；
- 校验和原子应用配置；
- 重启 sing-box，失败时恢复旧配置；
- 查询 sing-box 用户级流量统计；
- 为中心面板提供兼容的 2S-UI API；
- 提供订阅生成需要的公网地址和端口元数据。

---

## 二、目录说明

```text
2s-ui-bare/
├── agent/                         # singbox-agent 源码和 systemd 文件
│   ├── metadata.example.yaml      # 订阅公网元数据示例
│   ├── singbox-agent.env.example  # Agent 环境变量示例
│   └── singbox-agent.service      # Agent systemd 单元
├── frontend/                      # Vue 前端
├── service/                       # 中心端业务逻辑
├── database/                      # 数据模型和数据库
├── api/                           # 中心端 API
├── build.sh                       # 前端和后端完整构建脚本
└── README-NEW.md                  # 本部署说明
```

---

## 三、环境要求

### 中心 2S-UI

建议运行在独立管理服务器上：

- Linux amd64 或 arm64；
- systemd；
- Go 1.27 或项目要求的 Go 版本；
- Node.js 和 npm，仅源码构建时需要；
- 可访问各 VPS 的 Agent 或节点 API；
- 建议使用 HTTPS；
- 建议使用 WireGuard、Tailscale 或独立管理网连接节点。

### 裸节点 VPS

- Linux + systemd；
- 已安装 sing-box；
- sing-box 配置位于 `/etc/sing-box/config.json`；
- sing-box 二进制默认位于 `/usr/local/bin/sing-box`；
- sing-box systemd 服务默认名为 `sing-box`；
- Agent 与 sing-box 使用同一台 VPS，但使用独立 systemd 服务。

---

## 四、从源码构建中心面板

进入项目目录：

```bash
cd /Users/jimmie/codex-default-workspace/2s-ui-bare
```

完整构建：

```bash
./build.sh
```

`build.sh` 会依次执行：

1. 安装前端依赖；
2. 构建主面板前端；
3. 构建订阅页面；
4. 将前端文件复制到 `web/html`；
5. 编译中心端二进制 `sui`。

如果只验证后端：

```bash
go test ./agent ./service ./database/... ./api
```

如果单独构建前端：

```bash
cd frontend
npm install
npm run build
```

如果构建时出现 `vue-tsc: command not found`，说明前端依赖尚未安装，先执行：

```bash
cd frontend
npm install
```

---

## 五、部署中心 2S-UI

### 1. 准备目录和用户

以下示例将中心面板安装到 `/usr/local/2s-ui`：

```bash
sudo useradd --system --home /usr/local/2s-ui --shell /usr/sbin/nologin sui || true
sudo mkdir -p /usr/local/2s-ui
sudo cp sui /usr/local/2s-ui/sui
sudo chown -R sui:sui /usr/local/2s-ui
sudo chmod 0755 /usr/local/2s-ui/sui
```

### 2. 创建 systemd 服务

创建 `/etc/systemd/system/s-ui.service`：

```ini
[Unit]
Description=2S-UI central management panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=sui
WorkingDirectory=/usr/local/2s-ui
ExecStart=/usr/local/2s-ui/sui
Restart=always
RestartSec=5
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
```

创建数据库目录并启动：

```bash
sudo mkdir -p /usr/local/2s-ui/db /usr/local/2s-ui/cert
sudo chown -R sui:sui /usr/local/2s-ui
sudo systemctl daemon-reload
sudo systemctl enable --now s-ui
sudo systemctl status s-ui
```

默认访问地址：

```text
http://中心服务器IP:2095/app/
```

默认登录信息：

```text
用户名：admin
密码：admin
```

首次登录后必须立即修改密码，并启用 TOTP 二次验证。

> 也可以继续使用项目原有的安装脚本、Docker 或发行版安装方式。二开功能已经包含在当前源码和构建产物中。

---

## 六、部署方式一：原生 2S-UI 节点

### 1. 在 VPS 上安装完整 2S-UI

按照原项目方式安装 2S-UI 和 sing-box。确认服务正常：

```bash
sudo systemctl enable --now s-ui
sudo systemctl enable --now sing-box
sudo systemctl status s-ui
sudo systemctl status sing-box
```

### 2. 在中心面板添加节点

进入：

```text
节点 → 添加节点
```

填写：

- 节点名称；
- 节点 API 地址；
- 节点端口；
- 节点访问令牌或认证信息；
- 节点类型选择 `2S-UI 节点`。

保存后执行节点测试，确认状态正常。

### 3. 采用远程入站

打开节点卡片的导入按钮，面板会读取远程 2S-UI 的入站列表。勾选需要纳入中心管理的入站，然后导入。

导入后可以：

- 将中心用户绑定到入站；
- 将中心用户同步到节点；
- 汇总节点流量；
- 将节点地址合并到统一订阅。

---

## 七、部署方式二：裸 sing-box + singbox-agent

### 1. 安装 sing-box

确保 VPS 上已有裸 sing-box：

```bash
sudo install -m 0755 sing-box /usr/local/bin/sing-box
sudo install -d -m 0750 /etc/sing-box
```

确认配置：

```bash
/usr/local/bin/sing-box check -c /etc/sing-box/config.json
```

确认 sing-box 服务：

```bash
sudo systemctl enable --now sing-box
sudo systemctl status sing-box
```

### 2. 构建 Agent

在项目目录执行：

```bash
cd agent
go build -trimpath -o singbox-agent .
```

将二进制复制到 VPS：

```bash
sudo install -m 0755 singbox-agent /usr/local/bin/singbox-agent
```

### 3. 创建 Agent 认证 Token

```bash
sudo install -d -m 0750 /etc/singbox-agent
openssl rand -hex 32 | sudo tee /etc/singbox-agent/token >/dev/null
sudo chmod 0600 /etc/singbox-agent/token
```

读取 Token：

```bash
sudo cat /etc/singbox-agent/token
```

Token 只在安全环境中保存，不要提交到 Git，也不要发送到公共聊天群。

### 4. 配置 Agent 环境变量

创建 `/etc/default/singbox-agent`：

```ini
SINGBOX_AGENT_LISTEN=127.0.0.1:2088
SINGBOX_CONFIG=/etc/sing-box/config.json
SINGBOX_BINARY=/usr/local/bin/sing-box
SINGBOX_SERVICE=sing-box
SINGBOX_AGENT_TOKEN_FILE=/etc/singbox-agent/token
SINGBOX_AGENT_METADATA=/etc/singbox-agent/metadata.yaml
# 如果启用了用户流量统计，再填写：
SINGBOX_V2RAY_API=127.0.0.1:10085
```

所有参数也可以通过命令行传入：

```bash
singbox-agent \
  --listen 127.0.0.1:2088 \
  --config /etc/sing-box/config.json \
  --binary /usr/local/bin/sing-box \
  --service sing-box \
  --token-file /etc/singbox-agent/token \
  --metadata /etc/singbox-agent/metadata.yaml \
  --v2ray-api 127.0.0.1:10085
```

### 5. 安装 systemd 服务

```bash
sudo install -m 0644 singbox-agent.service /etc/systemd/system/singbox-agent.service
sudo systemctl daemon-reload
sudo systemctl enable --now singbox-agent
sudo systemctl status singbox-agent
```

检查健康状态：

```bash
TOKEN=$(sudo cat /etc/singbox-agent/token)
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:2088/v1/health
```

查看入站：

```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:2088/v1/inbounds
```

### 6. 配置管理网络

默认 Agent 只监听：

```text
127.0.0.1:2088
```

如果中心面板位于其他服务器，不建议直接把 Agent 暴露到公网。推荐顺序：

1. WireGuard 私网地址；
2. Tailscale 私网地址；
3. VPS 专用管理网；
4. 最后才考虑公网访问，并配合防火墙、HTTPS、mTLS 和源 IP 白名单。

例如使用 WireGuard 管理地址：

```ini
SINGBOX_AGENT_LISTEN=10.20.0.11:2088
```

同时只允许中心管理服务器访问该端口：

```bash
sudo ufw allow from 10.20.0.1 to any port 2088 proto tcp
sudo ufw deny 2088/tcp
```

### 7. 在中心面板添加 Agent 节点

进入：

```text
节点 → 添加节点
```

填写：

- 节点名称；
- Agent 管理地址，例如 `http://10.20.0.11:2088`；
- Bearer Token；
- 节点类型选择 `裸 sing-box + Agent`。

保存后执行节点测试。

---

## 八、裸 sing-box 的订阅元数据

sing-box 配置通常只能说明：

```text
监听地址 = 0.0.0.0
监听端口 = 9443
```

它无法可靠推导：

- 公网域名；
- NAT 后的外部端口；
- CDN 域名；
- Nginx/Caddy 反代地址；
- 实际客户端 SNI。

因此 Agent 支持独立元数据文件：

```text
/etc/singbox-agent/metadata.yaml
```

复制示例：

```bash
sudo install -m 0640 agent/metadata.example.yaml \
  /etc/singbox-agent/metadata.yaml
```

示例：

```yaml
inbounds:
  hy2-in:
    server: ora.61008281.xyz
    port: 9443
    sni: ora.61008281.xyz
    obfs: salamander
    obfs-password: rrrrrr

  vless-in:
    server: ora.61008281.xyz
    port: 443
    sni: ora.61008281.xyz

  trojan-in:
    server: ora.61008281.xyz
    port: 8443
    sni: ora.61008281.xyz
```

修改后重启 Agent：

```bash
sudo systemctl restart singbox-agent
```

元数据覆盖优先级：

```text
metadata.yaml
    > sing-box config.json 可推导字段
    > 自动探测字段
    > 空值
```

注意：元数据文件不需要写用户 UUID 或密码，用户信息仍从 sing-box 配置读取。

---

## 九、启用用户级流量统计

sing-box 配置中加入或合并：

```json
{
  "experimental": {
    "v2ray_api": {
      "listen": "127.0.0.1:10085",
      "stats": {
        "enabled": true,
        "users": []
      }
    }
  }
}
```

然后在 Agent 环境文件中设置：

```ini
SINGBOX_V2RAY_API=127.0.0.1:10085
```

重启：

```bash
sudo systemctl restart sing-box
sudo systemctl restart singbox-agent
```

Agent 使用 sing-box 原生计数器：

```text
user>>>用户名>>>traffic>>>uplink
user>>>用户名>>>traffic>>>downlink
```

当中心面板新增或修改用户时，Agent 会在启用 `v2ray_api.stats.enabled` 的前提下自动维护统计用户列表。

不要将 `v2ray_api` 监听地址设置为公网地址。该接口应只监听 `127.0.0.1` 或受保护的管理网。

---

## 十、从裸 sing-box 配置迁移到中心面板

二开版提供了“导入裸 sing-box 配置”功能。

### 适用场景

原 VPS 上已经存在：

```text
/etc/sing-box/config.json
```

现在希望改为由中心 2S-UI 管理，不想重新手工创建每个入站和用户。

### 操作步骤

1. 在中心面板创建目标节点；
2. 如果 VPS 已经部署 Agent，节点类型选择 `裸 sing-box + Agent`；
3. 打开节点卡片的导入按钮；
4. 选择本地裸 sing-box 的 `config.json`；
5. 点击“导入裸 sing-box 配置”；
6. 等待导入完成；
7. 检查入站、用户和绑定关系；
8. 配置该节点的公网元数据；
9. 执行节点同步或重新生成订阅。

### 会导入的内容

- `inbounds` 中的入站；
- 入站协议和 Tag；
- TLS、Reality、传输参数；
- VLESS、Trojan、Hysteria2 用户；
- 用户凭据；
- 用户与入站绑定关系。

### 不会导入的内容

当前导入功能不会把以下内容直接写入中心数据库：

- sing-box 出站；
- DNS；
- 路由规则；
- 证书文件本体；
- 系统级防火墙；
- Nginx/Caddy 配置；
- VPS 上的其他系统服务。

导入过程不会覆盖中心已有的入站。如果发现重复 Tag，会终止本次导入并回滚，避免误覆盖。

### 迁移建议

生产迁移前建议：

```bash
sudo cp /etc/sing-box/config.json \
  /etc/sing-box/config.json.backup.$(date +%Y%m%d-%H%M%S)
```

迁移后先验证：

```bash
/usr/local/bin/sing-box check -c /etc/sing-box/config.json
sudo systemctl status sing-box
sudo systemctl status singbox-agent
```

不要在没有备份的情况下直接执行中心端批量同步。

---

## 十一、日常使用流程

### 新增入站

1. 进入“入站”；
2. 选择协议：VLESS、Trojan 或 Hysteria2；
3. 配置监听地址、端口、TLS、Reality 或传输；
4. 选择目标节点；
5. 保存并执行同步；
6. 检查节点状态和订阅链接。

### 新增用户

1. 进入“客户端/用户”；
2. 创建用户；
3. 设置 UUID、密码、流量和到期时间；
4. 绑定一个或多个入站；
5. 保存；
6. 等待节点同步；
7. 从订阅页面获取链接或二维码。

### 禁用用户

禁用用户后，中心端会将用户状态同步到对应节点。建议同时检查：

- 用户是否仍出现在入站配置；
- Agent 是否正常；
- sing-box 是否成功重启；
- 节点日志是否有配置校验错误。

### 查看流量

中心面板可以查看：

- 节点流量；
- 入站流量；
- 用户流量；
- 在线状态；
- 节点健康状态。

裸 sing-box 节点的用户级流量依赖 `experimental.v2ray_api`，未启用时只能获得有限的节点状态和配置数据。

---

## 十二、Agent API 参考

所有 API 都需要：

```http
Authorization: Bearer <TOKEN>
```

### 健康检查

```http
GET /v1/health
```

### 运行信息

```http
GET /v1/info
```

### 入站和用户

```http
GET /v1/inbounds
```

### 流量统计

```http
GET /v1/stats
```

### 配置校验

```http
POST /v1/config/validate
```

### 应用配置

```http
POST /v1/config/apply
```

Agent 会执行：

1. 写入临时文件；
2. 运行 `sing-box check`；
3. 原子替换配置；
4. 按请求重启 sing-box；
5. 重启失败时恢复旧配置。

### 重启核心

```http
POST /v1/core/restart
```

### 2S-UI 兼容接口

```text
GET  /app/apiv2/status
GET  /app/apiv2/onlines
GET  /app/apiv2/inbounds
GET  /app/apiv2/clients
POST /app/apiv2/save
POST /app/apiv2/restartSb
```

---

## 十三、常用排障命令

### 中心面板

```bash
sudo systemctl status s-ui
sudo journalctl -u s-ui -n 200 --no-pager
```

### sing-box

```bash
sudo systemctl status sing-box
sudo journalctl -u sing-box -n 200 --no-pager
/usr/local/bin/sing-box check -c /etc/sing-box/config.json
```

### Agent

```bash
sudo systemctl status singbox-agent
sudo journalctl -u singbox-agent -n 200 --no-pager
```

### 测试 Agent

```bash
TOKEN=$(sudo cat /etc/singbox-agent/token)

curl -i \
  -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:2088/v1/health

curl -i \
  -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:2088/v1/inbounds
```

### 检查端口

```bash
sudo ss -lntp | grep -E '2088|10085|2095|2096'
```

常见问题：

| 现象 | 检查方向 |
|---|---|
| 中心节点离线 | Agent 地址、Token、防火墙、WireGuard/Tailscale 路由 |
| Agent 启动失败 | `EnvironmentFile`、Token 文件、配置路径、文件权限 |
| 配置同步失败 | `sing-box check`、JSON 格式、协议参数、服务权限 |
| 用户流量为 0 | `v2ray_api` 是否启用、用户是否写入 `stats.users` |
| 订阅地址错误 | 检查 `metadata.yaml` 中的 `server` 和 `port` |
| 重启后配置回退 | sing-box 校验失败或 systemd 服务名不正确 |
| 导入重复失败 | 中心已有相同入站 Tag，先改名或删除冲突入站 |

---

## 十四、备份与升级

### 中心数据库备份

中心数据库位置依安装方式而定，常见为项目目录下的 `db`：

```bash
sudo systemctl stop s-ui
sudo tar czf /root/2s-ui-backup-$(date +%Y%m%d-%H%M%S).tar.gz \
  /usr/local/2s-ui/db \
  /usr/local/2s-ui/cert
sudo systemctl start s-ui
```

### 裸节点备份

```bash
sudo cp -a /etc/sing-box /root/sing-box-backup-$(date +%Y%m%d-%H%M%S)
sudo cp -a /etc/singbox-agent /root/singbox-agent-backup-$(date +%Y%m%d-%H%M%S)
```

### 升级顺序

推荐顺序：

1. 备份中心数据库；
2. 备份每台 VPS 的 sing-box 配置和 Agent 配置；
3. 在测试环境构建新版本；
4. 先升级中心面板；
5. 再逐台升级 Agent；
6. 最后执行节点同步；
7. 检查订阅、用户登录和流量统计。

不要同时停止所有节点，也不要在没有回滚备份的情况下批量升级。

---

## 十五、当前边界和注意事项

1. Agent 不应直接暴露公网。
2. `experimental.v2ray_api` 只建议监听 `127.0.0.1`。
3. sing-box 配置中的监听地址不等于公网连接地址，订阅公网信息应通过 `metadata.yaml` 补充。
4. 导入裸配置主要用于迁移入站和用户，不会自动迁移整个 VPS 的系统网络环境。
5. 复杂的 Nginx/Caddy 反代、CDN、NAT、端口跳跃场景，需要管理员手动填写元数据并验证订阅。
6. 修改中心面板或 Agent 前，必须先备份数据库、配置文件和 Token。
7. 原生 2S-UI 节点与裸 sing-box + Agent 节点可以混合使用，但应明确每个节点的管理边界，避免同时由多个面板直接修改同一份 sing-box 配置。

---

## 十六、推荐的三 VPS 部署方案

如果三台 VPS 当前都运行裸 sing-box，推荐：

```text
中心管理服务器：2S-UI
VPS 1：裸 sing-box + singbox-agent
VPS 2：裸 sing-box + singbox-agent
VPS 3：裸 sing-box + singbox-agent
```

每台 VPS：

1. 保留现有 sing-box 配置；
2. 安装 Agent；
3. 配置 Token；
4. 配置 `metadata.yaml`；
5. 启用用户级流量统计；
6. 通过 WireGuard/Tailscale 接入中心；
7. 在中心面板选择“裸 sing-box + Agent”；
8. 使用导入功能迁移现有配置；
9. 验证入站、用户、订阅和流量统计；
10. 再开始日常统一管理。

这样可以在不安装完整 2S-UI 的前提下，保留裸 sing-box 的轻量性，同时获得中心化的用户、节点和订阅管理能力。
