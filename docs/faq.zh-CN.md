# OpenSurge for QNAP 常见问题

完整安装与日常使用流程见 [OpenSurge for QNAP 使用指南](app-user-guide.zh-CN.md) 和 [QNAP Docker 部署指南](../deploy/qnap/README.zh-CN.md)。

## 为什么 QNAP 版只有一个容器？

当前默认架构把 Web、Control API、mihomo、dnsmasq、TUN 和策略路由集中在一个 `opensurge` 容器中。

QNET、静态 IP 和父网卡必须在创建容器时确定，因此默认架构不再使用额外 Manager/Orchestrator 容器，也不需要把 Docker Socket 暴露给 Web。

## 为什么容器里只看到 eth0，而我选择的是 br0 / eth1？

这是正常现象。

- `br0` / `eth1` 等是 QNAP 宿主侧 QNET 父接口；
- `eth0` 是容器 network namespace 内的接口。

两者不要求同名。物理网卡选择发生在创建 QNET 网络时。

## 为什么 Web 里不能修改 QNET 父网卡或静态 IP？

它们是 Docker/QNET 创建参数，不是 OpenSurge 运行配置。

需要修改时：

1. 停止 Gateway；
2. 保留 `/data`；
3. 修改 Compose 创建参数；
4. 重建 `opensurge` 容器。

Web 只管理能够安全持久化到 OpenSurge 配置中的运行参数。

## QNAP 缺少 nftables，是否就不能透明代理？

不一定。

当前 same-LAN QNAP 后端可以在 TUN、`NET_ADMIN` 和 Linux policy routing 可用时使用：

```text
ip rule iif <LAN interface>
→ OpenSurge dedicated table
→ tun0
→ mihomo
```

因此 same-LAN 模式不要求 `nf_tables`。

需要 NAT 的隔离下游拓扑仍可能要求 nftables/fwmark 能力。

## `/dev/net/tun` 不存在怎么办？

先区分“宿主没有 TUN 驱动”和“容器没有映射设备”。

宿主检查：

```sh
ls -l /dev/net/tun
grep -w tun /proc/misc
```

如果宿主 TUN 正常，Compose 仍必须包含：

```yaml
devices:
  - /dev/net/tun:/dev/net/tun
```

以及 `NET_ADMIN` / `NET_RAW`。

如果宿主内核本身没有 TUN 支持，Docker 镜像无法凭空补出宿主内核能力。

## 为什么 `nft list ruleset` 报错，但 Gateway 仍可能正常？

部分 QNAP 5.10 内核没有 `nf_tables` netlink 支持。在 same-LAN nft-free TUN 模式中，nftables 本来就不是必需的数据面组件。

应检查：

```sh
ip rule show
ip route show table 20241
```

以及 TUN / Mihomo / DNS 实际状态，而不是仅根据 `nft` 命令判断 Gateway 是否可用。

## 导入订阅后为什么没有立即生效？

导入只创建草稿。

网关停止时使用 **设为下次启动版本**；网关运行时使用 **应用并重载**。

Web 会在后端返回后重新读取持久化 sources 状态。只有确认 `desired=true` 或 `applied=true` 才显示成功。

## “设为下次启动版本”会不会因为容器重启而丢失？

正常情况下不会。选择状态和相关配置都保存在 `/data`。

因此升级/重建容器时必须继续挂载同一个持久化目录。

## 为什么保存运行参数时 Gateway 会短暂停止？

运行中的网络参数不能只修改内存状态。当前 Web 流程是：

```text
停止 Gateway
→ 保存 /data/config/opensurge.yaml
→ 重新读取 revision 校验
→ 重新启动 Gateway
```

这样可以避免页面显示成功但实际配置没有落盘。

## NAS 或容器异常重启后为什么显示 interrupted？

OpenSurge 不会把上一次 network namespace 的 runtime 状态直接当成当前仍然有效。

先点击 **安全清理旧状态**。清理依据持久化 snapshot/journal 和 ownership 信息，只处理能够确认属于 OpenSurge 的对象，然后再启动新的 Gateway。

## 为什么不自动修改 QTS 的默认网关、DNS 或 Virtual Switch？

这是安全边界。

OpenSurge Gateway 本身需要网络管理能力，但 QNAP Web 不应该同时获得对整个 NAS 管理网络的任意修改权限。QNET 父接口等宿主参数由部署阶段决定；运行时只管理容器自己的数据面。

## NAS 需要安装 Go、Node.js 或 gcc 吗？

不需要。

测试镜像由 GitHub Actions 预构建。NAS 只需要 Docker/Container Station、QNET、TUN 和必要的内核网络能力。

## 为什么不用 NAS 自己 build 镜像？

QNAP NAS 不是构建服务器。正常测试流程是：

```text
GitHub Actions 构建
→ 下载 amd64/arm64 tar.gz
→ SHA256 校验
→ docker load
→ 重建容器
```

这样更快，也避免在性能较弱的 NAS 上安装开发依赖和长时间编译。

## 如何安全升级测试镜像？

1. 备份当前镜像 tag；
2. 下载并校验新测试镜像；
3. `docker load`；
4. 保持同一个 `/data` 和 QNET 创建参数；
5. 重建 `opensurge`；
6. 检查 Web、配置、订阅和管理员状态；
7. 再启动 Gateway。

不要把删除 `/data` 当成升级步骤。

## 第一阶段应该如何测试客户端？

只测试一台设备：

```text
IPv4 Gateway = OpenSurge IP
DNS          = OpenSurge IP
```

主路由 DHCP 暂时保持原状。确认 DNS、DIRECT、PROXY、UDP/QUIC、长连接和下载都稳定后，再增加客户端。
