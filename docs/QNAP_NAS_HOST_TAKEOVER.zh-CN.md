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
same-LAN 数据面 / tun0 / Mihomo
        ↓
192.168.2.1
```

## 默认部署不会获得宿主网络命名空间权限

正式版默认 `docker-compose.yml` 只授予 `NET_ADMIN` 和 `NET_RAW`，不会挂载 QNAP host network namespace，也不会授予 `SYS_ADMIN`。因此只把 OpenSurge 作为局域网旁路由时，不需要额外宿主权限。

只有确实需要 NAS Host Takeover 时，再叠加：

```sh
docker compose \
  -f docker-compose.yml \
  -f docker-compose.host-takeover.yml \
  up -d
```

该 override 只增加：

```text
CAP_SYS_ADMIN
/proc/1/ns/net -> /run/opensurge/host-netns (read-only)
```

如果未使用 override，Web 中 NAS Host Takeover 会显示为不支持/不可用，而普通 QNET 网关功能不受影响。

## 为什么不用修改 QTS 默认网关

QTS Network & Virtual Switch 可能拒绝或覆盖“同一台 NAS 内 QNET 容器 IP”作为宿主默认网关。OpenSurge 因此不修改 QTS 保存的默认网关，而使用 Linux RPDB 只覆盖 NAS 本机产生的 IPv4 公网流量。

普通 NAS 公网流量使用：

```text
pref <dynamic-main>   iif lo lookup main suppress_prefixlength 0
pref <dynamic-proxy>  iif lo lookup 20242

table 20242:
  default via 192.168.2.241 dev <QNAP-LAN-iface> proto 242
```

其中：

- `iif lo` 只选择 Linux 本机产生的流量，不把 Docker/QNET 转发流量一并接管；
- `lookup main suppress_prefixlength 0` 保留 QTS 已有的 LAN、Docker、静态路由等所有比默认路由更具体的路由；
- table `20242` 只保存 OpenSurge 管理的默认路由，下一跳是 OpenSurge QNET IPv4；
- QTS 原来的 main table 默认路由没有被删除或替换。

## Host policy priority 是动态分配的

正式版不再假定固定使用 `24090/24091/24100/24110`。OpenSurge 会先读取 QNAP 的 IPv4 RPDB，再为自己选择一段连续、未占用并且能够排在相关 QTS host policy 之前的 priority block。

默认候选为：

```text
19996  DNS UDP
19997  DNS TCP
19998  main suppress
19999  proxy
```

如果 QTS 已经存在会命中 NAS 源地址的规则，例如：

```text
20010: from 192.168.2.240 lookup 20010
```

OpenSurge 会确保自己的规则排在它之前；如果默认候选被占用，会继续向更高优先级方向寻找连续可用位置。选择结果会持久化到：

```text
/data/control/qnap-host-routing.json
```

因此排障时不要依赖固定数字，应以 Web API 状态中的 `host_rule_priorities` 和宿主 `ip rule show` 为准。

## DNS：使用 policy routing，不依赖 nftables/iptables

QNAP 5.10 vendor kernel 可能没有可用的 `nf_tables` netlink 接口，因此 NAS Host Takeover 的 same-LAN 路径不依赖 nftables，也不要求安装 `iptables` / `iptables-legacy`。

Mihomo TUN 启用：

```text
dns-hijack:
  - any:53
```

NAS 常把 DNS 发给同网段主路由，例如 `192.168.2.1:53`。Host namespace 中 TCP/UDP 53 使用动态 priority，在普通 host takeover 规则之前进入 table `20242`：

```text
pref <dynamic-dns-udp> iif lo ipproto udp dport 53 lookup 20242
pref <dynamic-dns-tcp> iif lo ipproto tcp dport 53 lookup 20242
```

OpenSurge container namespace 继续使用专用 DNS 规则：

```text
pref 20239 from <NAS IPv4>/32 iif eth0 ipproto udp dport 53 lookup 20243
pref 20240 from <NAS IPv4>/32 iif eth0 ipproto tcp dport 53 lookup 20243

table 20243:
  default dev tun0 proto 243
```

最终路径：

```text
NAS DNS -> LAN resolver:53
        ↓
host dynamic DNS policy
        ↓
table 20242 -> OpenSurge QNET IP
        ↓
container 20239/20240
        ↓
table 20243 -> tun0
        ↓
Mihomo dns-hijack any:53
```

普通 QNET 客户端不匹配 `from <NAS IPv4>/32`，仍使用原来的 same-LAN 数据面。

## 能力预检与冲突保护

启用前会验证：

- 当前 QNAP 内核支持 RPDB `ipproto + dport`；
- 动态 host priority block 未被占用；
- host route table `20242` 没有非 OpenSurge 路由；
- container priorities `20239`、`20240` 未被占用；
- container route table `20243` 没有非 OpenSurge 路由；
- `tun0` 真实存在；
- 实际 public route 和 DNS route lookup 最终确实选择 OpenSurge。

冲突或实际路由未命中时会拒绝启用并回滚，不覆盖 QTS 或用户已有规则。

## 自动回退

Web 开关保存的是“希望 NAS 使用 OpenSurge”的持久化意图，但实际 host policy 只在 OpenSurge 数据面 ready 时存在：

```text
OpenSurge ready
    → 自动安装 NAS host policy + DNS policy routing

OpenSurge 停止 / 数据面异常
    → 删除 NAS host selectors
    → NAS 回到 QTS 原有默认路由
    → 清理 OpenSurge container DNS policy

OpenSurge 再次 ready
    → 如果开关仍为启用，自动恢复
```

Control 每 5 秒协调一次。正常停止或容器重建时会在退出前主动释放实际 host policy，同时保留用户意图。

强制 `SIGKILL`、宿主机突然断电等无法执行退出清理的情况，仍可能在容器重新启动和协调前造成短暂网络中断；QTS 原始默认路由本身没有被删除。

## 安全边界

Host Takeover override 仍保持以下边界：

- 不挂载 QNAP 根文件系统；
- 不挂载 `/var/run/docker.sock`；
- 不使用 host network 运行数据面；
- 只有 root Control 进程保留宿主网络管理能力；
- LAN Web 进程继续通过 `setpriv` 删除全部 capability；
- host-routing API 不开放给 AI Remote Management Token，只允许已登录 Web 管理员通过内部 Control Token 调用；
- 不执行 `nft flush ruleset`、`ip rule flush` 或全局 `ip route flush`；
- 删除时只操作 OpenSurge 自有 priority、table 和 route protocol；
- 不改写 QTS Network & Virtual Switch 配置。

## IPv6

该功能严格属于当前 QNAP 的 IPv4-only 产品边界：

- NAS IPv4：可选通过 OpenSurge；
- NAS IPv6：不接管；
- 不建立 IPv6 TUN、IPv6 policy routing 或 IPv6 DNS 接管。
