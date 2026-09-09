# OpenSurge for QNAP — Docker / Container Station 部署

> 状态：Phase 2 开发文档。Docker 镜像与 QNAP 真机 soak test 完成前，不标记为 stable release。

OpenSurge for QNAP 的正式运行方式是 Docker/OCI。QNAP 上优先使用 **Container Station + QNET**，让 OpenSurge 容器拥有一个独立的 LAN IPv4，同时保持自己的 Linux network namespace。

QNAP 官方 2026 年的 Docker 静态 IP 指南同样使用 QNET：
<https://www.qnap.com/en/how-to/faq/article/how-to-deploy-unifi-network-application-via-docker-on-qnap-nas>

## 1. 推荐拓扑

```text
Main router                  192.168.2.1
        │
        ├──── QNAP host      192.168.2.240
        │
        └──── OpenSurge      192.168.2.241
                 │
                 ├─ Web      :8080
                 ├─ DNS      :53
                 ├─ mihomo TUN
                 ├─ nftables
                 └─ policy routing

Test client
  IPv4:    192.168.2.x
  Gateway: 192.168.2.241
  DNS:     192.168.2.241
```

先只接入一台测试设备。主路由 DHCP 保持开启，不做 DHCP takeover。

## 2. 为什么 QNAP 默认使用 QNET

QNET 是 Container Station 提供的 QNAP 网络驱动，可以让容器直接获得 LAN 静态 IP。相对于普通 Docker `macvlan`，QNET 更适合作为 QNAP 默认方案，因为 NAS host 和同 NAS 的其他容器可能需要访问 OpenSurge 的 LAN 地址。

不要把 `network_mode: host` 当作默认方案。OpenSurge 会管理 TUN、nftables、policy routing；独立 network namespace 可以显著降低错误配置影响 QTS/QuTS hero 宿主机网络的风险。

通用 Linux 主机将在独立部署文件中使用 macvlan/ipvlan；QNAP 配置不应为了通用 Linux 而放弃 QNET。

## 3. 前置条件

- QTS / QuTS hero 上已安装 Container Station。
- 建议使用 Container Station 3.x 及 Compose V2。
- `/dev/net/tun` 可供容器映射。
- 在目标 LAN 中准备一个不会被 DHCP 动态分配的静态 IPv4。
- 确认 OpenSurge 使用的 QNAP 网络接口。不要根据接口名字猜测。
- 对 `/data` 对应目录建立正常的 NAS 备份策略。

QNAP 关于 Container Station 3 / Compose 应用的官方说明：
<https://www.qnap.com/en-us/how-to/tutorial/article/how-to-use-container-station-3>

## 4. 确认 QNAP LAN 接口

可在 QNAP SSH 中检查：

```sh
ip -br link
ip -4 addr
ip route
```

也可使用：

```sh
ifconfig
```

接口可能是 `eth0`、`br0`、`bond0` 或其他由 Network & Virtual Switch 创建的接口。

**不要使用当前被其他关键工作负载独占/专用的接口。** 如果 NAS 有多网口，先明确每个接口当前承担的业务再选择。

## 5. 配置环境变量

复制：

```sh
cd deploy/qnap
cp .env.example .env
```

至少修改：

```dotenv
OPENSURGE_IP=192.168.2.241
OPENSURGE_SUBNET=192.168.2.0/24
OPENSURGE_GATEWAY=192.168.2.1
OPENSURGE_PARENT_INTERFACE=br0
OPENSURGE_DATA_PATH=/share/Container/opensurge
```

这些值必须按实际网络修改。

### OpenSurge IP

要求：

- 当前未被其他设备使用；
- 推荐在主路由 DHCP 动态池之外；
- 或在主路由中做固定地址保留；
- 与主路由处于同一 IPv4 子网。

## 6. 创建应用

在 Container Station：

1. 打开 **Container Station**。
2. 进入 **Applications / 应用程序**。
3. 创建新应用。
4. 使用 `deploy/qnap/docker-compose.yml`。
5. 填入实际环境变量。
6. 先执行 YAML Validate。
7. 再创建应用。

也可以 SSH 后使用 Compose V2：

```sh
docker compose -f deploy/qnap/docker-compose.yml up -d --build
```

QTS 5.1 / Container Station 3.0 之后应使用 `docker compose`，而不是旧的 `docker-compose`：
<https://www.qnap.com/en-uk/how-to/faq/article/why-cant-i-use-docker-compose-commands-in-container-station>

## 7. Docker 权限

默认 Compose 只授予：

```yaml
cap_add:
  - NET_ADMIN
  - NET_RAW

devices:
  - /dev/net/tun:/dev/net/tun
```

同时在容器自己的 network namespace 中设置：

```yaml
sysctls:
  net.ipv4.ip_forward: "1"
  net.ipv4.conf.all.rp_filter: "0"
  net.ipv4.conf.default.rp_filter: "0"
```

默认**不使用**：

```yaml
privileged: true
network_mode: host
```

如果未来某个 QNAP 版本必须增加权限，必须先记录具体缺失能力并按最小权限增加，不能直接把 `privileged` 作为通用解决办法。

## 8. 首次打开 Web UI

从 LAN 内另一台设备访问：

```text
http://<OPENSURGE_IP>:8080/
```

首次进入会要求创建管理员：

- 用户名 3–64 字符；
- 密码至少 12 字符；
- 密码使用 Argon2id 存储；
- 原始密码不会写入配置或日志。

LAN-facing Web Gateway 与 privileged Control API 是两个边界：

```text
Browser
   │
   ▼
0.0.0.0:8080
Authenticated Web Gateway
   │ internal bearer
   ▼
127.0.0.1:61767
Privileged Control API
```

Control API 仍然只监听 loopback，不直接暴露到 LAN。

## 9. 第一次配置网络

镜像首次启动会生成 `/data/config/opensurge.yaml`。其中 `192.168.50.0/24` 仅是安全的示例配置。

在启动 Gateway 之前，必须把以下项目改成真实网络：

- LAN interface；
- container LAN IP；
- LAN CIDR；
- upstream interface；
- upstream gateway；
- DNS listen address。

第一稳定版本只把 **Same-LAN Manual Gateway** 作为产品级支持路径：

```text
主路由 DHCP：保持开启
OpenSurge DHCP：关闭
测试客户端：手工把 Gateway + DNS 指向 OpenSurge
```

不要第一轮就关闭主路由 DHCP。

## 10. 健康检查

容器提供：

```text
GET /health/live
GET /health/ready
```

- `live`：LAN-facing Web Gateway 进程还活着。
- `ready`：内部 loopback Control API 可以响应。

Docker HEALTHCHECK 使用 `live`。

外部互联网暂时不可达不应直接把容器判为 unhealthy。DIRECT / proxy / DNS / provider 等属于 connectivity/doctor 诊断层。

## 11. 接入第一台测试设备

例如 OpenSurge 为 `192.168.2.241`：

```text
Client IPv4: 192.168.2.100
Subnet:      255.255.255.0
Gateway:     192.168.2.241
DNS:         192.168.2.241
```

验证顺序：

1. 能访问 `http://192.168.2.241:8080/`；
2. DNS 能解析；
3. DIRECT 目标正常；
4. 代理目标正常；
5. UDP 正常；
6. 长连接/视频流正常；
7. 切换 selector 不影响 DNS/网关；
8. 重启 OpenSurge 容器后恢复正常；
9. 重启 NAS 后恢复正常。

只有单设备测试稳定后再增加其他客户端。

## 12. IPv6 警告

Phase 2 v1 不接管下游 IPv6。

如果客户端同时从主路由获得 IPv6，可能发生：

```text
IPv4 -> OpenSurge
IPv6 -> 主路由直接出站
```

因此 Web/doctor 后续会明确检测并提示 IPv6 bypass。不要把“IPv4 走 OpenSurge”错误理解为“双栈全部经过 OpenSurge”。

## 13. 反向代理

如果使用 Lucky/Nginx 给 Web UI 配置域名，需要把域名加入：

```dotenv
OPENSURGE_ALLOWED_HOSTS=opensurge.home.arpa
```

多个名称用逗号分隔。

默认不信任 `X-Forwarded-For` 来决定登录限流来源；trusted-proxy 支持会在明确配置代理地址后再启用，不会默认信任整个 LAN。

## 14. 数据目录

所有长期数据位于 `/data`：

```text
/data/
  config/
  profiles/
  providers/
  runtime/
  state/
  logs/
  backups/
  licenses/
  control/
```

镜像升级不得依赖容器 writable layer 保存配置。

## 15. 当前不支持

Phase 2 v1 暂不包含：

- QPKG；
- downstream IPv6 takeover；
- 自动修改 QNAP Virtual Switch；
- VLAN 管理器；
- cloud control；
- Kubernetes；
- 把 QNAP host network 作为默认数据面。
