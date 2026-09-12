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

启用 Web UI 的 **“让 NAS 使用 OpenSurge”** 后，OpenSurge 只增加自己拥有的 IPv4 policy routing：

```text
NAS 本机 IPv4 公网流量
        ↓
QNAP host policy
        ↓
table 20242
        ↓
192.168.2.241
        ↓
OpenSurge eth0
        ↓
现有 same-LAN 数据面 / tun0 / Mihomo
        ↓
192.168.2.1
```

## 为什么不用修改 QTS 默认网关

QTS Network & Virtual Switch 可能拒绝或覆盖“同一台 NAS 内 QNET 容器 IP”作为宿主默认网关。OpenSurge 因此不修改 QTS 保存的默认网关，而使用 Linux RPDB 只覆盖 NAS 本机产生的 IPv4 公网流量。

普通 NAS 公网流量使用：

```text
pref 24100  iif lo  lookup main suppress_prefixlength 0
pref 24110  iif lo  lookup 20242

table 20242:
  default via 192.168.2.241 dev br0 proto 242
```

其中：

- `iif lo` 只选择 Linux 本机产生的流量，不把 Docker/QNET 转发流量一并接管；
- `lookup main suppress_prefixlength 0` 保留 QTS 已有的 LAN、Docker、静态路由等所有比默认路由更具体的路由；
- table `20242` 只保存 OpenSurge 管理的默认路由，下一跳是 OpenSurge QNET IPv4；
- QTS 原来的 main table 默认路由没有被删除或替换。

因此局域网、QTS 管理面和已有容器本地网络继续使用原来的具体路由，只有没有更具体路由的 NAS 本机 IPv4 公网流量进入 OpenSurge。

## DNS：使用 policy routing，不依赖 nftables/iptables

QNAP 5.10 vendor kernel 可能没有可用的 `nf_tables` netlink 接口，因此 NAS Host Takeover 的 same-LAN 路径不再依赖 nftables，也不要求安装 `iptables` / `iptables-legacy`。

Mihomo TUN 本身已经启用：

```text
dns-hijack:
  - any:53
```

问题在于 NAS 常把 DNS 发给同网段主路由，例如 `192.168.2.1:53`。普通 `24100` 规则会保留 LAN 直连路由，因此需要两级更具体的 L4 policy routing。

### QNAP host namespace

TCP/UDP 53 在普通 host takeover 规则之前进入 table `20242`：

```text
pref 24090  iif lo  ipproto udp  dport 53  lookup 20242
pref 24091  iif lo  ipproto tcp  dport 53  lookup 20242
```

因此即使 NAS DNS 目标仍是 `192.168.2.1`，数据包也先发送给 `192.168.2.241`。

### OpenSurge container namespace

OpenSurge 原来的 same-LAN table `20241` 会保留 LAN CIDR 直连；为了避免 NAS DNS 再从 eth0 直接发回主路由，只针对 NAS 主地址增加两条更高优先级规则：

```text
pref 20239  from <NAS IPv4>/32  iif eth0  ipproto udp  dport 53  lookup 20243
pref 20240  from <NAS IPv4>/32  iif eth0  ipproto tcp  dport 53  lookup 20243

table 20243:
  default dev tun0 proto 243
```

最终路径：

```text
NAS DNS -> 192.168.2.1:53
        ↓
host pref 24090/24091
        ↓
table 20242 -> 192.168.2.241
        ↓
container pref 20239/20240
        ↓
table 20243 -> tun0
        ↓
Mihomo dns-hijack any:53
        ↓
OpenSurge DNS / fake-ip / 域名规则
```

普通 QNET 客户端不匹配 `from <NAS IPv4>/32`，仍然使用原来的 `iif eth0 -> table 20241` 数据面，不受 NAS Host Takeover 的 DNS 专用规则影响。

## 能力预检

在安装 table `20242`、`20243` 或正式策略规则之前，OpenSurge 会先用不可能命中的临时规则验证当前 QNAP 内核是否支持 RPDB 的 `ipproto + dport` 选择器，然后立即精确删除测试规则。

如果内核拒绝 L4 policy routing，启用操作会在修改正式 NAS 路由之前失败，并给出明确错误；不会先把 NAS 指向 `.241` 再因为 DNS 接管失败而留下半配置状态。

同时还会检查：

- host policy priorities `24090`、`24091`、`24100`、`24110` 是否被占用；
- host route table `20242` 是否已有非 OpenSurge 路由；
- container priorities `20239`、`20240` 是否被占用；
- container route table `20243` 是否已有路由；
- `tun0` 是否真实存在。

冲突时拒绝启用，不覆盖现有规则。

## 自动回退

Web 开关保存的是“希望 NAS 使用 OpenSurge”的持久化意图，但真正的 host policy 只在 OpenSurge 数据面处于 ready 状态时存在。

```text
OpenSurge ready
    → 自动安装 NAS host policy + DNS policy routing

OpenSurge 停止 / 数据面异常
    → 先删除 NAS host selectors
    → NAS 自动回到 QTS main table 默认网关
    → 再清理 OpenSurge 自己的 container DNS policy

OpenSurge 再次 ready
    → 如果开关仍为启用，自动恢复
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
- 不执行 `nft flush ruleset`、`ip rule flush` 或全局 `ip route flush`；
- 删除时只操作 OpenSurge 专用 priority、table 和 route protocol；
- 不改写 QTS 网络配置，也不再要求宿主 nf_tables/iptables NAT 能力。

升级到包含此功能的版本后，需要 **使用新版 Compose 重建一次容器**，才能获得只读 host network namespace 挂载。之后开关状态保存在 `/data/control/qnap-host-routing.json`。

## IPv6

该功能严格属于当前 QNAP 的 IPv4-only 产品边界：

- NAS IPv4：可选通过 OpenSurge；
- NAS IPv6：不接管；
- OpenSurge for QNAP 不建立 IPv6 TUN、IPv6 policy routing 或 IPv6 DNS 接管。
