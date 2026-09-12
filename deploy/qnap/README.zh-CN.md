# OpenSurge for QNAP 部署指南

这份说明只讲 **QNAP 上最常用、最稳妥的部署方式**：让 OpenSurge 作为局域网旁路由使用。

当前支持范围：**IPv4 + QNET + Same-LAN**。

## 一、准备条件

需要：

- QNAP 已安装 Container Station / Docker；
- NAS 能创建 QNET 网络；
- `/dev/net/tun` 可用；
- 给 OpenSurge 准备一个不会冲突的固定 IPv4；
- 准备一个持久化目录，例如 `/share/Container/opensurge`。

示例网络：

```text
主路由：      192.168.2.1
QNAP：        192.168.2.240
OpenSurge：   192.168.2.241
局域网：      192.168.2.0/24
```

## 二、复制配置

进入 `deploy/qnap`：

```sh
cp .env.example .env
```

编辑 `.env`，重点确认：

```text
OPENSURGE_IP=192.168.2.241
OPENSURGE_SUBNET=192.168.2.0/24
OPENSURGE_GATEWAY=192.168.2.1
OPENSURGE_PARENT_INTERFACE=br0
OPENSURGE_DATA_PATH=/share/Container/opensurge
OPENSURGE_WEB_UID=1000
OPENSURGE_WEB_GID=100
```

含义：

| 参数 | 怎么填 |
|---|---|
| `OPENSURGE_IP` | OpenSurge 的固定局域网 IP |
| `OPENSURGE_SUBNET` | 你的局域网网段 |
| `OPENSURGE_GATEWAY` | 主路由 IP |
| `OPENSURGE_PARENT_INTERFACE` | QNAP 实际联网的网卡 / Virtual Switch |
| `OPENSURGE_DATA_PATH` | 配置保存目录 |
| `OPENSURGE_WEB_UID/GID` | 拥有持久化目录写权限的 QNAP 用户 UID/GID |

不知道 QNET 父接口时先运行：

```sh
sh ./preflight.sh --list-interfaces
```

UID/GID 可以在 QNAP 上查看：

```sh
id <你的QNAP用户名>
```

不要为了省事把目录递归改成 `777`。

## 三、运行预检

```sh
sh ./preflight.sh
```

预检会检查 Docker、Compose、QNET、TUN、网卡、IP 配置和 `/data` 写权限。

出现关键错误时先修复，不建议直接跳过。

## 四、启动 OpenSurge

```sh
docker compose up -d
```

查看状态：

```sh
docker ps --filter name=opensurge
docker logs --tail 100 opensurge
```

默认镜像：

```text
zyk1172/opensurge-for-qnap:1.0.0
```

如果 `1.0.0` 尚未正式发布，可先使用仓库 Releases 中的 `qnap-test-latest` 测试镜像。

## 五、第一次打开 Web

浏览器访问：

```text
http://<OpenSurge-IP>:8080
```

例如：

```text
http://192.168.2.241:8080
```

首次创建管理员账户需要一次性 bootstrap token，可从日志中找到：

```sh
docker logs opensurge
```

创建管理员后，之后的代理、DNS、策略和设备设置都在 Web 中完成。

## 六、导入代理配置并启动网关

在 Web 中进入 **代理与规则源**，添加：

- HTTPS Mihomo 订阅；或
- 本地 `.yaml` / `.yml` 配置。

然后进入 **网络设置**，启动网关。

如果启动失败，进入 **诊断 → 运行 Doctor**。

## 七、让一台设备先试用

先不要动主路由 DHCP。

只选一台手机、电脑或电视，把：

```text
IPv4 网关 = OpenSurge IP
DNS       = OpenSurge IP
```

例如：

```text
IPv4 网关 = 192.168.2.241
DNS       = 192.168.2.241
```

确认直连网站、代理网站、DNS、视频和下载都正常后，再逐步给其他设备使用。

## 八、可选：让 QNAP NAS 自己也走 OpenSurge

默认部署不会接管 QNAP 宿主网络。

需要时使用额外配置：

```sh
docker compose \
  -f docker-compose.yml \
  -f docker-compose.host-takeover.yml \
  up -d
```

然后在 Web 中开启 **让 NAS 使用 OpenSurge**。

这个模式会额外授予 `SYS_ADMIN`，并读取 QNAP host network namespace，因此只在确实需要时启用。

OpenSurge 不会删除 QTS 原来的默认网关；当接管条件不满足时，会尝试自动回退到 QTS 原路径。

详细说明：[`../../docs/QNAP_NAS_HOST_TAKEOVER.zh-CN.md`](../../docs/QNAP_NAS_HOST_TAKEOVER.zh-CN.md)

## 九、更新

只要一直保留同一个持久化目录即可：

```text
/share/Container/opensurge -> /data
```

更新镜像：

```sh
docker compose pull
docker compose up -d
```

不要删除整个 `/data`，否则账户、配置、订阅和恢复状态都会丢失。

## 十、需要改 IP 或网卡怎么办

下面这些值属于容器创建时配置：

- QNET 父接口；
- OpenSurge 固定 IP；
- 网段；
- 主路由地址。

要修改时：

1. 保留 `/data`；
2. 修改 `.env`；
3. 重新创建容器。

已有 `/data/config/opensurge.yaml` 不会因为重建容器自动被覆盖。

## 十一、常用排查

```sh
docker ps --filter name=opensurge
docker logs --tail 200 opensurge
docker inspect --format '{{json .State.Health}}' opensurge
docker exec opensurge ip -br addr
docker exec opensurge ip route
docker exec opensurge ip rule
```

部分 QNAP 内核不支持可用的 `nf_tables`。在 Same-LAN 模式下，`nft list ruleset` 失败 **不等于 OpenSurge 网关失败**，应以 Web 状态、TUN、`ip rule` 和实际联网结果为准。

## 十二、当前限制

- 仅支持 IPv4 数据面；
- 不自动修改主路由 DHCP；
- 不自动修改 QTS「网络与虚拟交换机」；
- 不提供 QPKG；
- ARM64 镜像可构建，但不同 QNAP ARM 型号的 QNET / 内核兼容性可能不同。

日常使用说明见：[Web 使用指南](../../docs/app-user-guide.zh-CN.md)
