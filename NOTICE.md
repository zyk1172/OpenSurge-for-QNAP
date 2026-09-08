# NOTICE

**本项目 OpenSurge for QNAP 是 [OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac)
的派生项目（derivative work）。**

本文件是**自愿提供**的来源透明性说明。GPL-3.0-only 本身并不像 Apache-2.0 那样要求
分发时附带 NOTICE 文件；此处提供它是为了让使用者清楚知道本项目的来源、修改者与改造方向。

---

## 1. 项目标识

| 项目 | 值 |
| --- | --- |
| 正式产品显示名称 | OpenSurge for QNAP |
| 仓库名 | `OpenSurge-for-QNAP` |
| 仓库地址 | <https://github.com/zyk1172/OpenSurge-for-QNAP> |
| 性质 | OpenSurge for Mac 的派生项目 |
| 许可证 | `GPL-3.0-only`（继承自上游，未修改） |

## 2. 原始项目来源

| 项目 | 值 |
| --- | --- |
| Original project | OpenSurge for Mac |
| Original author / organization | YTwsy |
| Original repository | <https://github.com/YTwsy/OpenSurge-for-Mac> |
| Upstream license | `GPL-3.0-only` |
| **Fork baseline commit** | `b03bf2f8a2b02a6fffba9c879ce1980ddb831a67` |
| Baseline upstream ref | `master`（Merge pull request #33 from YTwsy/codex/release-v0.2.2） |
| Fork date | 2026-09-09 |

上游版权声明与 `LICENSE` 文件在本 fork 中**完整保留，未删除、未弱化**。

## 3. 主要修改者

- Fork 与 Linux/QNAP 移植维护者：zyk1172（<https://github.com/zyk1172>）
- 上游原作者：YTwsy（<https://github.com/YTwsy>）—— 上游代码的著作权人

本 fork **未获得上游作者的背书（endorsement）**，除非其明确声明。

## 4. 修改方向

本 fork 的目标**不是**简单让上游项目在 QNAP 上运行，而是系统性改造为：

> 一个专门面向 QNAP NAS，同时尽量保持通用 Linux Docker 兼容性的、Web 管理的、
> 产品级稳定的透明代理网关。

主要改造方向：

1. **运行环境**：macOS 原生 App + 特权助手 → Docker / OCI 容器（linux/amd64、linux/arm64）。
2. **管理界面**：本机 loopback Web GUI + 菜单栏 App → 监听 `0.0.0.0` 的 Web UI 作为
   唯一主要管理界面，并实现正式认证（首次启动 setup wizard、Argon2id 密码哈希、
   HttpOnly/SameSite session、CSRF、登录限流、审计日志）。
3. **网络数据面**：`pfctl` + macOS 路由 → `nftables` + Linux 策略路由 + fwmark +
   mihomo TUN。OpenSurge 只维护自己的 `table inet opensurge`，**绝不执行
   `nft flush ruleset`**，绝不污染 QNAP 防火墙、Container Station 或用户已有规则。
4. **架构边界**：新增 `internal/platform` 平台抽象层，禁止把 Linux 命令散落到业务代码；
   即便本 fork 不再构建 Darwin 后端，也保持「核心业务逻辑 ≠ Linux shell 命令」的边界。
5. **可靠性**：预检（preflight）→ 快照（snapshot）→ 事务化应用 → 连通性验证 → 提交，
   失败逆序回滚；引入显式状态机、崩溃恢复 reconciliation 与有界重启的 watchdog。
6. **数据持久化**：macOS `~/Library/Application Support` → Docker 卷 `/data/`，
   配置原子写入并保留历史版本。
7. **可诊断性**：结构化日志 + 脱敏 + `opensurge doctor` + 一键诊断包。
8. **测试**：macOS Lima 实验室 → Linux network namespace 实验室与故障注入。

第 1 版范围明确**不包含**：QPKG、macOS App、Windows、iOS、Android、云管理、
多用户 RBAC、自动修改 QNAP Virtual Switch、实验性 IPv6 接管、eBPF 重写、VLAN 管理。

## 5. GPL-3.0-only 说明

- 本项目与上游一致，采用 **`GPL-3.0-only`**。
- 未擅自改为 MIT、Apache-2.0、`GPL-3.0-or-later` 或闭源许可证。
- 上游 `LICENSE` 文件原样保留。上游版权声明未删除。
- 随容器镜像分发的 GPL 程序（mihomo、dnsmasq 等）的实际版本、许可证与对应源码获取方式，
  记录在 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)，并由 CI 在构建时根据实际
  构建产物生成与校验。
- 依据 GPL-3.0 第 6 条，分发本项目的二进制/镜像时，需同时提供对应源码或书面的源码获取
  要约。本项目的源码始终在仓库地址公开可获取。

## 6. 第三方组件

第三方组件的完整清单、版本、许可证与源码来源见
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
本项目不声称对 mihomo、dnsmasq、React 等第三方项目拥有任何权利。

---

_本 NOTICE 文件用于来源透明，不构成 GPL-3.0 的强制要求，也不修改或附加任何许可证条款。_
