# OpenSurge for QNAP — 单容器 Docker 部署

> QNAP 版默认只运行 **一个 `opensurge` 容器**。物理网卡 / Virtual Switch、QNET、静态 IPv4、LAN CIDR、主路由和持久化目录在创建容器时确定；运行后 Web 不再尝试修改 QNAP 宿主网络。

当前稳定化范围是 **IPv4 Same-LAN Manual Gateway（旁路由）**。主路由 DHCP 保持原状，只让需要经过 OpenSurge 的客户端把 IPv4 网关和 DNS 指向 OpenSurge。

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
│ TUN + nftables                │
│ /data 持久化                  │
└───────────────────────────────┘
          │
          ▼
局域网客户端把 Gateway/DNS 指向 OpenSurge IP
```

没有：

- `opensurge-manager`
- `opensurge-orchestrator`
- Docker Socket
- `privileged: true`
- 运行后修改 QTS / Virtual Switch 的逻辑

Gateway 容器只需要：

- `NET_ADMIN`
- `NET_RAW`
- `/dev/net/tun`

## 2. 使用预构建测试镜像

NAS 不负责编译 Go、React、dnsmasq 或 mihomo。

测试版镜像由 GitHub Actions 构建并发布：

```text
https://github.com/zyk1172/OpenSurge-for-QNAP/releases/tag/qnap-test-latest
```

TS-264C / x86_64 使用：

```text
OpenSurge-for-QNAP-test-amd64.tar.gz
```

加载：

```sh
gzip -dc OpenSurge-for-QNAP-test-amd64.tar.gz | docker load
```

加载后的镜像：

```text
opensurge-for-qnap:test
```

默认 Compose 使用 `pull_policy: never`，不会让 NAS 在镜像缺失时偷偷改为远程拉取另一个同名镜像。

## 3. 创建容器前必须确定的参数

这些不是运行期 Web 设置，而是 Docker/QNET 创建参数：

| 参数 | 含义 | 示例 |
|---|---|---|
| `OPENSURGE_PARENT_INTERFACE` | QNAP 物理网卡 / Virtual Switch | `eth1` / `br0` |
| `OPENSURGE_IP` | OpenSurge 独立 IPv4 | `192.168.2.241` |
| `OPENSURGE_SUBNET` | 当前 LAN CIDR | `192.168.2.0/24` |
| `OPENSURGE_GATEWAY` | 主路由 IPv4 | `192.168.2.1` |
| `OPENSURGE_DATA_PATH` | QNAP 持久化目录 | `/share/Container/opensurge` |

双网卡 NAS 不要根据 `eth0/eth1` 数字猜物理口。应结合 QTS「网络与虚拟交换机」和宿主 `ip addr` / `ip route` 识别实际父接口。

容器内部数据接口通常仍是 `eth0`。这是 Docker/QNET 创建出的容器接口，与 QNAP 宿主父接口名称不同是正常现象。

## 4. 默认 Compose

正式默认文件：

```text
deploy/qnap/docker-compose.yml
```

它只有一个服务：

```text
opensurge
```

运行前由安装程序、Hermes、Container Station 或 shell 环境把上面的创建参数提供给 Compose。例如：

```sh
export OPENSURGE_PARENT_INTERFACE=eth1
export OPENSURGE_IP=192.168.2.241
export OPENSURGE_SUBNET=192.168.2.0/24
export OPENSURGE_GATEWAY=192.168.2.1
export OPENSURGE_DATA_PATH=/share/Container/opensurge

docker compose -f docker-compose.yml config
docker compose -f docker-compose.yml up -d
```

不需要 `.env` 文件；也不要执行 `docker compose build`。

## 5. 第一次启动

第一次启动且：

```text
/data/config/opensurge.yaml
```

不存在时，entrypoint 使用创建时参数生成 QNAP seed config：

- `gateway.interface = eth0`
- `gateway.lan_ip = OPENSURGE_IP`
- `gateway.lan_cidr = OPENSURGE_SUBNET`
- `gateway.upstream_gateway = OPENSURGE_GATEWAY`
- `dns.listen = OPENSURGE_IP`

如果 `/data/config/opensurge.yaml` 已存在，新的环境值 **不会覆盖持久化配置**。

因此：

- 同一台 NAS 重建容器：保留 `/data`；
- 改网卡/IP/CIDR/主路由：修改容器创建参数并重建；
- OpenSurge 内部订阅、规则、DNS、TUN、设备策略：在 Web 中管理。

## 6. Web

容器启动后打开：

```text
http://<OpenSurge-IP>:8080
```

例如：

```text
http://192.168.2.241:8080
```

首次创建管理员。

QNAP build 的 Web 与 Mac build 分流：

- 「网络设置」显示容器实际接口/IP/默认路由；
- QNET 父接口、容器静态 IP、CIDR、主路由只读，并提示需要重建容器；
- 不提供 macOS Wi‑Fi DHCP takeover；
- 不提供合盖保持运行；
- 不提供恢复 Mac DHCP；
- 不提供 Finder 操作；
- 订阅快照只显示容器持久化路径；
- 运行参数保存时会校验持久化后的 config revision；
- 网关运行中保存可变运行参数时，Web 执行停止 → 保存 → 重新启动，并明确报告失败。

## 7. 订阅持久化

导入 HTTPS 或 YAML 后只产生草稿。

点击：

```text
设为下次启动版本
```

时，后端会：

1. 读取当前 config revision；
2. 组合订阅和全局 overlay；
3. 做完整候选配置验证；
4. 把 imported profile 和 source digest 写入 `/data`；
5. 更新 `/data/config/opensurge.yaml`；
6. QNAP Web 再重新读取 `/api/v1/sources`；
7. 只有重新读取后确认 `desired=true` 或 `applied=true` 才提示成功。

因此不再存在“HTTP 返回成功，但页面假定已经保存”的静默成功。

## 8. 持久化

宿主：

```text
/share/Container/opensurge
```

挂载到：

```text
/data
```

整个 `/data` 保留，包括：

- `config/`
- `control/`
- `profiles/`
- `providers/`
- `state/`
- `backups/`
- `runtime/`
- `logs/`

同 NAS 容器重建保留整个目录。迁移到另一台 NAS 时，不要把旧 `runtime/` 当成普通配置直接恢复。

## 9. 首次验证

```sh
docker ps --filter name=opensurge
docker inspect --format '{{json .State.Health}}' opensurge
docker exec opensurge ip -br addr
docker exec opensurge ip route
docker exec opensurge cat /data/config/opensurge.yaml
```

应该只有一个 OpenSurge 产品容器：

```text
opensurge
```

不应出现：

```text
opensurge-manager
opensurge-orchestrator
```

确认 `/data/config/opensurge.yaml` 中 LAN IP/CIDR/主路由与创建容器时一致。

## 10. 客户端测试

先只选择一台测试设备，不要修改主路由 DHCP。

客户端：

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
7. `docker restart opensurge` 后持久化；
8. NAS reboot 后恢复。

## 11. 当前限制

- 首版仍是 IPv4 Same-LAN Manual Gateway；
- 不自动修改主路由 DHCP；
- 不做下游 IPv6 takeover；
- QNET 的真实父接口选择仍需要 QNAP 实机确认；
- 修改 QNET 父网卡、静态 IP、CIDR、主路由需要重建容器；
- 正式 stable 前仍需 TS-264C 24h / 72h soak。
