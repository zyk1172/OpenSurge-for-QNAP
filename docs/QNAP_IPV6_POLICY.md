# QNAP IPv6 policy

OpenSurge for QNAP is currently an **IPv4-only** product.

The QNAP container image does not support an IPv6 data plane. It does not advertise RA/SLAAC, assign downstream IPv6 addresses, synthesize an operational IPv6 fake-IP path, install IPv6 policy routes, or attempt to become an IPv6 gateway. IPv6 remains entirely under the ISP modem/ONT, main router, and client operating system.

Historical IPv6 configuration fields remain in the schema only for upgrade/API compatibility:

- `transparent.tun_ipv6: auto/always` is explicitly rejected on QNAP. `off` is the only supported runtime state.
- old packet-broker/native-Linux markers and shared-L2 readiness state are discarded during normalization, so an old experimental path cannot be revived by a stale config.
- `dns.ipv6` may still be parsed or preserved by legacy API/config flows, but when TUN IPv6 is `off` the QNAP Mihomo manager suppresses it before rendering the runtime configuration. The current QNAP Web UI always saves it as `false`.

The supported container definitions also set `disable_ipv6=1` in the container network namespace. This gives the image a second, independent boundary even if a stale compatibility field remains in persistent configuration.

This boundary is intentional. IPv6 support can be reconsidered later if the upstream project provides a mature data plane that fits QNAP/QNET and can be validated on realistic home-router/ONT deployments without requiring unavailable router features.

## 中文说明

OpenSurge for QNAP 当前明确定位为 **仅 IPv4** 产品。

QNAP 容器镜像不再支持 IPv6 数据面：不发送 RA/SLAAC、不为下游分配 IPv6、不建立可用的 IPv6 fake-IP 接管路径、不安装 IPv6 policy routing，也不尝试成为 IPv6 网关。IPv6 完全交给运营商光猫、主路由和客户端系统处理。

历史 IPv6 字段只保留用于升级/API 兼容：

- `transparent.tun_ipv6: auto/always` 在 QNAP 上会明确拒绝，唯一支持的运行状态是 `off`；
- 旧 packet-broker/native-Linux 标记以及 shared-L2 readiness 会在 Normalize 阶段被清除，旧实验性路径不能靠遗留配置重新启用；
- `dns.ipv6` 仍允许旧配置或旧 API 读取/保留，但当 TUN IPv6 为 `off` 时，QNAP Mihomo Manager 会在生成真正的运行配置前强制屏蔽它；当前 QNAP Web 保存配置时始终写入 `false`。

官方 QNAP Compose 同时在容器 network namespace 中设置 `disable_ipv6=1`，形成第二层独立边界，即使持久化配置里仍残留兼容字段，也无法恢复 IPv6 数据面。

等上游真正提供成熟、适合 QNAP/QNET、并且能够在普通运营商光猫/家用路由器环境可靠验证的 IPv6 数据面后，再重新评估支持。