# OpenSurge for QNAP — Docker 部署指南

> 当前支持的首个稳定拓扑：**IPv4 Same-LAN Manual Gateway（旁路由模式）**。
> 主路由 DHCP 保持开启，只让需要经过 OpenSurge 的客户端把 IPv4 网关和 DNS 指向 OpenSurge 容器。

## 1. 推荐拓扑

```text
主路由                192.168.2.1
QNAP NAS              192.168.2.240
├─ 网卡 1             NAS 管理/普通业务
└─ 网卡 2             可选：专门给 OpenSurge 的 QNET 父接口

OpenSurge 容器         192.168.2.241
  └─ QNET -> 指定 QNAP 网卡/桥接接口

测试客户端             192.168.2.100
  网关                 192.168.2.241
  DNS                  192.168.2.241
```

OpenSurge 容器使用 QNAP `qnet` 获得一个独立 LAN IP，不使用 `network_mode: host`，也不要求 `privileged: true`。

## 2. 双网卡 NAS：到底选择哪一个“网卡”

这里有两个不同概念，必须区分：

### QNAP 宿主机侧网卡：`OPENSURGE_PARENT_INTERFACE`

这是 **真正决定 OpenSurge 走 NAS 哪一块网卡** 的参数。

例如双网卡 NAS：

```env
OPENSURGE_PARENT_INTERFACE=eth1
```

也可能是：

```text
eth0
eth1
bond0
br0
```

具体名称以 QNAP Network & Virtual Switch 和 NAS 实际网络配置为准，不要根据“第一个网口/第二个网口”猜名字。

在 SSH 中可以直接运行：

```sh
cd OpenSurge-for-QNAP/deploy/qnap
sh ./preflight.sh --list-interfaces
```

或：

```sh
make qnap-interfaces
```

脚本会列出主机接口、IPv4 和默认路由，方便和 QNAP Network & Virtual Switch 中的网卡对应。

### 容器内部网卡：`OPENSURGE_CONTAINER_INTERFACE`

这是 OpenSurge 容器自身看到的接口。

标准单 QNET 网络部署中通常始终是：

```env
OPENSURGE_CONTAINER_INTERFACE=eth0
```

即使 QNAP 宿主机选择的是 `eth1`，容器内部仍很可能叫 `eth0`。

因此：

```text
QNAP eth1 / br0
       ↓ QNET
容器 eth0
```

不要把两者设置成同一个名字只是为了“看起来一致”。

## 3. 持久化存储

推荐在 QNAP 共享目录中建立专用目录：

```sh
mkdir -p /share/Container/opensurge
```

`.env` 中设置：

```env
OPENSURGE_DATA_PATH=/share/Container/opensurge
```

Compose 会将：

```text
/share/Container/opensurge  ->  /data
```

整块挂载。

### 为什么直接持久化整个 `/data`

OpenSurge 不仅有配置文件，还需要保存崩溃恢复和容器重建后的 ownership 信息。

主要目录：

| 目录 | 内容 | 是否必须跨容器重建保留 |
| --- | --- | --- |
| `/data/config` | 主配置 | 必须 |
| `/data/control` | 管理员凭据、内部控制 token | 必须 |
| `/data/profiles` | 导入/托管的配置 profile | 必须 |
| `/data/providers` | provider / rule-provider 数据 | 建议 |
| `/data/state` | 可选功能的持久状态 | 使用时必须 |
| `/data/backups` | 配置备份 | 建议 |
| `/data/runtime` | crash/reconciliation journal、PID/fingerprint、namespace ownership | **同 NAS 重建时必须** |
| `/data/logs` | OpenSurge 日志 | 建议 |
| `/data/licenses` | 许可证副本 | 非关键 |

详细说明见：[PERSISTENCE.md](PERSISTENCE.md)。

### 升级和迁移的区别

**同一台 NAS 升级/重建容器：**

保留完整 `/data`，包括 `runtime/`。

**迁移到另一台 NAS：**

恢复 `config/control/profiles/providers/state/backups`，但首次启动前清空旧：

```sh
rm -rf /share/Container/opensurge/runtime/*
```

`runtime/` 不是可跨主机迁移的普通配置。

## 4. 获取项目

当前阶段 Compose 默认从本仓库源码构建镜像，因此需要完整项目目录：

```sh
git clone https://github.com/zyk1172/OpenSurge-for-QNAP.git
cd OpenSurge-for-QNAP/deploy/qnap
```

以后有正式发布镜像后，可以再把 Compose 切换为固定版本镜像部署；当前不要假设存在 stable registry image。

## 5. 创建 `.env`

```sh
cp .env.example .env
```

至少修改：

```env
OPENSURGE_VERSION=dev
TZ=Asia/Shanghai

OPENSURGE_IP=192.168.2.241
OPENSURGE_SUBNET=192.168.2.0/24
OPENSURGE_GATEWAY=192.168.2.1

# 双网卡 NAS 的关键选项
OPENSURGE_PARENT_INTERFACE=eth1

# 容器内部通常保持 eth0
OPENSURGE_CONTAINER_INTERFACE=eth0

# 必须使用 QNAP 上的绝对路径
OPENSURGE_DATA_PATH=/share/Container/opensurge

OPENSURGE_LOG_MAX_SIZE=10m
OPENSURGE_LOG_MAX_FILES=3
OPENSURGE_ALLOWED_HOSTS=
```

### `OPENSURGE_IP`

必须：

- 与客户端和主路由位于目标 LAN；
- 不能和 NAS、主路由、其他设备冲突；
- 最好位于路由器 DHCP 动态地址池之外，或在路由器上进行保留。

## 6. 运行部署前检查

先列出网卡：

```sh
sh ./preflight.sh --list-interfaces
```

确认 `.env` 后运行完整检查：

```sh
sh ./preflight.sh
```

它会检查：

- IPv4 / CIDR / 网关是否一致；
- QNET 父接口名称；
- Docker 和 Compose V2；
- Compose 是否可正常解析；
- `/dev/net/tun`；
- QNAP 网卡是否存在；
- 主机到网关的路由信息；
- `/data` 持久化目录；
- 静态 IP 的明显冲突；
- qnet driver 是否被 Docker 枚举（仅 advisory）。

如果只想做配置/语法检查：

```sh
sh ./preflight.sh --env-file .env --static
```

## 7. Compose

仓库已提供可直接使用的：

```text
deploy/qnap/docker-compose.yml
```

关键部分如下：

```yaml
services:
  opensurge:
    build:
      context: ../..
      dockerfile: docker/Dockerfile
      args:
        OPENSURGE_VERSION: ${OPENSURGE_VERSION:-dev}
    image: opensurge-for-qnap:${OPENSURGE_VERSION:-dev}
    container_name: opensurge
    restart: unless-stopped

    cap_add:
      - NET_ADMIN
      - NET_RAW

    devices:
      - /dev/net/tun:/dev/net/tun

    sysctls:
      net.ipv4.ip_forward: "1"
      net.ipv4.conf.all.rp_filter: "0"
      net.ipv4.conf.default.rp_filter: "0"

    security_opt:
      - no-new-privileges:true

    environment:
      TZ: ${TZ:-Asia/Shanghai}
      OPENSURGE_ALLOWED_HOSTS: ${OPENSURGE_ALLOWED_HOSTS:-}
      OPENSURGE_SEED_LAN_IP: ${OPENSURGE_IP}
      OPENSURGE_SEED_LAN_CIDR: ${OPENSURGE_SUBNET}
      OPENSURGE_SEED_UPSTREAM_GATEWAY: ${OPENSURGE_GATEWAY}
      OPENSURGE_CONTAINER_INTERFACE: ${OPENSURGE_CONTAINER_INTERFACE:-eth0}

    volumes:
      - ${OPENSURGE_DATA_PATH}:/data

    networks:
      opensurge_lan:
        ipv4_address: ${OPENSURGE_IP}

    logging:
      driver: json-file
      options:
        max-size: "${OPENSURGE_LOG_MAX_SIZE:-10m}"
        max-file: "${OPENSURGE_LOG_MAX_FILES:-3}"

    stop_grace_period: 30s

networks:
  opensurge_lan:
    driver: qnet
    driver_opts:
      iface: ${OPENSURGE_PARENT_INTERFACE}
    ipam:
      driver: qnet
      options:
        iface: ${OPENSURGE_PARENT_INTERFACE}
      config:
        - subnet: ${OPENSURGE_SUBNET}
          gateway: ${OPENSURGE_GATEWAY}
```

仓库中的正式 Compose 带有必填变量校验和说明，应优先使用仓库文件，不要长期维护手工复制版本。

## 8. 启动

先建立数据目录：

```sh
mkdir -p /share/Container/opensurge
```

然后：

```sh
docker compose --env-file .env -f docker-compose.yml up -d --build
```

查看：

```sh
docker compose --env-file .env -f docker-compose.yml ps
docker logs --tail 100 opensurge
```

检查健康状态：

```sh
docker inspect --format '{{json .State.Health}}' opensurge
```

## 9. 首次启动配置

第一次启动时，如果：

```text
/data/config/opensurge.yaml
```

不存在，entrypoint 会使用 `.env` 传入的：

- `OPENSURGE_IP`
- `OPENSURGE_SUBNET`
- `OPENSURGE_GATEWAY`
- `OPENSURGE_CONTAINER_INTERFACE`

生成初始配置。

如果持久化配置已经存在：

> **绝不覆盖现有配置。**

因此修改 `.env` 不等于修改已经存在的 OpenSurge 运行配置；已有安装应通过 Web UI/配置迁移方式修改。

## 10. 打开 Web UI

浏览器访问：

```text
http://<OPENSURGE_IP>:8080
```

例如：

```text
http://192.168.2.241:8080
```

首次创建管理员账户。

随后在网络页面确认：

```text
LAN interface         eth0（通常）
LAN IP                与 OPENSURGE_IP 一致
LAN CIDR              与 OPENSURGE_SUBNET 一致
Upstream gateway      与 OPENSURGE_GATEWAY 一致
```

## 11. 先测试一台客户端

不要一次修改整个家庭网络。

选一台 iPhone/iPad/Mac/电脑，仅修改：

```text
IPv4 网关 = OpenSurge IP
DNS       = OpenSurge IP
```

测试：

1. 能打开 OpenSurge Web；
2. DNS 正常；
3. DIRECT 规则正常；
4. PROXY 规则正常；
5. UDP 正常；
6. 长连接/视频播放正常。

确认稳定后再迁移更多设备。

## 12. 双网卡部署建议

如果 NAS 两块网卡用途不同，例如：

```text
网卡 1：NAS 管理 / SMB / 普通服务
网卡 2：OpenSurge / 网关流量
```

可以把：

```env
OPENSURGE_PARENT_INTERFACE=eth1
```

指向第二块网卡。

OpenSurge 的 QNET 流量会绑定到该父接口，而不是因为 NAS 有两块网卡就自动选择。

如果两块网卡处于同一子网，QTS 自身可能还有 bridge/Virtual Switch/默认路由关系；这时以 QNAP Network & Virtual Switch 中实际拓扑为准，并重点检查：

```sh
sh ./preflight.sh --list-interfaces
sh ./preflight.sh
```

不要只根据 `eth0/eth1` 的数字猜物理端口。

## 13. 更新

保持：

```env
OPENSURGE_DATA_PATH=/share/Container/opensurge
```

不变。

然后：

```sh
docker compose --env-file .env -f docker-compose.yml down
docker compose --env-file .env -f docker-compose.yml up -d --build
```

新的容器 network namespace 与旧容器不同；OpenSurge 会利用持久化 runtime journal 识别 interrupted state，而不是根据旧 PID 猜测进程归属。

## 14. 故障恢复

客户端先恢复主路由网关/DNS，再检查：

```sh
docker logs --tail 200 opensurge

docker exec opensurge \
  omg status --config /data/config/opensurge.yaml --format json

docker exec opensurge \
  omg doctor --config /data/config/opensurge.yaml
```

如果状态显示 interrupted：

```sh
docker exec opensurge \
  omg stop --config /data/config/opensurge.yaml
```

然后再重建/启动。

## 15. 当前边界

当前稳定化范围仍然是：

- IPv4 Same-LAN Manual Gateway；
- 不自动修改 QNAP Network & Virtual Switch；
- 不做全 LAN DHCP takeover；
- 不做下游 IPv6 takeover；
- 不挂载 Docker socket；
- 不使用 host network；
- 默认不使用 `privileged: true`。

QNET 的宿主父接口属于 **部署参数**，不会让 Web UI 获得修改 QNAP 主机网络或 Docker daemon 的权限。
