# QNAP v1.0 稳定版验收清单

代码与发布工程应满足：

- same-LAN 策略路由同时存在主 `iif -> table` 规则和后一优先级的 `iif -> prohibit` 保护规则；
- 删除主规则或 TUN 默认路由后不能掉入容器 main 路由表直连；
- 首次管理员 setup 会拒绝缺失/错误启动令牌，并在正确令牌成功使用后立即失效；
- LAN Web 进程以非特权用户运行且有效 capabilities 为空，Control 仍只监听 loopback；
- QNAP 运行镜像不包含 `opensurge-manager` 和 `opensurge-orchestrator`；
- TUN 模式下 Mihomo mixed 端口和内部 DNS 只监听 loopback；
- 订阅 URL 拨号拒绝私网、CGNAT/Tailscale、文档、benchmark 和保留地址；
- `/health/ready` 通过内部认证访问真实 Control API；
- 容器重建后已有管理员仍存在，并且不会重新产生首次启动令牌；
- 默认 Compose 只授予 `NET_ADMIN` / `NET_RAW`，不默认授予 `SYS_ADMIN` 或挂载宿主 network namespace；
- NAS Host Takeover 仅通过 `docker-compose.host-takeover.yml` 显式启用高权限边界；
- Host Takeover priority 会考虑精确源地址、包含 NAS 地址的 CIDR 和 `from all` QTS policy，冲突或实际 route lookup 未命中时拒绝启用并回滚；
- amd64 与 arm64 镜像都能由发布流水线构建；
- `vX.Y.Z` stable tag 先通过 `govulncheck` 与容器 HIGH/CRITICAL 漏洞 gate；
- stable Release 发布 Docker 归档、SPDX JSON SBOM、`SHA256SUMS`、build provenance 和 SBOM attestation；
- 同一 stable workflow 发布 GHCR 多架构镜像；
- 配置 Docker Hub 凭据后，同一 stable workflow 发布 `zyk1172/opensurge-for-qnap:<version>`、`:<tag>` 和 `:latest` 多架构镜像；
- stable Release 不覆盖同名既有版本。

真机长稳、reboot、升级/回滚等验收属于发布操作验证，不在本轮代码修改范围内。
