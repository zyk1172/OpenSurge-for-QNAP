# QNAP IPv6 policy

OpenSurge for QNAP is currently an **IPv4-only** product.

The QNAP container image does not support an IPv6 data plane. It does not advertise RA/SLAAC, assign downstream IPv6 addresses, synthesize an operational IPv6 fake-IP path, install IPv6 policy routes, or attempt to become an IPv6 gateway. IPv6 remains entirely under the ISP modem/ONT, main router, and client operating system.

For upgrade compatibility, historical configuration keys such as `dns.ipv6`, `transparent.tun_ipv6`, `transparent.ipv6_shared_l2_ready`, and the old packet-broker fields are still accepted by the parser. On QNAP they are normalized to the disabled state before validation and runtime rendering. Saving the configuration persists the migrated OFF state.

The supported container definitions also disable IPv6 in the container network namespace. This prevents old persistent settings from accidentally recreating one of the retired experimental IPv6 paths.

This boundary is intentional. IPv6 support can be reconsidered later if the upstream project provides a mature data plane that fits QNAP/QNET and can be validated on realistic home-router/ONT deployments without requiring unavailable router features.

## 中文说明

OpenSurge for QNAP 当前明确定位为 **仅 IPv4** 产品。

QNAP 容器镜像不再支持 IPv6 数据面：不发送 RA/SLAAC、不为下游分配 IPv6、不建立可用的 IPv6 fake-IP 接管路径、不安装 IPv6 policy routing，也不尝试成为 IPv6 网关。IPv6 完全交给运营商光猫、主路由和客户端系统处理。

为避免升级后旧 `/data/config/opensurge.yaml` 无法读取，历史字段仍保留解析兼容，例如 `dns.ipv6`、`transparent.tun_ipv6`、`transparent.ipv6_shared_l2_ready` 和旧 packet-broker 字段；但 QNAP 加载配置时会统一迁移为 OFF，之后通过 Web/API 保存会把 OFF 状态持久化。

官方 QNAP Compose 同时在容器 network namespace 中关闭 IPv6，避免旧配置意外恢复已经废弃的实验性 IPv6 路径。

等上游真正提供成熟、适合 QNAP/QNET、并且能够在普通运营商光猫/家用路由器环境可靠验证的 IPv6 数据面后，再重新评估支持。