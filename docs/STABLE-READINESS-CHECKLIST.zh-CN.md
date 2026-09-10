# QNAP 稳定版准备验收清单

合并 stable-readiness 改动前至少验证：

- same-LAN 策略路由同时存在主 `iif -> table` 规则和后一优先级的 `iif -> prohibit` 保护规则；
- 删除主规则或 TUN 默认路由后都不能掉入容器 main 路由表直连；
- 首次管理员 setup 会拒绝缺失/错误启动令牌，并在正确令牌成功使用后立即失效；
- LAN Web 进程以非特权用户运行且有效 capabilities 为空，Control 仍只监听 loopback；
- QNAP 运行镜像不包含 `opensurge-manager` 和 `opensurge-orchestrator`；
- TUN 模式下 Mihomo mixed 端口和内部 DNS 只监听 loopback；
- 订阅 URL 拨号会拒绝私网、CGNAT/Tailscale、文档、benchmark 和保留地址；
- `/health/ready` 会通过内部认证访问真实 Control API；
- 容器重建后已有管理员仍存在，并且不会重新产生首次启动令牌；
- amd64 与 arm64 镜像都可以构建；
- rolling 测试版会发布 Docker 归档、SPDX JSON SBOM、校验和以及 GitHub attestations。
