# OpenSurge for QNAP

OpenSurge for QNAP 是一个运行在 QNAP NAS 上的透明代理网关。它把 Mihomo、DNS、TUN、策略路由和 Web 管理放进一个 Docker 容器里，让局域网设备可以把 QNAP 上的 OpenSurge 当作旁路由使用。

> 当前稳定范围：**IPv4 + QNET + Same-LAN 旁路由**。不自动修改主路由 DHCP，也不接管 IPv6。

[English](README.en.md)

## 能做什么

- 通过 Web 页面管理代理、规则、DNS 和设备策略；
- 支持 Mihomo HTTPS 订阅和本地 YAML；
- 可让不同设备使用不同代理出口；
- 支持连接、流量和规则分析；
- 容器重建后保留配置和恢复状态；
- 可选让 QNAP NAS 自己的 IPv4 流量也经过 OpenSurge。

## 最简单的使用方式

假设你的网络是：

```text
主路由：      192.168.2.1
QNAP：        192.168.2.240
OpenSurge：   192.168.2.241
局域网：      192.168.2.0/24
```

### 1. 准备配置

进入项目的 `deploy/qnap` 目录：

```sh
cp .env.example .env
```

只需要重点修改下面几项：

```text
OPENSURGE_IP=192.168.2.241
OPENSURGE_SUBNET=192.168.2.0/24
OPENSURGE_GATEWAY=192.168.2.1
OPENSURGE_PARENT_INTERFACE=br0
OPENSURGE_DATA_PATH=/share/Container/opensurge
```

其中：

- `OPENSURGE_IP`：给 OpenSurge 单独分配的固定 IP；
- `OPENSURGE_GATEWAY`：你的主路由 IP；
- `OPENSURGE_PARENT_INTERFACE`：QNAP 实际联网的网卡或 Virtual Switch；
- `OPENSURGE_DATA_PATH`：保存配置的目录，升级时不要删除。

不知道父网卡是哪一个，可以先运行：

```sh
sh ./preflight.sh --list-interfaces
```

### 2. 先做预检

```sh
sh ./preflight.sh
```

确认没有关键错误后再启动。

### 3. 启动容器

```sh
docker compose up -d
```

默认 Compose 使用：

```text
zyk1172/opensurge-for-qnap:1.0.0
```

如果 `1.0.0` 正式镜像尚未发布，可以先使用仓库 Releases 中的 `qnap-test-latest` 测试镜像。

### 4. 打开 Web 页面

浏览器访问：

```text
http://OpenSurge-IP:8080
```

例如：

```text
http://192.168.2.241:8080
```

首次创建管理员账户时，需要从容器日志中获取一次性 bootstrap token：

```sh
docker logs opensurge
```

### 5. 导入代理配置

进入 **代理与规则源**，添加：

- HTTPS Mihomo 订阅；或
- 本地 `.yaml` / `.yml` 配置。

导入完成后，在 Web 中启动网关。

### 6. 让设备经过 OpenSurge

先只测试一台设备，把它的网络设置改成：

```text
IPv4 网关 = OpenSurge IP
DNS       = OpenSurge IP
```

例如：

```text
IPv4 网关 = 192.168.2.241
DNS       = 192.168.2.241
```

确认国内直连、代理网站、DNS 和视频播放都正常后，再逐步给其他设备使用。

**不需要先修改全家的 DHCP。**

## 可选：让 QNAP NAS 自己也走 OpenSurge

默认部署不会修改 QNAP 宿主网络。

如果需要让 NAS 本机的 IPv4 公网流量也经过 OpenSurge，使用额外的 Host Takeover 配置：

```sh
docker compose \
  -f docker-compose.yml \
  -f docker-compose.host-takeover.yml \
  up -d
```

然后在 Web 的网络设置中开启 **让 NAS 使用 OpenSurge**。

该模式权限更高，只在确实需要时使用。OpenSurge 不会删除 QTS 原本的默认网关，异常时会尽量自动回退。

详细说明见：[NAS Host Takeover](docs/QNAP_NAS_HOST_TAKEOVER.zh-CN.md)

## 更新

更新时保留原来的 `/data` 目录即可：

```sh
docker compose pull
docker compose up -d
```

不要删除：

```text
/share/Container/opensurge
```

这里保存了账户、配置、订阅和恢复状态。

## 常用排查命令

```sh
docker ps --filter name=opensurge
docker logs --tail 200 opensurge
docker inspect --format '{{json .State.Health}}' opensurge
```

如果 Web 能打开但网关无法启动，优先进入 **诊断 → 运行 Doctor** 查看具体原因。

## 当前限制

- 仅支持 IPv4 数据面；
- 不自动修改主路由 DHCP；
- 不自动修改 QTS「网络与虚拟交换机」；
- 修改 QNET 父接口、OpenSurge 固定 IP、网段或主路由地址后，需要重建容器；
- ARM64 镜像可以构建，但不同 QNAP ARM 机型的 QNET 和内核兼容性可能不同。

## 更多说明

- [QNAP 部署指南](deploy/qnap/README.zh-CN.md)
- [Web 使用指南](docs/app-user-guide.zh-CN.md)
- [常见问题](docs/faq.zh-CN.md)
- [NAS Host Takeover](docs/QNAP_NAS_HOST_TAKEOVER.zh-CN.md)
- [持久化说明](deploy/qnap/PERSISTENCE.md)

## 项目来源

OpenSurge for QNAP 基于 [OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac) 派生，并针对 QNAP、QNET 和单容器部署进行了适配。

本项目不是上游作者官方提供或背书的 QNAP 版本。

许可证：`GPL-3.0-only`
