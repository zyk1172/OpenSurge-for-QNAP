# Upstream Relationship

本文件记录 OpenSurge for QNAP 与上游 OpenSurge for Mac 的派生关系、同步策略与平台差异。
**本项目不隐藏、不弱化上游来源。**

## 1. 上游信息

| 项目 | 值 |
| --- | --- |
| Original project | OpenSurge for Mac |
| Original author / organization | YTwsy |
| Original repository | <https://github.com/YTwsy/OpenSurge-for-Mac> |
| Upstream license | `GPL-3.0-only` |
| Upstream default branch | `master` |

## 2. Fork 信息

| 项目 | 值 |
| --- | --- |
| Fork repository | <https://github.com/zyk1172/OpenSurge-for-QNAP> |
| Fork baseline commit | `b03bf2f8a2b02a6fffba9c879ce1980ddb831a67` |
| Baseline upstream ref | `master` / v0.2.2 |
| Baseline commit date | 2026-09-07 |
| Fork date | 2026-09-09 |
| Original porting branch | `port/qnap-linux-docker` |
| Product name | **OpenSurge for QNAP** |
| Repository name | `OpenSurge-for-QNAP` |
| License | `GPL-3.0-only`（继承上游，未修改） |

截至 2026-09-10，本 fork 的 baseline 仍与上游 `master` / v0.2.2 最新稳定基线一致；后续仍按下述人工同步策略处理，不自动 merge macOS 产品改动。

## 3. 命名与归属边界

可以修改的是本 fork 的产品名称、QNAP/Linux 文案和派生实现。

必须保留或明确说明的是：

- upstream Git 历史与作者信息；
- 上游仓库、fork baseline 和派生关系；
- 第三方项目名称与许可证；
- 上游及第三方必须保留的 copyright/license 文本；
- Go module 等历史内部命名仅在确有工程价值时迁移，避免为改名制造无意义大 diff。

## 4. Upstream 同步策略

1. **不自动 merge。** 上游仍以 macOS 产品为目标，自动同步可能重新引入已删除的平台耦合。
2. **人工挑选平台无关改动。** 优先考虑：
   - profile / provider / policy / config 修复；
   - device policy 修复；
   - Web UI 与前端测试；
   - 平台无关的诊断、测试和文档；
   - Tailscale / Headscale 等可跨平台抽象的能力。
3. macOS 专属的 `pf`、AppKit/SwiftUI、launchd、BPF IPv6、PKG/notarization 改动不直接 cherry-pick。
4. 每次同步都应记录原始 upstream commit，便于审计和后续追踪。

## 5. 当前已经实现的主要差异

| 维度 | 上游（macOS） | 当前 QNAP/Linux fork |
| --- | --- | --- |
| 产品壳 | macOS 菜单栏 App + Web | QNAP 单容器 + Web；桌面菜单栏/PKG 路径已删除 |
| 防火墙 / NAT | `pf` | 隔离/NAT 拓扑使用 `nftables`；Same-LAN 不强依赖 nftables |
| Same-LAN 路由 | macOS 路由 / mihomo TUN | `ip rule iif` + 专用 routing table + mihomo TUN |
| forwarding | macOS sysctl | Linux `/proc/sys/net/ipv4/ip_forward` |
| TUN | `utun*` | `/dev/net/tun` / `tun0` |
| IPv6 takeover | macOS BPF + patched mihomo | v1 明确拒绝/不支持 downstream IPv6 takeover |
| 本机系统代理 | `networksetup` | QNAP/Linux 明确拒绝该 macOS-only 配置 |
| Stop/rollback | 上游运行时状态 | 持久化 cleanup recipe + network namespace ownership |
| 崩溃窗口 | 上游 boot/PID 机制 | 增加 write-ahead cleanup journal |
| 状态持久化 | 原子 state 写入 | temp + file fsync + rename + parent-dir fsync |
| QNAP Web 安全 | 不适用 | LAN Web + Argon2id + session + Origin/CSRF + bootstrap token + 登录限流 |
| 权限边界 | macOS 本机进程 | Control 保留网络权限；Web 以可配置 UID/GID 降权并清空 capabilities |
| QNAP bind mount | 不适用 | `/share/... -> /data` + 容器内真实权限/ACL 预检 |
| Docker 发布 | 不适用 | 固定基础镜像/第三方 SHA、amd64/arm64 构建、SBOM、provenance、checksums |
| Web | macOS/Web 共用 | QNAP build 过滤桌面能力，并增加 QNAP 专属响应式和环境诊断 |
| CI | 上游 CI | QNAP Compose、Web、权限边界、安全、持久化、数据面与镜像发布 CI |

## 6. 仍未完成或仍属于 stable gate 的内容

以下内容**不得写成已经完成稳定验证**：

- QNAP 不同型号/不同 QTS/QuTS hero 版本的广泛真机矩阵；
- ARM64 QNAP 的充分真机覆盖（当前可构建，不等于已全面验证）；
- 真实客户端覆盖后的 NAS reboot、Container Station restart、24h / 72h soak；
- 磁盘满、ACL 变更、持久化目录只读等系统性故障注入矩阵；
- 自动配置备份/版本 Diff/一键恢复的完整产品化；
- DHCP takeover；
- downstream IPv6 takeover；
- 对未来 mihomo Linux eBPF transparent inbound 的稳定采用。

Dockerfile / Compose、LAN Web 认证、Argon2id、Session、CSRF、登录限流、`/data` 最终布局、日志轮转、SBOM/provenance 等已经进入当前实现，不再列为“未来能力”。

## 7. QNAP 权限原则

QNAP 不是“固定 1000:1000”的平台。项目当前给出的默认值是：

```text
OPENSURGE_WEB_UID=1000
OPENSURGE_WEB_GID=100
```

这是常见 QNAP 用户/`everyone` 组的实用默认值，不是协议要求。部署必须以 NAS 上 `id <username>` 的数字 UID/GID 与共享文件夹 ACL 为准。

项目不得用以下方式掩盖权限问题：

- `chmod -R 777`；
- 对整个 `/data` 做递归 `chown`；
- 只通过宿主 `[ -w PATH ]` 就宣称 bind mount 可用。

支持路径要求 `deploy/qnap/preflight.sh` 使用实际镜像和实际 bind mount 做容器内权限语义验证。

## 8. 保留的上游资产

本 fork 不是重写，主要继续复用：

- `internal/gateway/*` 的 lifecycle 骨架与依赖注入模式；
- `internal/device/*`；
- `internal/mihomo/*`；
- `internal/config/*`；
- `internal/dhcp/*`；
- `internal/process/*`；
- `internal/runtime/*` 的通用部分；
- `internal/controlapi/*` 中平台无关能力；
- `web/` React 控制面基础。

具体早期分类见 [QNAP_PORTING_AUDIT.md](QNAP_PORTING_AUDIT.md)。该文件是移植前置审计，不应被当作当前实现状态的唯一来源；若与当前代码、CI 或本文件冲突，以当前实现与最新验证结果为准。

## 9. License

本项目沿用 **`GPL-3.0-only`**，没有改为 MIT、Apache-2.0、`GPL-3.0-or-later` 或闭源许可证。
上游 `LICENSE` 与历史归属必须保留。

第三方版本、镜像构建 pin、SBOM 与 provenance 见 [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) 及发布工作流。

来源说明另见 [NOTICE.md](../NOTICE.md)。
