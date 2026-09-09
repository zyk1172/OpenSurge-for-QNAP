# OpenSurge for QNAP

OpenSurge for QNAP 是面向 QNAP NAS 的单容器透明代理网关。它把 Web 管理、mihomo、DNS、TUN、策略路由和持久化恢复集中在一个 Docker 容器中，通过 QNAP QNET 获得独立局域网 IPv4。

当前项目处于 **QNAP 真机稳定化 / 测试版阶段**。默认目标是 IPv4 Same-LAN Manual Gateway：主路由 DHCP 保持不变，只让需要代理的客户端把 IPv4 网关和 DNS 指向 OpenSurge。

> 当前测试镜像仅用于验证，不是 stable release。

## 当前架构

```text
局域网客户端
 Gateway / DNS = OpenSurge IP
        │
        ▼
QNAP QNET 独立 LAN IP
┌──────────────────────────────┐
│ opensurge                    │
│                              │
│ Web UI :8080                 │
│ Control API（loopback）      │
│ mihomo                       │
│ dnsmasq                      │
│ TUN                          │
│ iproute2 policy routing      │
│ /data 持久化                │
└──────────────────────────────┘
        │
        ├─ DIRECT
        └─ PROXY
```

默认部署只有一个产品容器：

```text
opensurge
```

不需要 Manager / Orchestrator，也不把 Docker Socket 暴露给 Web。

### QNAP same-LAN 数据面

在支持 TUN 和 Linux policy routing、但缺少 `nf_tables` 的 QNAP 内核上，OpenSurge 使用 ingress-interface 路由：

```text
eth0 ingress
   ↓
ip rule iif eth0
   ↓
OpenSurge dedicated routing table
   ↓
tun0
   ↓
mihomo
```

因此当前 same-LAN QNET 模式 **不要求 nftables**。TCP 和 UDP 都通过 TUN 数据面处理。

对需要 NAT 的隔离下游网络，仍保留 nftables/fwmark 后端及严格能力检查。

## 测试镜像

最新 rolling test release：

<https://github.com/zyk1172/OpenSurge-for-QNAP/releases/tag/qnap-test-latest>

QNAP TS-264C / x86_64 使用：

```text
OpenSurge-for-QNAP-test-amd64.tar.gz
```

导入：

```sh
gzip -dc OpenSurge-for-QNAP-test-amd64.tar.gz | docker load
```

镜像标签：

```text
opensurge-for-qnap:test
```

测试镜像由 GitHub Actions 构建。NAS 不需要安装 Go、Node.js、pnpm、gcc 或项目编译依赖，也不应该承担镜像构建任务。

## QNAP 部署

完整指南：

- [中文部署指南](deploy/qnap/README.zh-CN.md)
- [English deployment guide](deploy/qnap/README.md)
- [持久化说明](deploy/qnap/PERSISTENCE.md)
- [默认 Compose](deploy/qnap/docker-compose.yml)

创建容器时需要确定：

| 参数 | 含义 |
|---|---|
| `OPENSURGE_PARENT_INTERFACE` | QNAP QNET 父网卡 / Virtual Switch |
| `OPENSURGE_IP` | OpenSurge 独立 IPv4 |
| `OPENSURGE_SUBNET` | LAN CIDR |
| `OPENSURGE_GATEWAY` | 上游主路由 IPv4 |
| `OPENSURGE_DATA_PATH` | QNAP 持久化目录 |

物理网卡、QNET、静态 IP、CIDR 和上游网关属于 **容器创建参数**。运行中的 Web 不修改 QTS Network & Virtual Switch。

容器内部接口通常仍为 `eth0`；它与 QNAP 宿主上的 `eth1`、`br0`、bond 或 Virtual Switch 名称不是同一个概念。

## Web 管理

容器启动后访问：

```text
http://<OpenSurge-IP>:8080
```

QNAP Web 当前提供：

- 首次管理员创建与登录；
- 网关启动、停止和异常状态恢复；
- 容器实际网络状态；
- DNS / TUN 可变运行参数；
- HTTPS 订阅和本地 YAML 导入；
- 草稿、当前运行版本和下次启动版本持久化；
- Provider / 策略组 / 规则；
- 设备策略；
- 连接与流量状态；
- Doctor、日志和生命周期操作记录。

QNAP build 只呈现 NAS 相关功能，不把桌面系统专属控制项作为 QNAP 功能暴露。

## 配置与持久化

推荐：

```text
/share/Container/opensurge -> /data
```

主要内容：

```text
/data/config      主配置
/data/control     管理员和控制状态
/data/profiles    导入/托管 profile
/data/providers   provider 数据
/data/state       持久功能状态
/data/backups     配置备份
/data/runtime     网络 ownership / crash recovery journal
/data/logs        运行日志
```

同一台 NAS 更新或重建容器时保留整个 `/data`。已有 `/data/config/opensurge.yaml` 不会被新的首次启动 seed 自动覆盖。

## 当前 QNAP 真机兼容性

已经在 QNAP 5.10.60-qnap x86_64 环境中确认：

- TUN 驱动可用；
- `/dev/net/tun` 可映射到容器；
- `NET_ADMIN` / `NET_RAW` 可用；
- `iproute2` 可用；
- 该内核缺少 `nf_tables` netlink 支持；
- 手动 mihomo HTTP / SOCKS5 / DNS 路径可用。

针对缺少 `nf_tables` 的 same-LAN QNAP，项目已经加入 nft-free TUN ingress-interface policy routing，并有独立 TCP/UDP namespace CI。

仍需继续完成真实客户端、重启和长期运行验证。

## 网络安全边界

OpenSurge 的 QNAP 部署遵守以下约束：

1. 不执行 `nft flush ruleset`。
2. 不清空宿主 routing table。
3. 不默认使用 `privileged: true`。
4. 不使用 host network 作为 Gateway 数据面。
5. 不把 Docker Socket 提供给 LAN-facing Web。
6. 网络对象必须通过 ownership / snapshot / journal 精确恢复。
7. 容器重建后的新 network namespace 不直接重放旧 namespace 的内核状态。
8. 正常 QNAP Web 操作不修改 QTS 默认网关、DNS、DHCP 或 Virtual Switch。

## 当前范围

第一稳定版重点：

- 单容器 QNAP Docker；
- QNET 独立 LAN IP；
- IPv4 same-LAN manual gateway；
- TUN 透明代理；
- DNS；
- 订阅 / Provider / 策略 / 设备管理；
- 持久化和容器重建恢复。

暂不作为第一稳定版目标：

- 自动接管主路由 DHCP；
- 下游 IPv6 takeover；
- 自动修改 QNAP Network & Virtual Switch；
- QPKG；
- 全自动家庭网络迁移。

## Stable 前仍需完成

- 一台真实客户端的 TCP / UDP / QUIC 端到端验证；
- QNAP reboot 后恢复；
- 24h / 72h soak；
- 长时间 CPU、内存、FD、日志容量观察；
- 更新 / 回滚演练；
- SBOM、provenance、release checksums 与第三方许可证核对；
- stable 镜像发布流程。

## 开发与验证

普通测试：

```sh
go test ./...
go vet ./...
```

Web：

```sh
cd web
pnpm install --frozen-lockfile
pnpm test
OPENSURGE_TARGET=qnap pnpm build
```

Linux namespace 网络实验：

```sh
make lab-test-linux
```

不要在承担 NAS 管理网络或家庭主网关职责的宿主 namespace 中直接运行高风险网络集成测试。

## 上游来源与许可证

OpenSurge for QNAP 基于 [OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac) 派生，并在此基础上进行 QNAP/Linux 产品化改造。本项目不是上游作者官方提供或背书的 QNAP 版本。

- 上游项目：OpenSurge for Mac
- 上游作者 / 组织：YTwsy
- Fork baseline：`b03bf2f8a2b02a6fffba9c879ce1980ddb831a67`（v0.2.2）
- Fork 日期：2026-09-09
- License：`GPL-3.0-only`
- 来源关系：[docs/UPSTREAM.md](docs/UPSTREAM.md)
- Attribution / notices：[NOTICE.md](NOTICE.md)、[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)

上游版权历史、`LICENSE` 和第三方许可证信息继续保留。平台无关的上游修复可以人工挑选合入，但不会自动把 macOS 运行时重新并入 QNAP 默认路径。
