# OpenSurge for QNAP

OpenSurge for QNAP 是面向 QNAP NAS 的单容器 IPv4 透明代理网关。它把 Web 管理、mihomo、DNS、TUN、策略路由和持久化恢复集中在一个 Docker 容器中，通过 QNAP QNET 获得独立局域网 IPv4。

## 稳定版范围

`v1.0.x` 的稳定支持边界是 **IPv4 Same-LAN Manual Gateway**：主路由 DHCP 保持不变，需要经过 OpenSurge 的客户端将 IPv4 网关和 DNS 指向 OpenSurge。

同时提供可选的 **NAS Host Takeover**，让 QNAP 宿主自身的 IPv4 公网流量通过 OpenSurge。该功能默认不授予宿主 namespace 权限，需要显式叠加高权限 Compose override。

暂不包含：

- IPv6 takeover；
- 自动修改主路由 DHCP；
- 自动修改 QTS Network & Virtual Switch；
- QPKG；
- 全自动家庭网络迁移。

## 架构

```text
局域网客户端
 Gateway / DNS = OpenSurge IP
        │
        ▼
QNAP QNET 独立 LAN IP
┌──────────────────────────────┐
│ opensurge                    │
│ Web UI :8080                 │
│ Control API（loopback）      │
│ mihomo + dnsmasq             │
│ TUN + policy routing         │
│ /data 持久化                │
└──────────────────────────────┘
        │
        ├─ DIRECT
        └─ PROXY
```

默认只有一个产品容器，不需要 Manager / Orchestrator，也不把 Docker Socket 暴露给 Web。

## 正式镜像

Docker Hub：

```text
zyk1172/opensurge-for-qnap:1.0.0
zyk1172/opensurge-for-qnap:latest
```

默认 Compose 使用固定稳定版：

```text
deploy/qnap/docker-compose.yml
```

也可以覆盖镜像：

```sh
export OPENSURGE_IMAGE=zyk1172/opensurge-for-qnap:1.0.0
```

GitHub Release 同时提供 amd64 / arm64 Docker archive、SPDX JSON SBOM、`SHA256SUMS`、build provenance 和 SBOM attestation。

Rolling 测试镜像仍保留在：

<https://github.com/zyk1172/OpenSurge-for-QNAP/releases/tag/qnap-test-latest>

## QNAP 部署

完整指南：

- [中文部署指南](deploy/qnap/README.zh-CN.md)
- [English deployment guide](deploy/qnap/README.md)
- [持久化说明](deploy/qnap/PERSISTENCE.md)
- [NAS Host Takeover](docs/QNAP_NAS_HOST_TAKEOVER.zh-CN.md)

创建容器时需要确定：

| 参数 | 含义 |
|---|---|
| `OPENSURGE_PARENT_INTERFACE` | QNAP QNET 父网卡 / Virtual Switch |
| `OPENSURGE_IP` | OpenSurge 独立 IPv4 |
| `OPENSURGE_SUBNET` | LAN CIDR |
| `OPENSURGE_GATEWAY` | 上游主路由 IPv4 |
| `OPENSURGE_DATA_PATH` | QNAP 持久化目录 |

推荐：

```text
/share/Container/opensurge -> /data
```

已有 `/data/config/opensurge.yaml` 不会被新的首次启动 seed 覆盖。

### 默认最小权限部署

默认 Compose 只需要：

```text
NET_ADMIN
NET_RAW
/dev/net/tun
```

不会挂载宿主网络 namespace，也不会授予 `SYS_ADMIN`。

### 可选 NAS Host Takeover

需要让 NAS 本机也通过 OpenSurge 时：

```sh
docker compose \
  -f deploy/qnap/docker-compose.yml \
  -f deploy/qnap/docker-compose.host-takeover.yml \
  up -d
```

该 override 才会增加：

```text
CAP_SYS_ADMIN
/proc/1/ns/net -> /run/opensurge/host-netns:ro
```

QTS 的原始默认网关不会被替换。OpenSurge 使用独立 RPDB/table 接管本机 IPv4，并在网关不 ready 时自动释放规则回到 QTS 原路径。

## Same-LAN 数据面

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

same-LAN QNET 模式不要求 nftables。需要 NAT 的隔离下游网络仍使用 nftables/fwmark 后端并进行能力检查。

## Web 管理

容器启动后访问：

```text
http://<OpenSurge-IP>:8080
```

QNAP Web 提供：

- 首次管理员创建与登录；
- 网关启动、停止和异常恢复；
- DNS / TUN 配置；
- HTTPS 订阅和本地 YAML；
- Provider / 策略组 / 规则；
- 设备策略；
- 连接、流量和规则分析；
- Doctor、日志、生命周期状态；
- 可选 NAS Host Takeover。

Control 进程保持 root 以管理容器网络，只监听 loopback；LAN-facing Web 进程使用非特权 UID/GID 并清空 capability。

## 安全与发布供应链

- 首次管理员创建需要一次性 bootstrap token；
- TUN 模式下 Mihomo mixed 端口和内部 DNS 只监听 loopback；
- 远程订阅只允许 HTTPS，并拒绝私网、loopback、link-local、CGNAT/Tailscale 和保留目标；
- 不执行全局 `ip rule flush`、`ip route flush` 或 `nft flush ruleset`；
- 发布构建固定高权限 GitHub Actions 到 commit SHA；
- stable release 生成 SPDX SBOM、SHA256 校验、provenance 和 SBOM attestation；
- Go 依赖和最终容器镜像进入自动漏洞扫描 gate。

## 已知边界

- QNAP 产品边界为 IPv4-only；
- ARM64 镜像可构建并发布，但不同 QNAP ARM 型号仍可能存在 QNET/内核差异；
- NAS Host Takeover 会根据 QTS 当前 RPDB 动态选择未占用 priority，不依赖固定 `24090/24110`；
- `SIGKILL` 或宿主突然断电无法执行退出清理，可能在容器重启和协调前造成短暂网络中断；QTS 原始默认路由本身不会被删除。

## 开发与验证

```sh
go test ./...
go vet ./...

cd web
pnpm install --frozen-lockfile
pnpm test
OPENSURGE_TARGET=qnap pnpm build
```

不要在承担 NAS 管理网络或家庭主网关职责的宿主 namespace 中直接运行高风险网络集成测试。

## 上游来源与许可证

OpenSurge for QNAP 基于 [OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac) 派生。本项目不是上游作者官方提供或背书的 QNAP 版本。

- Fork baseline：`b03bf2f8a2b02a6fffba9c879ce1980ddb831a67`（v0.2.2）
- License：`GPL-3.0-only`
- 来源关系：[docs/UPSTREAM.md](docs/UPSTREAM.md)
- Attribution / notices：[NOTICE.md](NOTICE.md)、[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)
