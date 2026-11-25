# singbox-agent

为裸 sing-box 提供受限的远程管理 API。它不是 2S-UI，不保存中心用户数据库，也不替换 sing-box；中心面板通过它读取配置并安全应用变更。

## 当前 MVP

- Bearer Token 鉴权
- `/v1/health`：Agent 和 sing-box 状态
- `/v1/info`：受限运行信息
- `/v1/inbounds`：读取 sing-box 入站及 users
- `/v1/config/validate`：临时文件 + `sing-box check`
- `/v1/config/apply`：校验、原子替换、可选重启、失败回滚
- `/v1/core/restart`：重启指定 systemd 服务
- `/etc/singbox-agent/metadata.yaml`：可选的公网订阅元数据
- 兼容 2S-UI master 的 `/app/apiv2/status`、`onlines`、`inbounds`、`clients`、`save` 和 `restartSb` 接口
- 兼容接口支持 VLESS、Trojan、Hysteria2 入站中的基础用户读取和增改删
- 可通过 sing-box `experimental.v2ray_api` 查询用户级上下行计数

用户级流量统计需要在 sing-box 配置中启用：

```json
{
  "experimental": {
    "v2ray_api": {
      "listen": "127.0.0.1:10085",
      "stats": {
        "enabled": true,
        "users": ["alice", "bob"]
      }
    }
  }
}
```

然后在 `/etc/default/singbox-agent` 设置：

```ini
SINGBOX_V2RAY_API=127.0.0.1:10085
```

Agent 查询的是 sing-box 原生 `user>>>用户名>>>traffic>>>uplink/downlink` 计数器，不会把节点总流量错误分摊给用户。

订阅公网元数据示例见 `metadata.example.yaml`。Agent 会把它与 `config.json` 合并：协议、用户、TLS/传输和 Hy2 服务端参数优先读取 sing-box 配置，公网域名和外部端口从元数据覆盖。兼容 API 可以让现有 2S-UI 节点监控链路直接连接 Agent。

## 构建

```bash
go build -trimpath -o singbox-agent .
```

## 初始化

```bash
sudo install -d -m 0750 /etc/singbox-agent
openssl rand -hex 32 | sudo tee /etc/singbox-agent/token >/dev/null
sudo chmod 0600 /etc/singbox-agent/token
sudo install -m 0755 singbox-agent /usr/local/bin/singbox-agent
sudo install -m 0644 singbox-agent.service /etc/systemd/system/singbox-agent.service
sudo systemctl daemon-reload
sudo systemctl enable --now singbox-agent
```

默认只监听 `127.0.0.1:2088`。systemd 服务通过 `/etc/default/singbox-agent` 传入可选参数；未填写时使用程序默认值。可参考 `singbox-agent.env.example`。接入中心面板前，建议改为仅监听 WireGuard/Tailscale 管理地址，并使用 HTTPS/mTLS；不要直接暴露公网。

## API 示例

```bash
TOKEN=$(sudo cat /etc/singbox-agent/token)
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:2088/v1/health
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:2088/v1/inbounds
```

配置应用请求体：

```json
{"config": {"log": {"level": "info"}, "inbounds": [], "outbounds": []}, "restart": true}
```

Agent 会先运行 `sing-box check`，校验失败不会替换现有配置；重启失败会回滚旧配置。
