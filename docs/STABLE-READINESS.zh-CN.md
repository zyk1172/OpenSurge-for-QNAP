# QNAP 稳定版准备：安全与故障边界

本文记录 stable-readiness 加固后的关键安全与故障边界。

## same-LAN 路由默认断流

same-LAN QNET 部署会安装两条基于入口接口的 RPDB 规则：

1. OpenSurge 主规则，把流量送入专用路由表；
2. 后一优先级的 `prohibit` 保护规则。

保护规则先安装、最后删除。因此主规则或 TUN 默认路由意外消失时，客户端转发流量会被拒绝，而不是继续掉入容器 `main` 路由表直连。显式 DIRECT fallback 仍通过专用表中指向真实上游网关的默认路由实现。

保护规则、主规则和专用路由表仍属于 OpenSurge 自有资源，并沿用现有的写前清理日志与精确回滚机制。

## 首次管理员启动令牌

QNAP 首次创建管理员前必须输入一次性 32 字节启动令牌。令牌保存在 `/data/web-auth/bootstrap-token`，权限为 `0600`，首次创建时同时打印到容器日志。管理员创建成功后令牌立即失效并删除。

已有安装会把旧的 `/data/control/admin.json` 管理员凭据迁移到 `/data/web-auth/admin.json`，不会要求重新创建账号。

## 一个容器、两个权限域

QNAP 仍然只部署一个 Docker 容器，但 entrypoint 在容器内监管两个 OpenSurge 进程：

- Control 进程以 root 运行，保留容器网络管理能力，只监听 `127.0.0.1`；
- 面向 LAN 的 Web 进程以 UID/GID 65532 运行，capability 集为空。

Web 通过内部 bearer token 把已认证的 QNAP 操作代理到 loopback Control。Mac 专属 LAN API 在 QNAP Web 边界直接拒绝。QNAP 运行镜像不再包含旧 manager/orchestrator 二进制。

## 监听端口暴露

透明 TUN 模式开启时，Mihomo mixed 端口只绑定 `127.0.0.1`，并关闭 `allow-lan`。Mihomo 内部 DNS 1053 始终只监听 loopback；LAN 上的 DNS 仍由 dnsmasq 的 53 端口提供。

只有显式关闭透明模式、进入手动代理用途时，mixed 端口才允许面向 LAN。

## 订阅网络策略

远程订阅仍只允许 HTTPS，并在每次连接时重新解析地址。所有解析结果都必须通过公网目标校验。私网、loopback、link-local、CGNAT/Tailscale `100.64.0.0/10`、文档、benchmark 和保留地址段都会在拨号前拒绝。

## HTTPS 反向代理模式

局域网直连 HTTP 仍受支持。通过可信 HTTPS 反向代理访问时，可设置 `OPENSURGE_SECURE_COOKIES=true`；管理员 cookie 会增加 `Secure`，Web 响应增加 HSTS。直接使用明文 HTTP 时不要开启该选项。

## Readiness 与发布供应链

`/health/ready` 现在会携带内部认证实际访问 loopback Control 配置接口，不再把“端口能返回 HTTP”当成 ready。

rolling 测试版发布流程会固定高权限 GitHub Actions 版本，使用 digest 固定的基础镜像，发布 SPDX JSON SBOM，并为两个架构的 Docker 归档生成 GitHub build provenance 与 SBOM attestation。`SHA256SUMS` 同时覆盖 Docker 归档和 SBOM 文件。
