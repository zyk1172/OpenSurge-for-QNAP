# OpenSurge for QNAP — 单容器 Docker 部署

OpenSurge for QNAP `v1.0.x` 的稳定支持范围是 **IPv4 Same-LAN Manual Gateway**。主路由 DHCP 保持不变，只让需要代理的客户端把 IPv4 网关和 DNS 指向 OpenSurge。

## 1. 正式镜像

Docker Hub：

```text
zyk1172/opensurge-for-qnap:1.0.0
zyk1172/opensurge-for-qnap:latest
```

默认 Compose 已固定到 `1.0.0`：

```text
deploy/qnap/docker-compose.yml
```

如需覆盖镜像：

```sh
export OPENSURGE_IMAGE=zyk1172/opensurge-for-qnap:1.0.0
```

GitHub Release 还提供 amd64 / arm64 Docker archive、SPDX JSON SBOM、`SHA256SUMS`、build provenance 与 SBOM attestation。

## 2. 架构与默认权限

```text
QNAP 物理网卡 / Virtual Switch
          │
          │ qnet
          ▼
┌───────────────────────────────┐
│ opensurge                     │
│ Web :8080                     │
│ Control API loopback          │
│ mihomo + dnsmasq              │
│ TUN + policy routing          │
│ /data 持久化                  │
└───────────────────────────────┘
          │
          ▼
局域网客户端 Gateway/DNS = OpenSurge IP
```

默认部署只有一个容器，不挂载 Docker Socket，不使用 `privileged: true`，也不修改 QTS Network & Virtual Switch。

默认只授予：

```text
NET_ADMIN
NET_RAW
/dev/net/tun
```

**默认不会授予 `SYS_ADMIN`，也不会挂载 QNAP host network namespace。**

## 3. 创建参数

| 参数 | 含义 | 示例 |
|---|---|---|
| `OPENSURGE_PARENT_INTERFACE` | QNAP QNET 父网卡 / Virtual Switch | `eth1` / `br0` |
| `OPENSURGE_IP` | OpenSurge 独立 IPv4 | `192.168.2.241` |
| `OPENSURGE_SUBNET` | LAN CIDR | `192.168.2.0/24` |
| `OPENSURGE_GATEWAY` | 上游主路由 IPv4 | `192.168.2.1` |
| `OPENSURGE_DATA_PATH` | 持久化目录 | `/share/Container/opensurge` |
| `OPENSURGE_WEB_UID` | Web 降权进程 UID | `1000` |
| `OPENSURGE_WEB_GID` | Web 降权进程 GID | `100` |

双网卡 NAS 不要只根据 `eth0/eth1` 数字猜物理口。结合 QTS「网络与虚拟交换机」与宿主 `ip addr` / `ip route` 确认实际父接口。

QNAP UID/GID 也不要硬猜：

```sh
id <你的QNAP用户名>
```

不要使用递归 `chmod 777` 掩盖 QTS ACL 问题。

## 4. 预检

从 `deploy/qnap` 执行：

```sh
cp .env.example .env
# 修改 .env
sh ./preflight.sh
```

预检会检查 Docker / Compose / qnet / `/dev/net/tun`、父接口与 IPv4 拓扑、真实 `/share/...` bind mount，以及 Web UID/GID 对 `/data/web-auth` 的写入语义。

## 5. 启动默认旁路由

```sh
export OPENSURGE_PARENT_INTERFACE=br0
export OPENSURGE_IP=192.168.2.241
export OPENSURGE_SUBNET=192.168.2.0/24
export OPENSURGE_GATEWAY=192.168.2.1
export OPENSURGE_DATA_PATH=/share/Container/opensurge
export OPENSURGE_WEB_UID=1000
export OPENSURGE_WEB_GID=100

docker compose -f docker-compose.yml config
docker compose -f docker-compose.yml up -d
```

正常部署不需要在 NAS 上执行 `docker compose build`。

## 6. 可选：让 NAS 本身通过 OpenSurge

NAS Host Takeover 需要进入 QNAP host network namespace，因此被拆成独立高权限 override：

```text
deploy/qnap/docker-compose.host-takeover.yml
```

只有需要该功能时才运行：

```sh
docker compose \
  -f docker-compose.yml \
  -f docker-compose.host-takeover.yml \
  up -d
```

它会额外增加：

```text
CAP_SYS_ADMIN
/proc/1/ns/net:/run/opensurge/host-netns:ro
```

然后可在 Web UI 中启用 **“让 NAS 使用 OpenSurge”**。

OpenSurge 不替换 QTS 默认网关；它使用独立 table `20242` 和动态 RPDB priority 接管 NAS 本机 IPv4。QTS 存在 `from <NAS-IP>`、包含 NAS IP 的 CIDR 或 `from all` policy 时，OpenSurge 会把自己的 priority block 放在相关规则之前；冲突或实际 route lookup 未命中会拒绝启用并回滚。

详见 [`../../docs/QNAP_NAS_HOST_TAKEOVER.zh-CN.md`](../../docs/QNAP_NAS_HOST_TAKEOVER.zh-CN.md)。

## 7. 第一次启动与持久化

推荐：

```text
/share/Container/opensurge -> /data
```

第一次启动且 `/data/config/opensurge.yaml` 不存在时，entrypoint 使用创建参数生成初始配置。已有持久化配置不会被新的 seed 覆盖。

主要目录：

```text
/data/config
/data/control
/data/web-auth
/data/profiles
/data/providers
/data/state
/data/backups
/data/runtime
/data/logs
```

同一台 NAS 更新或重建容器时保留整个 `/data`。

## 8. Web 管理

访问：

```text
http://<OpenSurge-IP>:8080
```

首次管理员创建需要容器日志中打印的一次性 bootstrap token。创建成功后 token 会失效并删除。

QNAP Web 提供网关生命周期、DNS/TUN、订阅、Provider、策略、设备规则、连接/流量分析、Doctor、日志，以及可选 NAS Host Takeover。

## 9. Same-LAN 数据面

当前 QNAP same-LAN 模式使用：

```text
eth0 ingress
   ↓
ip rule iif eth0
   ↓
OpenSurge 专用 routing table
   ↓
tun0
   ↓
mihomo
```

该路径不依赖 nftables，因此适用于部分缺少 `nf_tables` netlink 支持、但 TUN 和 policy routing 可用的 QNAP 内核。

需要 NAT 的隔离下游拓扑仍使用 nftables/fwmark 后端。

## 10. 基础检查

```sh
docker ps --filter name=opensurge
docker inspect --format '{{json .State.Health}}' opensurge
docker exec opensurge ip -br addr
docker exec opensurge ip route
docker exec opensurge ip rule
docker exec opensurge ls -l /dev/net/tun
```

same-LAN Gateway 启动后：

```sh
docker exec opensurge ip rule show
docker exec opensurge ip route show table 20241
```

缺少 `nf_tables` 时 `nft list ruleset` 失败不代表 same-LAN Gateway 失败。

## 11. 客户端设置

先选择一台客户端：

```text
IPv4 Gateway = OpenSurge IP
DNS          = OpenSurge IP
```

再逐步迁移其他设备，不要一开始修改全网 DHCP。

## 12. 当前边界

- QNAP 产品边界为 IPv4-only；
- 不自动修改主路由 DHCP；
- 不自动修改 QTS Network & Virtual Switch；
- 修改 QNET 父接口、静态 IP、CIDR、主路由需要重建容器；
- ARM64 镜像会正式发布，但不同 QNAP ARM 型号仍可能存在 QNET/内核差异；
- 强制 `SIGKILL` 或宿主突然断电无法执行退出清理，NAS Host Takeover 可能在容器恢复协调前出现短暂网络中断。
