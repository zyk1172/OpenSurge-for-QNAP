# OpenSurge for QNAP

> **当前状态：Phase 1 / Linux 数据面移植中。尚未发布可用于 QNAP 的正式 Docker 镜像。**

OpenSurge for QNAP 是基于
[OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac) 的派生项目，目标是把上游的
Web 控制面、mihomo 配置/策略能力和网关生命周期移植为适合 QNAP NAS 与通用 Linux Docker
长期运行的透明代理网关。

本项目不是上游作者官方提供的 QNAP 版本，也未获得上游背书，除非上游作者另有明确声明。

- 原始项目：OpenSurge for Mac
- 原作者 / 组织：YTwsy
- 上游仓库：<https://github.com/YTwsy/OpenSurge-for-Mac>
- Fork baseline：`b03bf2f8a2b02a6fffba9c879ce1980ddb831a67`（v0.2.2）
- Fork 日期：2026-09-09
- 许可证：`GPL-3.0-only`，继承自上游且未修改
- 详细来源关系：[docs/UPSTREAM.md](docs/UPSTREAM.md)
- 许可证/来源说明：[NOTICE.md](NOTICE.md)、[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)

## 产品目标

稳定版的目标拓扑是：

```text
LAN Client
   │
   │ Gateway / DNS = OpenSurge container
   ▼
QNAP / Linux
└─ OpenSurge container
   ├─ Web UI / Control API
   ├─ mihomo TUN
   ├─ DNS / dnsmasq
   ├─ nftables（仅 OpenSurge 自有表）
   └─ iproute2 policy routing
          │
          ├─ DIRECT
          └─ PROXY
```

首个稳定模式优先支持 **Same-LAN Manual Gateway**：主路由 DHCP 保持开启，只让指定客户端
手工把 IPv4 网关和 DNS 指向 OpenSurge。DHCP 全局接管与下游 IPv6 接管不进入第一稳定版。

## Phase 1 已实现

当前 PR/分支已经完成或正在验证的基础层：

- 保留并注明上游来源、完整 Git 历史与 `GPL-3.0-only`；
- 删除 macOS 菜单栏 App、PKG、公证、launchd helper、pf、macOS BPF IPv6 等运行时；
- 增加 `platform.NetworkBackend`，把业务生命周期与 Linux 网络命令解耦；
- Linux IPv4 backend：`nftables + iproute2 + /dev/net/tun`；
- mihomo `auto-route` 在 QNAP/Linux 路径被明确禁止，策略路由由 OpenSurge 单一管理；
- `nftables` 只允许操作指定 OpenSurge 表，禁止 `nft flush ruleset`；
- 启动前检查 nft 表、fwmark、route table、rule priority 冲突，不能证明 ownership 就拒绝启动；
- policy route 使用结构化 `ip -j` 解析和精确删除，不再 `flush` 整个 routing table；
- runtime state 持久化完整 NAT/routing cleanup recipe；
- Stop / rollback / interrupted recovery 不依赖进程内 backend 内存，可由 fresh backend 清理；
- 网络修改使用 write-ahead cleanup journal，降低“内核已修改但 state 尚未落盘”的崩溃窗口；
- snapshot 绑定 Linux network namespace：隔离容器重启后不会把旧 namespace 的状态错误恢复到新 namespace；
- state 原子写入后 `fsync` 文件与父目录；
- 配置增加 `Normalize → Validate`，旧配置缺失的 `lan_cidr` / rule priority 会被实际写入运行配置；
- 第一组 Linux/QNAP 生命周期回归测试与最小 GitHub Actions Go CI。

## 当前明确不支持 / 尚未完成

Phase 1 **不能当作最终产品使用**。以下内容仍未完成：

- 正式 `Dockerfile` / Compose / QNAP Container Station 部署方案；
- Web 远程登录认证、Session、CSRF、登录限流；
- Web UI 全面去除 macOS 文案并完成 QNAP 网络设置流程；
- watchdog、bounded restart、完整 reconciliation 状态机；
- Linux network namespace 三段式实验室（client → gateway → upstream）；
- QNAP 真机验证、NAS reboot 验证、24h/72h soak test；
- Docker healthcheck、诊断包、日志轮转与 secret redaction 完整产品化；
- Docker release SBOM / provenance / 第三方许可证自动核验；
- DHCP takeover；
- 下游 IPv6 takeover。

在这些门槛完成前，不应发布“stable”标签。

## 网络安全边界

1. **不清空宿主机全局防火墙。** 禁止 `nft flush ruleset`。
2. **不因为 ID 很少见就视为 ownership。** nft table、fwmark、route table 和 rule priority 必须先验证无冲突。
3. **Stop 必须跨进程可恢复。** 清理依据来自持久化 snapshot，不来自 Go 对象内存。
4. **删除必须精确。** 不清空整个 routing table，只删除本次 recipe 对应的 rule/routes。
5. **配置错误不得破坏上一份可用网络状态。** 高风险改动必须可验证、可回滚。
6. **容器 namespace 是恢复边界。** 新 namespace 不重放旧 namespace 的网络快照。

## 开发与验证

普通测试不会主动修改宿主机网络：

```bash
go test ./...
go vet ./...
```

真实 Linux 网络测试必须显式启用，并且只应在 disposable container / network namespace 中运行：

```bash
OPEN_SURGE_NETWORK_TESTS=1 go test ./internal/platform/linux/
```

不要在正在承担家庭网络或 NAS 管理网络的宿主 namespace 上运行高风险集成测试。

## 路线

### Phase 1 — Linux 数据面基础

平台抽象、ownership、事务恢复、配置迁移保护、基础回归测试。

### Phase 2 — Docker + Web 产品化

正式 Docker/Compose、持久化目录、LAN Web 认证、健康检查、诊断和 namespace lab。

### Phase 3 — QNAP 真机稳定性

Container Station / Virtual Switch 拓扑验证、故障注入、NAS reboot、24h/72h soak、性能与日志容量测试。

### Phase 4 — 可选扩展

稳定版之后再评估 DHCP takeover、IPv6、更广泛 Linux NAS 兼容。

## 上游同步

不会自动 merge 上游 macOS 运行时代码。优先人工挑选平台无关的：

- mihomo profile / provider / policy 修复；
- Web UI 改进；
- 配置解析与设备策略修复；
- 测试与文档改进。

详细策略见 [docs/UPSTREAM.md](docs/UPSTREAM.md)。

## License

本项目继续使用 **GPL-3.0-only**。上游 `LICENSE`、版权历史与第三方许可证说明均应保留。
分发未来的 Docker 二进制/镜像时，还必须确保实际随镜像发布的 GPL 组件具有对应源码获取方式，
并让第三方 notice、SBOM 和实际构建版本保持一致。
