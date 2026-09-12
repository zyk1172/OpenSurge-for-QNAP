# QNAP NAS 主机通过 OpenSurge 上网

OpenSurge for QNAP 可以让 **NAS 宿主机自己产生的 IPv4 公网流量** 通过同一台 NAS 上的 OpenSurge QNET 容器转发，而不要求 QTS 把容器 IP 保存成系统默认网关。

典型拓扑：

```text
主路由             192.168.2.1
QNAP NAS            192.168.2.240
OpenSurge/QNET      192.168.2.241
```

QTS 仍然保留：

```text
default via 192.168.2.1
```

启用 Web UI 的 **“让 NAS 使用 OpenSurge”** 后，OpenSurge 只在 QNAP 宿主网络命名空间增加自己拥有的 IPv4 policy routing：

```text
NAS 本机 IPv4 公网流量
        ↓
OpenSurge host policy
        ↓
table 20242
        ↓
192.168.2.241
        ↓
OpenSurge TUN / Mihomo
        ↓
192.168.2.1
```

## 为什么不用修改 QTS 默认网关

QTS Network & Virtual Switch 可能拒绝或覆盖“同一台 NAS 内 QNET 容器 IP”作为宿主默认网关。OpenSurge 因此不修改 QTS 保存的默认网关，而使用 Linux RPDB 只覆盖 NAS 本机产生的 IPv4 公网流量。

规则顺序为：

```text
pref 24100  iif lo  lookup main suppress_prefixlength 0
pref 24110  iif lo  lookup 20242
```

其中：

- `iif lo` 只选择 Linux 本机产生的流量，不把 Docker/QNET 转发流量一并接管；
- `lookup main suppress_prefixlength 0` 保留 QTS 已有的 LAN、Docker、静态路由等所有比默认路由更具体的路由；
- table `20242` 只保存一个 OpenSurge 管理的默认路由，下一跳是 OpenSurge QNET IPv4；
- QTS 原来的 main table 默认路由没有被删除或替换。

因此局域网、QTS 管理面和已有容器本地网络继续使用原来的具体路由，只有没有更具体路由的 NAS 本机 IPv4 公网流量进入 OpenSurge。

## DNS

启用 NAS 主机接管时，OpenSurge 在 QNAP host network namespace 创建独立 nftables 表：

```text
opensurge_nas_host
```

NAS 本机发出的 IPv4 TCP/UDP 53 端口请求会被定向到 OpenSurge IPv4 DNS。这样 fake-IP、域名规则和 OpenSurge 的 DNS 路径能够与 NAS 本机流量保持一致。

禁用功能时会删除整个 `opensurge_nas_host` 表，不修改 QTS 自己的 DNS 配置文件。

## 自动回退

Web 开关保存的是“希望 NAS 使用 OpenSurge”的持久化意图，但真正的 host policy 只在 OpenSurge 数据面处于 ready 状态时存在。

```text
OpenSurge ready
    → 自动安装 NAS host policy

OpenSurge 停止 / 数据面异常
    → 删除 NAS host policy
    → NAS 自动回到 QTS main table 默认网关

OpenSurge 再次 ready
    → 如果开关仍为启用，自动恢复 host policy
```

Control 进程每 5 秒对状态进行一次协调。

正常停止或重建容器时，Control 进程退出前会主动释放实际 host policy，同时保留用户的启用意图。新容器恢复并通过 readiness 后可以再次接管。

> 强制 `SIGKILL`、宿主机突然断电等无法执行退出清理的情况，仍可能在容器重新启动和协调前造成短暂网络中断；QTS 原始默认路由本身没有被删除。

## 安全边界

为操作 QNAP host network namespace，当前 Compose 增加：

```text
/proc/1/ns/net -> /run/opensurge/host-netns (read-only)
CAP_SYS_ADMIN
```

范围被刻意限制为：

- 不挂载 QNAP 根文件系统；
- 不挂载 `/var/run/docker.sock`；
- 不使用 host network 运行 OpenSurge 数据面；
- 只有容器内 root Control 进程保留网络管理能力；
- LAN Web 进程仍通过 `setpriv` 删除全部 capability；
- 该 host-routing API 不开放给局域网 AI Remote Management Token，只允许已登录 Web 管理员通过内部 Control Token 调用；
- 删除时只操作 OpenSurge 专用 table/rule/nft table，不改写 QTS 网络配置。

升级到包含此功能的版本后，需要 **使用新版 Compose 重建一次容器**，才能获得只读 host network namespace 挂载。之后开关状态保存在 `/data/control/qnap-host-routing.json`。

## IPv6

该功能严格属于当前 QNAP 的 IPv4-only 产品边界：

- NAS IPv4：可选通过 OpenSurge；
- NAS IPv6：不接管；
- OpenSurge for QNAP 不建立 IPv6 TUN、IPv6 policy routing 或 IPv6 DNS 接管。
