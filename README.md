# OpenSurge for QNAP

> **当前状态：QNAP Docker 部署适配 / 稳定化阶段。**
> 已具备 Linux/QNAP 数据面、LAN Web 管理、QNET Compose、双架构镜像构建和故障恢复测试；尚未发布正式 stable registry image，真实 QNAP 长时间 soak / reboot 验证仍需完成。

OpenSurge for QNAP 是基于
[OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac) 的派生项目，目标是把上游的 Web 控制面、mihomo 配置/策略能力和网关生命周期移植为适合 QNAP NAS 与通用 Linux Docker 长期运行的透明代理网关。

本项目不是上游作者官方提供的 QNAP 版本，也未获得上游背书，除非上游作者另有明确声明。

- 原始项目：OpenSurge for Mac
- 原作者 / 组织：YTwsy
- 上游仓库：<https://github.com/YTwsy/OpenSurge-for-Mac>
- Fork baseline：`b03bf2f8a2b02a6fffba9c879ce1980ddb831a67`（v0.2.2）
- Fork 日期：2026-09-09
- 许可证：`GPL-3.0-only`，继承自上游且未修改
- 详细来源关系：[docs/UPSTREAM.md](docs/UPSTREAM.md)
- 许可证/来源说明：[NOTICE.md](NOTICE.md)、[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)

## QNAP 部署入口

- 中文完整部署指南：[deploy/qnap/README.zh-CN.md](deploy/qnap/README.zh-CN.md)
- English deployment guide: [deploy/qnap/README.md](deploy/qnap/README.md)
- 持久化数据模型：[deploy/qnap/PERSISTENCE.md](deploy/qnap/PERSISTENCE.md)
- QNAP Compose：[deploy/qnap/docker-compose.yml](deploy/qnap/docker-compose.yml)
- 环境变量模板：[deploy/qnap/.env.example](deploy/qnap/.env.example)

## 产品目标

首个稳定拓扑：

```text
LAN Client
   │
   │ Gateway / DNS = OpenSurge container
   ▼
QNAP NAS
└─ OpenSurge Docker container (QNET 独立 LAN IP)
   ├─ Authenticated Web UI
   ├─ loopback privileged Control API
   ├─ mihomo TUN
   ├─ DNS / dnsmasq
   ├─ nftables（仅 OpenSurge 自有 table）
   └─ iproute2 policy routing
          │
          ├─ DIRECT
          └─ PROXY
```

首个稳定模式优先支持 **Same-LAN Manual Gateway**：主路由 DHCP 保持开启，只让指定客户端手工把 IPv4 网关和 DNS 指向 OpenSurge。DHCP 全局接管与下游 IPv6 接管不进入第一稳定版。

## 当前已经实现

### Linux/QNAP 数据面

- `platform.NetworkBackend` 平台边界；
- Linux IPv4 backend：`nftables + iproute2 + /dev/net/tun`；
- mihomo `auto-route` 在 QNAP/Linux 路径明确禁用，策略路由由 OpenSurge 单一管理；
- nftables 只管理自己的 table，禁止 `nft flush ruleset`；
- nft table / fwmark / route table / rule priority ownership 冲突检测；
- policy route 结构化读取与精确删除，不 flush 整张 routing table；
- write-ahead cleanup journal；
- network snapshot + rollback；
- runtime state 绑定 host boot session + Linux network namespace；
- fresh-process Stop / interrupted recovery 不依赖进程内 backend 状态。

### Docker / Container Station

- 正式多阶段 `docker/Dockerfile`；
- `linux/amd64` 与 `linux/arm64` CI 构建；
- mihomo / dnsmasq 固定版本与校验值；
- `/dev/net/tun` + `NET_ADMIN` + `NET_RAW`；
- 不默认使用 `privileged: true`；
- 不使用 host network；
- QNAP QNET 静态 LAN IP Compose；
- `no-new-privileges`；
- Docker 日志轮转；
- liveness/readiness smoke；
- 容器 recreate / stale runtime reconciliation 测试。

### Web 管理

- LAN-facing authenticated Web Gateway；
- privileged Control API 继续仅监听 loopback；
- 首次管理员创建；
- Argon2id；
- Session / HttpOnly / SameSite；
- Origin/Host 防护；
- 登录限流；
- QNAP production Web build；
- 容器 Linux network discovery；
- QNAP 路径禁止执行 macOS 主机网络改写。

### 故障恢复

- mihomo bounded recovery；
- dnsmasq bounded recovery；
- PID + fingerprint 防 PID reuse；
- dnsmasq 实际 SIGKILL fault injection；
- 容器重建后 stale PID 不会被错误 signal；
- Linux namespace lab：`client -> gateway -> upstream`；
- 验证测试结束后宿主 namespace 无 OpenSurge 网络污染。

### QNAP 部署适配

- QNAP preflight；
- 双网卡 NAS 的 QNET 父接口显式选择：`OPENSURGE_PARENT_INTERFACE`；
- `--list-interfaces` / `make qnap-interfaces`；
- QNAP 持久化路径要求使用专用绝对路径；
- 整块 `/data` bind mount，保留 crash-reconciliation journal；
- Compose 的 IP / CIDR / upstream gateway 会用于**首次**配置 seed；
- 已存在的 `/data/config/opensurge.yaml` 永不被部署变量覆盖；
- 中文 QNAP Docker 部署文档和持久化说明。

## 持久化原则

推荐：

```text
/share/Container/opensurge -> /data
```

关键内容：

```text
/data/config      主配置
/data/control     管理员凭据、内部控制状态
/data/profiles    导入/托管 profile
/data/providers   provider / rule-provider
/data/state       持久功能状态
/data/backups     配置备份
/data/runtime     crash/reconciliation ownership journal
/data/logs        运行日志
```

同一台 NAS 升级/重建容器时保留完整 `/data`。迁移到另一台 NAS 时，`runtime/` 不应作为普通可迁移配置原样恢复。详见 [deploy/qnap/PERSISTENCE.md](deploy/qnap/PERSISTENCE.md)。

## 双网卡 QNAP

需要区分两种接口：

```text
QNAP host interface     OPENSURGE_PARENT_INTERFACE = eth1 / br0 / ...
       │
       │ QNET
       ▼
container interface     OPENSURGE_CONTAINER_INTERFACE = eth0（通常）
```

双网卡 NAS 真正的“选择哪块物理/桥接网卡”发生在 QNET 父接口，而不是容器内部 `eth0`。

查看候选接口：

```bash
make qnap-interfaces
```

或：

```bash
cd deploy/qnap
sh ./preflight.sh --list-interfaces
```

以 QNAP Network & Virtual Switch 的实际拓扑为准，不要仅根据 `eth0/eth1` 数字猜物理端口。

## 网络安全边界

1. **不清空宿主机全局防火墙。** 禁止 `nft flush ruleset`。
2. **不能证明 ownership 就拒绝接管。** nft table、fwmark、route table、rule priority 都要做冲突检查。
3. **Stop 必须跨进程/跨容器恢复。** 清理依据来自持久化 runtime snapshot/journal。
4. **删除必须精确。** 不清空整个 routing table。
5. **Web UI 不获得 QNAP Docker socket。** QNET 父网卡属于部署参数，不通过 Web 修改 QTS/Container Station 网络。
6. **不默认 host network / privileged。** 当前目标权限为 `NET_ADMIN + NET_RAW + /dev/net/tun`。
7. **容器 namespace 是恢复边界。** 新 namespace 不重放旧 namespace 网络快照。

## 开发与验证

普通测试不会主动修改宿主网络：

```bash
go test ./...
go vet ./...
```

Web：

```bash
cd web
pnpm install --frozen-lockfile
pnpm test
OPENSURGE_TARGET=qnap pnpm build
```

Linux 网络实验室：

```bash
make lab-test-linux
```

QNAP Compose 静态检查：

```bash
make compose-qnap-check
make qnap-preflight-static
```

QNAP 实机部署前：

```bash
make qnap-interfaces
make qnap-preflight
```

不要在正在承担家庭网络或 NAS 管理网络的宿主 namespace 直接运行高风险网络集成测试。

## 当前尚未完成

在发布 `stable` 前仍需完成：

- 真实 QNAP NAS 端到端部署验证；
- NAS reboot 后完整恢复验证；
- 真实双网卡/QNET 拓扑验证；
- 24h / 72h soak；
- 性能、FD、内存、日志容量长期观察；
- 正式 registry image 发布流程；
- SBOM / provenance / release checksums；
- 生产升级/回滚演练。

第一稳定版仍明确不做：

- 全 LAN DHCP takeover；
- downstream IPv6 takeover；
- QNAP Network & Virtual Switch 自动改写；
- Docker socket 管理；
- QPKG。

## 路线

### 已完成基础阶段

- Linux 数据面抽象与事务恢复；
- Docker + Web 产品化；
- QNAP Web/UI 边界；
- dnsmasq/mihomo bounded recovery；
- container-recreate runtime reconciliation；
- QNAP deployment hardening。

### 当前阶段 — Docker/QNAP deployment adaptation

- 持久化数据模型；
- 双网卡选择；
- first-run Compose network seed；
- 完整部署文档；
- 部署 CI 门禁。

### 下一阶段 — QNAP 真机稳定性

- Container Station/QNET 真机；
- reboot；
- fault injection；
- 24h/72h soak；
- 性能与存储写入评估。

### 稳定版之后

再评估 DHCP takeover、IPv6 和更广泛 Linux NAS 兼容。

## 上游同步

不会自动 merge 上游 macOS 运行时代码。优先人工挑选平台无关的：

- mihomo profile / provider / policy 修复；
- Web UI 改进；
- 配置解析与设备策略修复；
- 测试与文档改进。

详细策略见 [docs/UPSTREAM.md](docs/UPSTREAM.md)。

## License

本项目继续使用 **GPL-3.0-only**。上游 `LICENSE`、版权历史与第三方许可证说明均保留。
分发 Docker 二进制/镜像时，还必须确保实际随镜像发布的 GPL 组件具有对应源码获取方式，并让第三方 notice、SBOM 和实际构建版本保持一致。
