# QNAP 稳定版：安全与故障边界

本文记录 `v1.0.x` 的主要安全、权限、故障恢复与发布边界。

## same-LAN 路由默认断流

same-LAN QNET 部署会安装两条基于入口接口的 RPDB 规则：

1. OpenSurge 主规则，把流量送入专用路由表；
2. 后一优先级的 `prohibit` 保护规则。

保护规则先安装、最后删除。因此主规则或 TUN 默认路由意外消失时，客户端转发流量会被拒绝，而不是继续掉入容器 `main` 路由表直连。显式 DIRECT fallback 仍通过专用表中指向真实上游网关的默认路由实现。

## 首次管理员启动令牌

QNAP 首次创建管理员前必须输入一次性 32 字节启动令牌。令牌保存在 `/data/web-auth/bootstrap-token`，权限为 `0600`，首次创建时同时打印到容器日志。管理员创建成功后令牌立即失效并删除。

已有安装会把旧的 `/data/control/admin.json` 管理员凭据迁移到 `/data/web-auth/admin.json`，不会要求重新创建账号。

## 一个容器、两个权限域

QNAP 只部署一个 Docker 容器，但 entrypoint 在容器内监管两个 OpenSurge 进程：

- Control 进程以 root 运行，保留容器网络管理能力，只监听 `127.0.0.1`；
- 面向 LAN 的 Web 进程以配置的非特权 UID/GID 运行，capability 集为空。

Web 通过内部 bearer token 把已认证的 QNAP 操作代理到 loopback Control。QNAP 运行镜像不包含旧 manager/orchestrator 二进制，也不挂载 Docker Socket。

## NAS Host Takeover 权限显式启用

稳定版默认 Compose 只授予：

```text
NET_ADMIN
NET_RAW
/dev/net/tun
```

不会默认授予 `SYS_ADMIN`，也不会默认挂载 QNAP host network namespace。

只有叠加：

```text
deploy/qnap/docker-compose.host-takeover.yml
```

才增加：

```text
CAP_SYS_ADMIN
/proc/1/ns/net -> /run/opensurge/host-netns:ro
```

NAS Host Takeover 使用动态 RPDB priority。除精确 `from <NAS-IP>` 外，也会识别包含 NAS IP 的 CIDR 和 `from all` 规则，避免被更早的 QTS policy shadow。实际 route lookup 未命中时拒绝启用并回滚。

## 监听端口暴露

透明 TUN 模式开启时，Mihomo mixed 端口只绑定 `127.0.0.1`，并关闭 `allow-lan`。Mihomo 内部 DNS 1053 始终只监听 loopback；LAN 上的 DNS 由 dnsmasq 的 53 端口提供。

只有显式关闭透明模式、进入手动代理用途时，mixed 端口才允许面向 LAN。

## 订阅网络策略

远程订阅只允许 HTTPS，并在每次连接时重新解析地址。所有解析结果都必须通过公网目标校验。私网、loopback、link-local、CGNAT/Tailscale `100.64.0.0/10`、文档、benchmark 和保留地址段都会在拨号前拒绝。

## HTTPS 反向代理模式

局域网直连 HTTP 受支持。通过可信 HTTPS 反向代理访问时，可设置 `OPENSURGE_SECURE_COOKIES=true`；管理员 cookie 会增加 `Secure`，Web 响应增加 HSTS。直接使用明文 HTTP 时不要开启该选项。

## Readiness

`/health/ready` 携带内部认证实际访问 loopback Control 配置接口，不把“端口能返回 HTTP”当成 ready。

## 发布供应链

正式 `vX.Y.Z` tag 触发 stable workflow，并执行：

- `govulncheck`；
- 最终容器 HIGH / CRITICAL Trivy gate；
- amd64 / arm64 构建；
- SPDX JSON SBOM；
- `SHA256SUMS`；
- GitHub build provenance 与 SBOM attestation；
- GitHub stable Release；
- GHCR 多架构镜像；
- Docker Hub `zyk1172/opensurge-for-qnap` 多架构镜像。

高权限 GitHub Actions 固定到 commit SHA。正式 Release 不覆盖同名已存在版本。
