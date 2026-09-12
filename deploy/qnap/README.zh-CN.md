# OpenSurge for QNAP — 单容器 Docker 部署

QNAP 默认部署只运行一个 `opensurge` 容器。物理网卡 / Virtual Switch、QNET、静态 IPv4、LAN CIDR、上游路由和持久化目录在创建容器时确定；运行后的 Web 负责 OpenSurge 自身配置，不修改 QTS 宿主网络。

当前稳定化主线仍是 **IPv4 Same-LAN Manual Gateway（旁路由）**：主路由 DHCP 保持原状，只让需要经过 OpenSurge 的客户端把 IPv4 网关和 DNS 指向 OpenSurge。

QNAP same-LAN 另提供实验性的 **IPv6 DNS / fake-IP 定向接管**：客户端继续使用主路由提供的公网 IPv6 与 IPv6 默认路由；主路由只把 Mihomo fake IPv6 网段静态路由到 OpenSurge。它不是“OpenSurge 接管整个 LAN IPv6 默认网关”。

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

### Same-LAN QNAP 的 IPv4 透明代理后端

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

这条 IPv4 manual-gateway 路径不依赖 nftables，因此适用于部分缺少 `nf_tables` netlink 支持、但 TUN 与 policy routing 正常的 QNAP 内核。

需要 NAT 的隔离下游拓扑仍使用 nftables/fwmark 后端，并保留严格能力检查。

### Same-LAN 的 IPv6 定向路径

启用 IPv6 DNS / fake-IP 定向接管后：

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
                                                  tun0
```

不会再安装“所有 `eth0` 入站 IPv6 都进入 TUN”的规则。容器内只选择：

```text
fdfe:dcba:9876::/64 → tun0
```

主路由继续提供原来的 RA、DHCPv6、IPv6 Bridge/Passthrough、公网 IPv6 地址和默认 IPv6 路由。

完整说明见 [`../../docs/QNAP_IPV6_DNS_FAKEIP.zh-CN.md`](../../docs/QNAP_IPV6_DNS_FAKEIP.zh-CN.md)。

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

如果最后一项失败，优先检查 `id <QNAP用户>`、QTS/QuTS 共享文件夹权限、高级权限/ACL 与配额。不要用 777 掩盖 ACL 问题。

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

### IPv6 相关 Compose sysctl

Compose 会在容器 network namespace 中启用 IPv6/forwarding，并设置：

```text
net.ipv6.conf.eth0.accept_ra=2
net.ipv6.conf.eth0.autoconf=1
```

原因是 Linux 在 forwarding 开启时默认可能停止接收 RA，而新的 IPv6 fake-IP 路径需要 OpenSurge 同时保留主路由提供的原生 IPv6 路由/前缀。

这些 sysctl 只作用于 OpenSurge 容器，不修改 QTS 宿主网络。

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

容器还会根据固定 IPv4 生成稳定的 IPv6 link-local 下一跳。例如：

```text
192.168.2.241 → fe80::1:0:c0a8:2f1
```

它用于主路由把 `fdfe:dcba:9876::/64` 静态路由到 OpenSurge，避免容器重建后自动 link-local 变化导致路由失效。

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
- 管理 IPv6 DNS / fake-IP 定向接管，并显示主路由需要填写的 DNS、fake IPv6 前缀和稳定 link-local 下一跳；
- 导入 HTTPS 订阅或 YAML；
- 管理草稿、当前运行版本和下次启动版本；
- 管理策略、Provider、设备规则；
- 查看连接、流量、Doctor、日志与生命周期操作；
- Doctor 在 Linux/QNAP 上检查 iproute2、TUN、持久化文件系统以及 IPv6 forwarding / `accept_ra=2` / 稳定下一跳；
- QNAP build 提供独立的响应式布局；
- 保存配置后重新读取持久化状态进行确认。

## 8. 配置 IPv6 DNS / fake-IP 定向接管（可选）

此功能只改变 IPv6 fake-IP 的数据路径，不把 OpenSurge 设为客户端 IPv6 默认网关。

Web → QNAP 网络 → **IPv6 DNS 定向接管**：

1. 打开启用开关；
2. 记下 Web 显示的：
   - 主路由 DNS 上游（OpenSurge IPv4）；
   - `fdfe:dcba:9876::/64`；
   - OpenSurge 稳定 link-local 下一跳；
   - LAN/桥接口；
3. 在主路由把用于客户端的 DNS 上游指向 OpenSurge；
4. 在主路由添加 `fdfe:dcba:9876::/64` → OpenSurge link-local 的 IPv6 静态路由；
5. **不要关闭主路由 RA，也不要把 `::/0` 指向 OpenSurge**；
6. 回到 Web 勾选“主路由 DNS 和 IPv6 静态路由已配置”；
7. 保存并重启 Gateway。

注意：Mihomo 当前使用统一 fake-IP DNS，A 查询仍可能得到 IPv4 fake-ip。本功能不会增加/修改 IPv4 路由。如果你把 OpenSurge DNS 全局提供给未使用现有 OpenSurge IPv4 数据面的设备，需要确认现有 IPv4 fake-IP 路径可达，或在主路由使用合适的 DNS 策略。

## 9. 订阅持久化

导入 HTTPS 或 YAML 后先产生持久化草稿。应用时后端读取当前 revision、合并订阅/overlay、验证完整候选配置、写入 `/data` 并重新读取状态；只有确认 `desired=true` 或 `applied=true` 才显示成功。

## 10. `/data` 目录

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

同 NAS 重建保留完整 `/data`。迁移到另一台 NAS 时，不要把旧 `runtime/` 当普通用户配置直接恢复；同时重新执行 `id <username>` 和 `preflight.sh`。

详见 [`PERSISTENCE.md`](PERSISTENCE.md)。

## 11. 基础验证

```sh
docker ps --filter name=opensurge
docker inspect --format '{{json .State.Health}}' opensurge
docker exec opensurge ip -br addr
docker exec opensurge ip route
docker exec opensurge ip rule
docker exec opensurge ls -l /dev/net/tun
```

默认应只有一个 OpenSurge 产品容器。

IPv4 same-LAN manual gateway 启动后：

```sh
docker exec opensurge ip rule show
docker exec opensurge ip route show table 20241
```

启用 IPv6 DNS/fake-IP 后额外检查：

```sh
docker exec opensurge cat /proc/sys/net/ipv6/conf/eth0/accept_ra
docker exec opensurge ip -6 addr show dev eth0
docker exec opensurge ip -6 rule show
docker exec opensurge ip -6 route show table 20241
```

预期 IPv6 规则只选择 `fdfe:dcba:9876::/64`，而不是 `iif eth0` 的 IPv6 默认路由。

## 12. 客户端测试

IPv4 主线先只选择一台客户端，不修改主路由 DHCP：

```text
IPv4 Gateway = OpenSurge IP
DNS          = OpenSurge IP
```

依次验证 DNS、DIRECT、PROXY、UDP/QUIC、长连接、大文件、容器 restart 和 NAS reboot。

IPv6 DNS/fake-IP 模式无需逐台修改客户端 IPv6：确认客户端仍保留运营商/主路由提供的公网 IPv6 与默认路由，然后验证 fake AAAA 目标进入 OpenSurge、普通 IPv6 不被整体改道。

## 13. 当前限制

- 第一稳定版主线仍聚焦 IPv4 Same-LAN Manual Gateway；
- 不自动修改主路由 DHCP；
- 可选 IPv6 功能只定向接管 `fdfe:dcba:9876::/64`，不做 whole-LAN IPv6 default-route takeover；
- OpenSurge 不发送 same-LAN RA/SLAAC；
- QNAP Linux TUN 的 IPv6 路径不保证逐设备身份；
- 修改 QNET 父网卡、静态 IP、CIDR、主路由需要重建容器；
- QNAP 不同型号/固件的 QNET、ACL 和内核能力仍需要真机覆盖；
- ARM64 当前属于可构建但仍需更多 QNAP 真机验证的目标；
- stable 前仍需真实客户端、reboot、24h / 72h soak 和升级/回滚验证。
