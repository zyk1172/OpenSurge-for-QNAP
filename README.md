# OpenSurge for QNAP

OpenSurge for QNAP 是面向 QNAP NAS 的单容器透明代理网关。它把 Web 管理、mihomo、DNS、TUN、策略路由和持久化恢复集中在一个 Docker 容器中，通过 QNAP QNET 获得独立局域网 IPv4。

当前项目处于 **QNAP 真机稳定化 / 测试版阶段**。默认目标仍是 IPv4 Same-LAN Manual Gateway：主路由 DHCP 保持不变，只让需要代理的客户端把 IPv4 网关和 DNS 指向 OpenSurge。QNAP same-LAN 另提供可选的 **IPv6 DNS / fake-IP 定向接管**：客户端继续使用主路由的公网 IPv6 和默认路由，只把 Mihomo fake IPv6 网段静态路由到 OpenSurge。

> 当前测试镜像仅用于验证，不是 stable release。

## 当前架构

```text
局域网客户端
 Gateway / DNS = OpenSurge IP（IPv4 manual gateway）
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

### QNAP same-LAN IPv4 数据面

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

因此当前 same-LAN QNET 的 IPv4 manual-gateway 模式 **不要求 nftables**。TCP 和 UDP 都通过 TUN 数据面处理。

对需要 NAT 的隔离下游网络，仍保留 nftables/fwmark 后端及严格能力检查。

### 可选 IPv6 DNS / fake-IP 定向接管

QNAP same-LAN 的 IPv6 不再要求客户端手工使用 OpenSurge ULA，也不让 OpenSurge 与主路由竞争 RA/default route。

```text
普通 IPv6：客户端 ──► 主路由 ──► Internet

DNS：客户端 ──► 主路由 DNS ──► OpenSurge DNS
                               │
                               └─ fake AAAA = fdfe:dcba:9876::/64
                                              │
                                    主路由 IPv6 静态路由
                                              ▼
                                          OpenSurge
                                              ▼
                                             TUN
```

主路由继续提供原来的 RA、DHCPv6/Bridge/Passthrough、公网 IPv6 和默认 IPv6 路由。只需要把 `fdfe:dcba:9876::/64` 静态路由到 Web 显示的 OpenSurge 稳定 link-local 下一跳。

OpenSurge Linux policy routing 也只匹配 fake IPv6 前缀，不再安装 `iif eth0 → IPv6 default dev tun0` 的全量接管规则。该路径以全局 Mihomo 规则为保证目标，不承诺逐设备 IPv6 策略；原有 IPv4 设备策略保持不变。

详见：

- [QNAP IPv6 DNS / fake-IP 定向接管](docs/QNAP_IPV6_DNS_FAKEIP.zh-CN.md)
- [QNAP IPv6 DNS / fake-IP steering (English)](docs/QNAP_IPV6_DNS_FAKEIP.md)

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
- [IPv6 DNS / fake-IP 指南](docs/QNAP_IPV6_DNS_FAKEIP.zh-CN.md)
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
- IPv6 DNS / fake-IP 定向接管配置与主路由静态路由参数提示；
- HTTPS 订阅和本地 YAML 导入；
- 高级全局附加配置中的 Hosts 文件导入、顶层 `hosts:` 映射，以及 `dns.use-hosts` / `dns.use-system-hosts` 控制；
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
- 手动 mihomo HTTP / SOCKS5 / DNS 路径可用；
- QNET IPv6 IPAM 不接受 IPv6 address pool，但容器 network namespace 可以显式启用 IPv6 并绑定 IPv6 地址。

针对缺少 `nf_tables` 的 same-LAN QNAP，项目已经加入 nft-free TUN policy routing。IPv6 fake-IP 路径还额外要求容器在 forwarding 开启时保持 `accept_ra=2`，以继续学习主路由的原生 IPv6 路由。

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
9. IPv6 DNS/fake-IP 模式只安装 `fdfe:dcba:9876::/64` 的容器内 IPv6 TUN 路由，不接管 LAN 的 `::/0` 默认路由。

## 当前范围

第一稳定版重点：

- 单容器 QNAP Docker；
- QNET 独立 LAN IP；
- IPv4 same-LAN manual gateway；
- TUN 透明代理；
- DNS；
- 订阅 / Provider / 策略 / 设备管理；
- 持久化和容器重建恢复。

实验性/继续验证：

- QNAP same-LAN IPv6 DNS / fake-IP 定向接管；
- 主路由 DNS + `fdfe:dcba:9876::/64` 静态路由；
- 保留客户端原生公网 IPv6 与主路由默认 IPv6 路由。

暂不作为第一稳定版目标：

- 自动接管主路由 DHCP；
- OpenSurge 发送 RA/SLAAC 或成为整个 LAN 的 IPv6 默认路由；
- 精确逐设备 IPv6 策略；
- 自动修改 QNAP Network & Virtual Switch；
- QPKG；
- 全自动家庭网络迁移。

## Stable 前仍需完成

- 一台真实客户端的 TCP / UDP / QUIC 端到端验证；
- IPv6 fake-IP 静态路由、DIRECT/PROXY/REJECT 与公网 IPv6 保留验证；
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
