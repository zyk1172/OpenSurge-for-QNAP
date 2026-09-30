# 群晖、飞牛与通用 Docker NAS 部署

当前为 **实验性 IPv4 + macvlan + same-LAN 旁路由适配**。配置与网关核心可共用，但尚未在群晖/飞牛真机完成客户端、重启和长期运行验收。QNAP 继续使用 [`../qnap`](../qnap/README.zh-CN.md) 的 QNET 部署。

## 前提

- Linux NAS，rootful Docker 与 Compose。群晖建议 DSM 7 上支持 Container Manager 的机型；不支持 Docker 的机型不在范围内。
- CPU 为 x86_64 / amd64 或 aarch64 / arm64，加载相应镜像；当前不提供 32 位 ARM 镜像。
- `/dev/net/tun`、macvlan 和 IPv4 策略路由可用。预检失败时按具体系统解决，不能用全特权模式绕过。
- 使用有线 LAN。父接口和上游交换机/虚拟交换机允许多个 MAC。无线、端口安全和虚拟化反欺骗设置可能阻止 macvlan。
- 预留一个独立 IPv4，既不是 NAS 地址也不是主路由地址，并排除在 DHCP 自动分配范围外。

## 1. 镜像和部署文件

正常用户无需在 NAS 构建。使用包含本次多 NAS 改动的 CI 镜像归档，核对随附 SHA256 后 `docker load`。旧版 `1.0.0` 或旧 `qnap-test-latest` 不能直接当作已包含此功能；镜像仓库暂保留历史名称 `opensurge-for-qnap`。

```sh
gzip -dc OpenSurge-for-QNAP-test-amd64.tar.gz | docker load
docker image inspect opensurge-for-qnap:test
```

ARM64 设备应选择 arm64 归档。`.env` 示例指向本地 `opensurge-for-qnap:test`，默认不拉取。发布支持此功能的正式镜像后，可修改 `OPENSURGE_IMAGE`，将 `OPENSURGE_PULL_POLICY` 改为 `missing`，并先 `docker compose pull`。

将 `deploy/nas` 的 Compose、预检、环境示例，以及相邻 `deploy/qnap/preflight.sh` 一起放到 NAS。两个预检文件必须保留相对目录结构。

## 2. 选择平台

进入 `deploy/nas`，选择一个示例复制为 `.env`：

```sh
# 群晖
cp .env.synology.example .env
# 或飞牛：cp .env.fnos.example .env
# 或其他 Docker NAS：cp .env.example .env
```

| 配置 | 含义 |
| --- | --- |
| `OPENSURGE_NAS_PLATFORM` | `synology`、`fnos` 或 `generic`；显式声明平台，不做容器内品牌猜测 |
| `OPENSURGE_IP` | 独立、预留、未占用的容器 IPv4 |
| `OPENSURGE_SUBNET` | LAN 的标准网络 CIDR，例如 `192.168.2.0/24` |
| `OPENSURGE_GATEWAY` | 原主路由 IPv4 |
| `OPENSURGE_PARENT_INTERFACE` | 目标 LAN 的真实宿主网卡、bond 或桥 |
| `OPENSURGE_DATA_PATH` | 专用绝对数据路径，升级保留；与 Compose 项目目录分开 |
| `OPENSURGE_WEB_UID/GID` | 经 `id <NAS用户名>` 确认的普通用户数字身份 |

```sh
sh ./preflight.sh --list-interfaces
ip -4 route get 192.168.2.1
id <NAS用户名>
```

群晖启用 Open vSwitch 后可能出现 `ovs_eth0` 或 `ovs_bond0`，未启用时可能是 `eth0` / `bond0`；这些名称必须结合实际路由核对。数据目录示例 `/volume1/docker/opensurge-data` 中的卷号同样按实际修改。

飞牛网卡可能是 `enp1s0`、`eno1`、`eth0` 或桥；文件管理器中复制实际路径，不要把 `/vol1/1000/docker/opensurge-data` 当固定系统路径。

创建选定的专用数据目录，确保所在卷已挂载。不要将共享文件夹根目录或已有其他服务的数据目录作为 `/data`。

## 3. 预检与启动

通过 NAS 的 SSH 终端，在有权访问 Docker 的管理身份下运行：

```sh
sh ./preflight.sh
docker compose --env-file .env -f docker-compose.yml up -d
docker logs opensurge
```

预检检查 Compose、网络地址、rootful Docker/macvlan、宿主接口、TUN、真实数据挂载与 Web UID/GID 的读写语义，并在 `--network none` 的临时容器中验证 TUN / `ip rule iif` 转发。它只为 OpenSurge 的 `web-auth` 目录设置所有权，不递归修改共享文件夹。

只有 `docker-compose` 命令的设备，可用同样参数替换 `docker compose`；若版本不认识 `pull_policy` 等字段，更新受支持的容器管理套件/Compose，不要删去关键网络或权限设置。

缺少 `/dev/net/tun` 时，需要按该 NAS 系统/机型配置内核 TUN 模块与开机恢复。不同内核的模块位置不同，本文不提供通用 `mknod` 或固定模块路径。预检会明确失败，待 TUN 设备可用后重试。

群晖官方项目界面支持 Compose 文件，但网络界面仅文档化 bridge/host。本适配推荐 SSH + Compose；使用项目界面时需确认 `.env`、macvlan、设备、权限和 sysctl 完整保留。不要改成普通 bridge 或 host 来取得同样的旁路由效果。

## 4. 使用一台真实客户端验收

从 **另一台局域网电脑/手机** 打开 `http://OpenSurge-IP:8080`，使用容器日志的一次性 bootstrap token 创建管理员，导入 Mihomo 订阅或 YAML，启动网关。将一台客户端的 **IPv4 网关与 DNS** 均设为 OpenSurge IP，测试 DNS、直连、代理、TCP、UDP/QUIC 和设备策略。

macvlan 默认隔离 NAS 宿主与容器，因此：

- NAS 自身无法直接访问 OpenSurge IP 是预期行为，不能仅凭 NAS 内的 `curl` 判断失败。
- NAS 的反向代理也可能无法连接该 IP；先用其他 LAN 设备直连 Web。
- 当前适配不提供“让 NAS 使用 OpenSurge”，不要套用 QNAP Host Takeover override。
- 此部署不自动创建宿主 macvlan shim、不改 NAS 默认网关/DNS、不修改全家 DHCP。

Compose 仅保留 OpenSurge IP 的 `/32` 地址分配范围；该 Docker 网络专用于 OpenSurge，不应连接其他容器。

## 更新、恢复和排查

更新镜像后重新运行 Compose，继续挂载原 `/data`。数据目录保留账户、配置、来源、desired/applied 和恢复记录。修改父接口、IPv4、CIDR 或上游网关时，网络及容器都需重建；`docker compose down` 只用于这个项目，随后 `up -d`，不要加删除数据的操作。

**已有 `/data/config/opensurge.yaml` 不会被新 `.env` 覆盖。** 如修改地址，先在停止网关的状态下同步持久配置中的 `gateway.lan_ip/lan_cidr/lan_prefix_len/upstream_gateway`、接口和 `dns.listen`，再重建；仅改 `.env` 不会迁移配置。跨 NAS 迁移也须核对新网卡与 UID/GID。

```sh
docker ps --filter name=opensurge
docker logs --tail 200 opensurge
docker inspect --format '{{json .State.Health}}' opensurge
docker network inspect opensurge-macvlan
docker exec opensurge ip -4 addr
docker exec opensurge ip -4 rule
```

已运行容器使用同一 IP 时，预检的地址冲突检查可能报告占用；检查该 IP 的归属，维护窗口内停下本项目再预检。ping 无响应不能证明地址空闲，仍需核对主路由 DHCP/ARP 与静态地址规划。

Web 中运行 Doctor 检查实际数据面。same-LAN 使用 iif 策略路由，不因 nftables 不可用就必然失败；当前新平台不承诺 isolated-LAN/NAT。重启 NAS 后要分别检查容器恢复与真实客户端路径。

适配来源、未验证项和正式支持门槛见 [调研记录](../../docs/NAS_ADAPTATION_RESEARCH.zh-CN.md)。当前交付为 Docker 部署，不含 `.spk` / `.fpk` 原生套件或应用商店上架。
