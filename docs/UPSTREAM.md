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
| Porting branch | `port/qnap-linux-docker` |
| Product name | **OpenSurge for QNAP** |
| Repository name | `OpenSurge-for-QNAP` |
| License | `GPL-3.0-only`（继承上游，未修改） |

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
   - 平台无关的诊断、测试和文档。
3. macOS 专属的 `pf`、AppKit/SwiftUI、launchd、BPF IPv6、PKG/notarization 改动不直接 cherry-pick。
4. 每次同步都应记录原始 upstream commit，便于审计和后续追踪。

## 5. Phase 1 已经实现的差异

| 维度 | 上游（macOS） | 当前 Phase 1（QNAP/Linux） |
| --- | --- | --- |
| 产品壳 | macOS 菜单栏 App + Web | 菜单栏与 PKG 运行时已删除；Web 仍在移植中 |
| 防火墙 | `pf` | `nftables` backend |
| 路由 | macOS 路由 / mihomo TUN | `iproute2` policy routing + mihomo TUN，`auto-route: false` |
| forwarding | macOS sysctl | Linux `/proc/sys/net/ipv4/ip_forward` |
| TUN | `utun*` | `/dev/net/tun` / `tun0` 语义 |
| IPv6 takeover | macOS BPF + patched mihomo | v1 明确拒绝/不支持 |
| 本机系统代理 | `networksetup` | QNAP/Linux 路径明确拒绝该 macOS-only 配置 |
| 网络 ownership | pf anchor | nft table + fwmark + route table + rule priority 冲突检查 |
| Stop/rollback | 上游运行时状态 | 持久化 cleanup recipe，可由 fresh backend 恢复 |
| 崩溃窗口 | 上游 boot/PID 机制 | 增加 write-ahead cleanup journal |
| 容器恢复边界 | 不适用 | snapshot 绑定 Linux network namespace |
| 状态持久化 | 原子 state 写入 | temp + file fsync + rename + parent-dir fsync |
| 测试 | macOS lab/真实设备 | 已增加 Linux 单元/生命周期测试；完整 namespace lab 尚未完成 |
| CI | 上游 CI | Phase 1 增加 Go test/vet/build 最小 CI |

## 6. 尚未实现、只属于产品目标的内容

以下内容**不得写成当前已具备能力**：

- 正式 Dockerfile / Compose / QNAP Container Station 发布路径；
- `0.0.0.0` LAN Web 暴露与管理员首次设置；
- Argon2id/bcrypt、Session、CSRF、登录限流；
- watchdog / bounded restart；
- 完整 desired-vs-observed reconciliation 状态机；
- `/data/{config,profiles,providers,runtime,state,logs,backups,licenses}` 的最终发布布局；
- 自动配置备份/最近 10 份回滚；
- secret redaction、日志轮转、Web tail、诊断包；
- Linux 三 network-namespace 实验室与系统性故障注入；
- QNAP 真机、NAS reboot、24h/72h soak；
- Docker SBOM / provenance / release-time third-party license verification；
- DHCP takeover；
- downstream IPv6 takeover。

这些项目属于后续 Phase 2/3，不应作为 Phase 1 合并依据。

## 7. 保留的上游资产

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

具体分类见 [QNAP_PORTING_AUDIT.md](QNAP_PORTING_AUDIT.md)。

## 8. License

本项目沿用 **`GPL-3.0-only`**，没有改为 MIT、Apache-2.0、`GPL-3.0-or-later` 或闭源许可证。
上游 `LICENSE` 与历史归属必须保留。

第三方版本与未来 Docker 发布要求见
[THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md)。Phase 1 尚未发布正式容器镜像，因此不会宣称
尚不存在的 Docker build pin、SBOM 或 release license verifier 已经生效。

来源说明另见 [NOTICE.md](../NOTICE.md)。
