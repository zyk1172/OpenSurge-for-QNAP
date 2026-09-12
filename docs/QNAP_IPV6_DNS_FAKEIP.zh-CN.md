# QNAP IPv6 DNS / fake-IP 定向接管

本文描述 OpenSurge for QNAP 在 `same_lan` 下的 IPv6 工作方式。

这套方案**不把 OpenSurge 设成局域网设备的 IPv6 默认网关**，也**不由 OpenSurge 发送 RA / SLAAC**。客户端继续使用主路由提供的原生 IPv6、公网 IPv6 地址和默认路由；只有 Mihomo DNS 生成的 IPv6 fake-IP 网段会被主路由静态路由到 OpenSurge。

## 目标

```text
普通 IPv6
客户端 ───────────────► 主路由 ─────────► Internet
  │                      ▲
  │ 原有 RA / GUA /      │
  │ 默认路由保持不变      │
  │
  │ DNS 查询
  ▼
主路由 DNS
  │
  │ 上游 DNS = OpenSurge IPv4
  ▼
OpenSurge / Mihomo DNS
  │
  └─ 需要 fake-IP 的 AAAA → fdfe:dcba:9876::/64
                               │
                               │ 主路由 IPv6 静态路由
                               ▼
                           OpenSurge
                               │
                               ▼
                              TUN
                               │
                         DIRECT / PROXY / REJECT
```

因此：

- 手机、电视、电脑不需要手动填写 IPv6 地址或网关；
- 主路由原来的 IPv6 Bridge / Passthrough、RA、DHCPv6 和公网 IPv6 分配可以继续保持；
- 普通公网 IPv6 不经过 OpenSurge；
- 只有 `fdfe:dcba:9876::/64` 进入 OpenSurge TUN；
- QNAP 宿主默认路由、QTS Network & Virtual Switch 不会被 OpenSurge 修改；
- IPv4 数据面保持项目原来的实现，本功能不新增 IPv4 路由。

## 地址规划

| 用途 | 地址 / 网段 |
|---|---|
| Mihomo IPv6 fake-IP | `fdfe:dcba:9876::/64` |
| Mihomo TUN IPv6 | `fdfe:dcba:9877::1/126` |
| 兼容旧配置的 OpenSurge ULA | `fdfe:dcba:9878::1/64` |
| 主路由静态路由下一跳 | OpenSurge 稳定 link-local IPv6 |

旧的 `fdfe:dcba:9878::/64` 不再是 QNAP same-LAN 的客户端接管网段。它仅为了旧配置/其他拓扑兼容而保留，客户端不需要使用它作为默认网关。

## 稳定 link-local 下一跳

容器重建后内核自动生成的 link-local IPv6 可能变化。OpenSurge 因此根据容器的固定 IPv4 额外生成一个稳定 link-local 地址：

```text
IPv4: 192.168.2.241
                 ↓
link-local: fe80::1:0:c0a8:2f1
```

算法：

```text
192.168 -> c0a8
2.241   -> 02f1 -> 2f1

fe80::1:0:c0a8:2f1
```

Web 的 QNAP 网络页会根据当前 OpenSurge IPv4 显示实际要填写的下一跳。

如果主路由要求为 link-local 下一跳指定接口，请选择与 QNAP/OpenSurge 位于同一 LAN 的桥/LAN 接口。

## 主路由需要配置的两项内容

假设：

```text
主路由 IPv4      = 192.168.2.1
OpenSurge IPv4   = 192.168.2.241
OpenSurge next-hop = fe80::1:0:c0a8:2f1
```

### 1. DNS 上游

让主路由用于局域网客户端的 DNS 查询进入 OpenSurge：

```text
DNS upstream = 192.168.2.241
```

这里使用的是 OpenSurge 的 IPv4 DNS 地址，仅用于 DNS 传输；这不意味着修改客户端 IPv4 默认网关。

### 2. IPv6 静态路由

添加：

```text
目标网段：fdfe:dcba:9876::/64
下一跳：  fe80::1:0:c0a8:2f1
接口：    LAN / bridge
```

不同路由器的界面名称可能是“IPv6 静态路由”“路由表”“下一跳”“网关”“设备/接口”等。

不要把 `::/0` 指向 OpenSurge。只需要把 fake-IP `/64` 送到 OpenSurge。

## 主路由 IPv6 不需要关闭

与早期 QNAP IPv6 设计不同，本模式**不要求关闭主路由 RA**。

继续保留：

```text
IPv6 RA              ON
DHCPv6                按原配置
IPv6 Bridge/Passthrough 按原配置
公网 IPv6 Prefix      按原配置
客户端默认 IPv6 Router 主路由
```

OpenSurge 不再和主路由竞争 IPv6 默认路由。

## OpenSurge 内部路由

启用后，Linux 只安装 fake-IP IPv6 选择器：

```text
ip -6 rule
  to fdfe:dcba:9876::/64 lookup 20241

IPv6 table 20241
  fdfe:dcba:9876::/64 dev tun0
```

同时保留 fail-closed guard：如果 OpenSurge 的 fake-IP TUN 路由丢失，`fdfe:dcba:9876::/64` 不会回落到主路由默认路径。

与旧实现不同，不再存在：

```text
ip -6 rule iif eth0 lookup 20241
IPv6 table 20241 default dev tun0
```

因此普通 IPv6 不会因为 OpenSurge 启用而被整体接管。

## 为什么容器需要 `accept_ra=2`

OpenSurge 容器仍需要 IPv6 forwarding 才能把 fake-IP 流量转入 TUN。

Linux 在 forwarding=1 时默认可能不接受 Router Advertisement，所以 Compose 明确设置：

```text
net.ipv6.conf.eth0.accept_ra=2
net.ipv6.conf.eth0.autoconf=1
```

这样容器在作为转发节点的同时仍能学习主路由的原生 IPv6 路由/前缀，用于正常 IPv6 回程和本机 IPv6 可达性。

## 设备策略边界

QNAP 的原生 Linux TUN 是三层入口，包进入 TUN 后没有原始 Ethernet source MAC。

因此这套 IPv6 路径的保证目标是：

```text
DNS fake-IP → 全局 Mihomo rules → DIRECT / PROXY / REJECT
```

而不是逐设备 IPv6 身份。

早期版本根据固定 IPv4 推导 `fdfe:dcba:9878::/64` 中的固定 ULA，再用 `SRC-IP-CIDR6` 模拟设备身份；该机制在当前 same-LAN 模式中已取消。

IPv4 设备策略不受这次改动影响。

## IPv4 A 查询的重要说明

本功能本身**不新增或修改 IPv4 路由**，但 OpenSurge 当前使用 Mihomo 统一 `fake-ip` DNS。该 DNS 在处理 A 查询时仍可能返回 IPv4 fake-IP。

因此如果主路由把 OpenSurge DNS 全局提供给所有客户端，需要确认以下至少一项成立：

- 这些客户端现有的 IPv4 OpenSurge/fake-IP 数据面本来就可达；或
- 主路由有合适的 DNS 分流/策略，避免让不经过 OpenSurge IPv4 数据面的客户端拿到不可达的 IPv4 fake-IP；或
- 已通过真机测试确认客户端的实际解析/连接路径符合预期。

这次 IPv6 改造不会自动增加 `198.18.0.0/16` 的 IPv4 静态路由，因为目标是保持 IPv4 数据面不变。

## Web 设置

QNAP Web → 网络 → **IPv6 DNS 定向接管**：

1. 开启“IPv6 DNS / fake-IP 定向接管”；
2. Web 显示：
   - 主路由 DNS 上游；
   - `fdfe:dcba:9876::/64`；
   - 当前 OpenSurge 的稳定 link-local 下一跳；
   - LAN 接口；
3. 在主路由完成 DNS + IPv6 静态路由；
4. 勾选“主路由 DNS 和 IPv6 静态路由已配置”；
5. 保存并重启 Gateway。

Web 内部会同时启用：

```yaml
dns:
  ipv6: true

transparent:
  tun_ipv6: "always"
  ipv6_shared_l2_ready: true
```

`ipv6_shared_l2_ready` 名称为了旧配置/API 兼容暂时保留；在 QNAP `same_lan` 中，其语义现在是“主路由 DNS 与 fake-IP IPv6 静态路由已准备好”。

## 验证

启动后先检查容器：

```sh
docker exec opensurge ip -6 addr show dev eth0
docker exec opensurge cat /proc/sys/net/ipv6/conf/eth0/accept_ra
docker exec opensurge ip -6 rule show
docker exec opensurge ip -6 route show table 20241
```

预期：

```text
accept_ra = 2
存在 fe80::1:0:... 的稳定下一跳
存在 to fdfe:dcba:9876::/64 的 policy rule
20241 表只包含 fdfe:dcba:9876::/64 → tun0 的 IPv6 路由
```

客户端验证：

1. IPv6 地址仍是主路由/运营商提供的公网地址；
2. IPv6 默认路由仍是主路由；
3. 国内/普通 IPv6 不应因为 OpenSurge 开启而整体改道；
4. 由 OpenSurge DNS 返回 fake IPv6 的目标应进入 OpenSurge；
5. PROXY/DIRECT/REJECT 按 Mihomo 全局规则生效；
6. `docker restart opensurge` 后稳定 link-local 和 fake-IP 路由恢复；
7. NAS reboot 后重复验证。

## 回滚

关闭 Web 中的“IPv6 DNS / fake-IP 定向接管”，然后在主路由：

1. 恢复原 DNS 上游；
2. 删除 `fdfe:dcba:9876::/64` 静态路由。

客户端原来的 RA、公网 IPv6 和默认路由从未被 OpenSurge 替换，所以不需要逐台恢复 IPv6 设置。
