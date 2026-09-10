# OpenSurge for QNAP — 单容器 Docker 部署

QNAP 默认部署只运行一个 `opensurge` 容器。物理网卡 / Virtual Switch、QNET、静态 IPv4、LAN CIDR、上游路由和持久化目录在创建容器时确定；运行后的 Web 负责 OpenSurge 自身配置，不修改 QTS 宿主网络。

当前稳定化范围是 **IPv4 Same-LAN Manual Gateway（旁路由）**：主路由 DHCP 保持原状，只让需要经过 OpenSurge 的客户端把 IPv4 网关和 DNS 指向 OpenSurge。

## 1. 架构

```text
QNAP 物理网卡 / Virtual Switch
          │
          │ qnet（创建容器时绑定）
          ▼
┌───────────────────────────────┐
│ opensurge                     │
│                               │
│ Web :8080                     │
│ Control API :61767 loopback   │
│ mihomo + dnsmasq              │
│ TUN + policy routing          │
│ /data 持久化                  │
└───────────────────────────────┘
          │
          ▼
局域网客户端把 Gateway/DNS 指向 OpenSurge IP
```

默认部署没有：

- Manager / Orchestrator 辅助容器；
- Docker Socket；
- `privileged: true`；
- 运行期自动修改 QTS / Virtual Switch 的逻辑。

Gateway 容器需要：

- `NET_ADMIN`；
- `NET_RAW`；
- `/dev/net/tun`。

### Same-LAN QNAP 的透明代理后端

对当前支持范围，OpenSurge 优先使用：

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

这条路径不依赖 nftables，因此适用于部分缺少 `nf_tables` netlink 支持、但 TUN 与 policy routing 正常的 QNAP 内核。

需要 NAT 的隔离下游拓扑仍使用 nftables/fwmark 后端，并保留严格能力检查。

## 2. 使用预构建测试镜像

NAS 不负责构建 Go、Web 前端、dnsmasq 或 mihomo。

最新 rolling test release：

<https://github.com/zyk1172/OpenSurge-for-QNAP/releases/tag/qnap-test-latest>

TS-264C / x86_64 使用：

```text
OpenSurge-for-QNAP-test-amd64.tar.gz
```

导入：

```sh
gzip -dc OpenSurge-for-QNAP-test-amd64.tar.gz | docker load
```

导入后的镜像标签：

```text
opensurge-for-qnap:test
```

默认 Compose 使用 `pull_policy: never`。如果本地测试镜像不存在，应明确报错，而不是静默拉取另一个同名镜像。

## 3. 创建容器前确定网络与权限参数

| 参数 | 含义 | 示例 |
|---|---|---|
| `OPENSURGE_PARENT_INTERFACE` | QNAP QNET 父网卡 / Virtual Switch | `eth1` / `br0` |
| `OPENSURGE_IP` | OpenSurge 独立 IPv4 | `192.168.2.241` |
| `OPENSURGE_SUBNET` | 当前 LAN CIDR | `192.168.2.0/24` |
| `OPENSURGE_GATEWAY` | 上游主路由 IPv4 | `192.168.2.1` |
| `OPENSURGE_DATA_PATH` | 持久化目录 | `/share/Container/opensurge` |
| `OPENSURGE_WEB_UID` | Web 降权进程 UID | `1000` |
| `OPENSURGE_WEB_GID` | Web 降权进程 GID | `100` |

双网卡 NAS 不要根据 `eth0/eth1` 数字猜物理口。应结合 QTS「网络与虚拟交换机」和宿主 `ip addr` / `ip route` 识别实际父接口。

容器内部数据接口通常仍是 `eth0`。宿主 QNET 父接口和容器接口名称不同是正常现象。

### QNAP 的 UID/GID 不要硬猜

QNAP 上经常能看到第一个普通用户 UID 为 `1000`，而 `everyone` 组常见 GID 为 `100`，所以项目默认值采用：

```text
OPENSURGE_WEB_UID=1000
OPENSURGE_WEB_GID=100
```

但这不是 QNAP 的统一强制值。实际部署必须以你的 NAS 为准：

```sh
id <你的QNAP用户名>
```

把输出中的数字 UID/GID 写入 `.env`。

不要为了省事执行：

```text
chmod -R 777
chown -R 1000:1000 /share/Container/opensurge
```

OpenSurge 不会递归改整个 `/data` 的所有权。只有 Web 专用目录 `/data/web-auth` 会匹配 `OPENSURGE_WEB_UID/GID`；其余配置、runtime、Provider 和恢复状态仍由 Control 进程维护。

QTS/QuTS 的 Shared Folder 权限、Advanced Folder Permissions/ACL、继承规则和 quota 都可能影响容器 bind mount，因此宿主上 `[ -w PATH ]` 通过并不能证明容器一定能写。

## 4. 先运行 QNAP 真预检

从 `deploy/qnap` 执行：

```sh
cp .env.example .env
# 修改 .env
sh ./preflight.sh
```

预检现在不仅检查网络，还会：

1. 确认 Docker / Compose / qnet / `/dev/net/tun`；
2. 确认父接口和 IPv4 拓扑；
3. 使用**实际 OpenSurge 镜像 + 实际 `/share/...` bind mount**验证 `/data` 的创建、写入、同步、重命名和删除；
4. 只为 `/data/web-auth` 设置 Web UID/GID；
5. 再以配置的 `OPENSURGE_WEB_UID:GID` 启动临时容器，验证 Web 身份确实可以创建、rename 和删除认证文件。

如果最后一项失败，优先检查：

```sh
id <QNAP用户>
```

以及 QTS/QuTS：

```text
控制台 → 权限 → 共享文件夹
控制台 → 权限 → 共享文件夹 → 高级权限
控制台 → 权限 → 配额
```

不要用 777 掩盖 ACL 问题。

## 5. 默认 Compose

使用：

```text
deploy/qnap/docker-compose.yml
```

它只有一个服务：

```text
opensurge
```

启动前由安装程序、Codex/Hermes、Container Station 或 shell 环境向 Compose 提供创建参数。例如：

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

不需要在 NAS 上执行 `docker compose build`。

## 6. 第一次启动与持久化配置

第一次启动且下面文件不存在时：

```text
/data/config/opensurge.yaml
```

entrypoint 使用创建时参数生成首次配置：

- 容器接口：`eth0`；
- LAN IP：`OPENSURGE_IP`；
- LAN CIDR：`OPENSURGE_SUBNET`；
- 上游网关：`OPENSURGE_GATEWAY`；
- DNS listen：OpenSurge IP。

如果持久化配置已经存在，新的 seed 值不会覆盖它。

因此：

- 同一台 NAS 更新/重建容器：保留 `/data`；
- 修改父网卡/IP/CIDR/上游网关：修改容器创建参数并重建；
- 订阅、规则、DNS、TUN 和设备策略：在 Web 中管理。

## 7. Web

打开：

```text
http://<OpenSurge-IP>:8080
```

首次访问创建管理员。

QNAP Web 当前重点：

- 显示容器实际接口、IPv4、CIDR 与路由状态；
- QNET 父网卡、静态 IP、CIDR、上游网关作为创建时参数只读显示；
- 管理 DNS / TUN 可变参数；
- 导入 HTTPS 订阅或 YAML；
- 管理草稿、当前运行版本和下次启动版本；
- 管理策略、Provider、设备规则；
- 查看连接、流量、Doctor、日志与生命周期操作；
- Doctor 在 Linux/QNAP 上检查 iproute2、TUN 和持久化文件系统语义，不再把 macOS `pfctl` 当作 QNAP 必需项；
- QNAP build 提供独立的响应式布局，窄屏不再被 1080px 最小宽度锁死；
- 保存配置后重新读取持久化状态进行确认。

QNAP build 不把桌面系统专属操作暴露为 NAS 功能。

## 8. 订阅持久化

导入 HTTPS 或 YAML 后先产生持久化草稿。

应用时后端会：

1. 读取当前 config revision；
2. 合并订阅和全局 overlay；
3. 验证完整候选配置；
4. 写入 `/data`；
5. 更新 `/data/config/opensurge.yaml`；
6. Web 重新读取 sources 状态；
7. 只有确认 `desired=true` 或 `applied=true` 才显示成功。

这样可以避免“HTTP 请求成功，但配置实际未持久化”的假成功。

## 9. `/data` 目录

推荐宿主路径：

```text
/share/Container/opensurge
```

挂载到：

```text
/data
```

主要目录：

- `config/`
- `control/`
- `web-auth/`
- `profiles/`
- `providers/`
- `state/`
- `backups/`
- `runtime/`
- `logs/`

同 NAS 重建保留完整 `/data`。迁移到另一台 NAS 时，不要把旧 `runtime/` 当普通用户配置直接恢复；同时重新执行 `id <username>` 和 `preflight.sh`，因为新 NAS 的 UID/GID、ACL、QNET 父接口都可能不同。

详见 [`PERSISTENCE.md`](PERSISTENCE.md)。

## 10. 基础验证

```sh
docker ps --filter name=opensurge
docker inspect --format '{{json .State.Health}}' opensurge
docker exec opensurge ip -br addr
docker exec opensurge ip route
docker exec opensurge ip rule
docker exec opensurge ls -l /dev/net/tun
```

默认应只有一个 OpenSurge 产品容器。

Gateway 启动后，same-LAN / nft-free 路径还应检查：

```sh
docker exec opensurge ip rule show
docker exec opensurge ip route show table 20241
```

应能观察到基于 ingress interface 的 OpenSurge policy rule，以及指向 TUN 的专用默认路由。

在缺少 `nf_tables` 的 QNAP 上，`nft list ruleset` 失败本身不再代表 same-LAN Gateway 失败。

## 11. 客户端测试

先只选择一台客户端，不修改主路由 DHCP：

```text
IPv4 Gateway = OpenSurge IP
DNS          = OpenSurge IP
```

依次验证：

1. DNS；
2. DIRECT；
3. PROXY；
4. UDP / QUIC；
5. 视频长连接；
6. 大文件下载；
7. `docker restart opensurge` 后恢复；
8. NAS reboot 后恢复。

## 12. 当前限制

- 第一稳定版聚焦 IPv4 Same-LAN Manual Gateway；
- 不自动修改主路由 DHCP；
- 不做下游 IPv6 takeover；
- 修改 QNET 父网卡、静态 IP、CIDR、主路由需要重建容器；
- QNAP 不同型号/固件的 QNET、ACL 和内核能力仍需要真机覆盖；
- ARM64 当前属于可构建但仍需更多 QNAP 真机验证的目标；
- stable 前仍需真实客户端、reboot、24h / 72h soak 和升级/回滚验证。
