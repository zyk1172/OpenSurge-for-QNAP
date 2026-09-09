# OpenSurge for QNAP — Web-first Docker 部署

> 默认部署不再要求 `.env`。用户网络参数全部在 Web 中完成。

当前稳定化拓扑仍是 **IPv4 Same-LAN Manual Gateway（旁路由）**：主路由 DHCP 保持开启，只让需要经过 OpenSurge 的客户端把 IPv4 网关和 DNS 指向 OpenSurge Gateway。

## 1. 为什么默认 Compose 不再包含网络参数

QNAP `qnet` 的父网卡、容器静态 IP、CIDR 和网关必须在 Gateway 容器创建时确定。如果把这些值写进 `.env`，首次部署就必须离开 Web。

新的默认架构把部署分成三层：

```text
浏览器
  │ http://NAS_IP:61780
  ▼
opensurge-manager
  - host network 只用于读取真实 QNAP 网卡/路由
  - 无 NET_ADMIN
  - 无 Docker socket
  │ Unix socket
  ▼
opensurge-orchestrator
  - network_mode: none
  - 不开放任何 TCP 端口
  - 唯一持有 /var/run/docker.sock
  - 只允许创建/替换固定名称的 OpenSurge Gateway/QNET
  │ Docker Engine
  ▼
opensurge-gateway
  - QNET 独立 LAN IP
  - NET_ADMIN + NET_RAW + /dev/net/tun
  - 无 Docker socket
  - 不使用 host network
  - 默认不使用 privileged:true
```

Docker socket 仍具有宿主机高权限，因此它只放在无网络的 orchestrator sidecar 中；LAN-facing manager 永远不挂载 Docker socket。

## 2. 唯一需要的首次部署动作

SSH 到 QNAP，获取项目：

```sh
cd /share/Container
git clone https://github.com/zyk1172/OpenSurge-for-QNAP.git
cd OpenSurge-for-QNAP/deploy/qnap
```

然后直接启动默认 Compose：

```sh
docker compose -f docker-compose.yml up -d --build
```

**不需要创建 `.env`。**

检查：

```sh
docker compose -f docker-compose.yml ps
docker logs --tail 100 opensurge-manager
docker logs --tail 100 opensurge-orchestrator
```

## 3. 打开部署管理 Web

浏览器访问：

```text
http://<QNAP管理IP>:61780
```

例如 NAS 管理地址是 `192.168.2.240`：

```text
http://192.168.2.240:61780
```

首次进入先创建管理员账户。管理员凭据保存在 Docker named volume `opensurge_manager_data`，不写入 Compose。

登录后在同一页面完成：

1. 选择 **QNAP 父网卡 / Virtual Switch**；
2. 填写 OpenSurge Gateway 的独立 IPv4；
3. 填写 LAN CIDR；
4. 填写主路由 IPv4；
5. 选择 QNAP 持久化目录（默认 `/share/Container/opensurge`）；
6. 点击“应用并创建/重建 Gateway”。

### 双网卡 NAS

Web 会从 QNAP 宿主网络命名空间读取真实接口并列出，例如：

```text
eth0 · 192.168.2.240 · 默认路由
eth1 · 192.168.2.10
br0  · 192.168.2.240
```

应根据 QTS“网络与虚拟交换机”的实际拓扑选择，不要按 `eth0/eth1` 数字猜物理接口。

这里选择的是 **QNAP 宿主父接口**。Gateway 容器内部仍使用自己的 `eth0`，不需要用户配置。

## 4. Web 应用配置时发生什么

Manager 只向 Unix socket 发送结构化部署参数。Orchestrator 会：

1. 校验网卡名、IPv4、CIDR、主路由、持久化路径；
2. 拒绝 `/etc`、`/` 等危险 bind path，只允许 `/share/...`；
3. 检查本地 OpenSurge Gateway 镜像；
4. 只管理固定资源：
   - container：`opensurge-gateway`
   - network：`opensurge-managed-lan`
5. 创建 QNET，并将 Web 选择的父接口写入 QNET `iface`；
6. 为 Gateway 配置 Web 选择的静态 IPv4；
7. 仅给 Gateway：`NET_ADMIN`、`NET_RAW`、`/dev/net/tun`；
8. 挂载 Web 选择的 `/share/...` 到 `/data`；
9. 等 Gateway healthcheck 通过后才报告部署成功；
10. 变更失败时尽力恢复上一个 OpenSurge-managed Gateway 配置。

Orchestrator 不接受任意容器名、任意镜像、任意 capability、任意 Docker API 请求或任意 host bind mount。

## 5. Gateway Web

部署成功后 Manager 会显示 Gateway 地址，例如：

```text
http://192.168.2.241:8080
```

打开后首次创建 Gateway 管理员。

之后以下配置都在 Gateway Web 中完成：

- 订阅 / profile；
- provider / rule-provider；
- 节点与策略组；
- 规则；
- 设备策略；
- Gateway 启停；
- DNS / TUN / 连接与诊断。

部署层参数（QNAP 父网卡、Gateway IP、CIDR、主路由、持久化路径）回到 `NAS_IP:61780` 修改即可；无需编辑 `.env`。

## 6. 首次测试

部署完成后先确认容器：

```sh
docker ps --filter name=opensurge
```

应至少看到：

```text
opensurge-manager
opensurge-orchestrator
opensurge-gateway
```

Gateway 健康状态：

```sh
docker inspect --format '{{json .State.Health}}' opensurge-gateway
```

确认 Gateway 独立 IP：

```sh
docker exec opensurge-gateway ip addr show eth0
docker exec opensurge-gateway ip route
```

然后只选一台测试客户端：

```text
IPv4 网关 = OpenSurge Gateway IP
DNS       = OpenSurge Gateway IP
```

依次测试：DNS、DIRECT、PROXY、UDP、视频/长连接、大文件下载。不要一开始修改主路由 DHCP 或让全 LAN 接管。

## 7. 持久化

Gateway 的 `/data` 仍整块 bind 到 Web 中选择的 QNAP `/share/...` 目录，其中包括：

- `config/`
- `control/`
- `profiles/`
- `providers/`
- `state/`
- `backups/`
- `runtime/`
- `logs/`

同一台 NAS 重建 Gateway 时保留整个 `/data`。迁移到另一台 NAS 时不要把旧 `runtime/` 当成普通配置直接恢复。

详细边界见 [PERSISTENCE.md](PERSISTENCE.md)。

## 8. 高级手工 Compose

旧的环境变量/QNET 直连方式保留为：

```text
docker-compose.manual.yml
```

它只用于开发、故障排查或 manager/orchestrator 无法工作的特殊环境，**不再是默认产品部署方式**。

## 9. 当前限制

- 首版仍只支持 IPv4 Same-LAN Manual Gateway；
- 不自动修改主路由 DHCP；
- 不做下游 IPv6 takeover；
- QNET 创建能力必须在目标 QNAP/Container Station 实机验证；
- orchestrator 需要 Docker socket，但通过无网络 sidecar 与 Web 进程隔离；
- 在正式 stable 发布前仍需要 TS-264C 真机、QNAP reboot 和 24h/72h soak 验证。
