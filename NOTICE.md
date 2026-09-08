# NOTICE

**OpenSurge for QNAP 是 [OpenSurge for Mac](https://github.com/YTwsy/OpenSurge-for-Mac)
的派生项目（derivative work）。**

本文件是自愿提供的来源透明性说明。`GPL-3.0-only` 不像 Apache-2.0 那样要求附带 NOTICE；
本文件不增加或修改许可证条款。

## 项目标识

| 项目 | 值 |
| --- | --- |
| 正式产品显示名称 | OpenSurge for QNAP |
| 仓库 | <https://github.com/zyk1172/OpenSurge-for-QNAP> |
| 性质 | OpenSurge for Mac 的派生项目 |
| 许可证 | `GPL-3.0-only`（继承上游，未修改） |

## 原始项目来源

| 项目 | 值 |
| --- | --- |
| Original project | OpenSurge for Mac |
| Original author / organization | YTwsy |
| Original repository | <https://github.com/YTwsy/OpenSurge-for-Mac> |
| Upstream license | `GPL-3.0-only` |
| Fork baseline commit | `b03bf2f8a2b02a6fffba9c879ce1980ddb831a67` |
| Baseline upstream ref | `master` / v0.2.2 |
| Fork date | 2026-09-09 |

上游 `LICENSE`、Git 历史和作者归属保持保留。本 fork 未获得上游作者背书，除非其另有明确声明。

## 当前 Phase 1 已完成的主要派生工作

- 删除 macOS 菜单栏、PKG、公证、launchd helper、pf 与 macOS BPF IPv6 runtime；
- 建立 `platform.NetworkBackend`；
- 增加 Linux `nftables + iproute2 + TUN` IPv4 backend；
- 增加 nft/routing ownership 冲突检查与精确 cleanup；
- 把 NAT/routing cleanup recipe 持久化，使 Stop/recovery 不依赖进程内对象；
- 增加 write-ahead cleanup journal、network namespace 恢复边界和更耐掉电的 state 写入；
- 明确 QNAP v1 不支持 downstream IPv6 takeover；
- 增加 Linux/QNAP 生命周期回归测试和最小 Go CI。

以下属于后续产品路线，**不表示 Phase 1 已经实现**：正式 Docker/Compose、LAN Web 认证、
Session/CSRF、watchdog、完整 reconciliation、namespace lab、QNAP 真机稳定性/soak、SBOM 与
release-time license verifier、DHCP takeover、IPv6 takeover。

## GPL-3.0-only

- 本项目继续采用 **`GPL-3.0-only`**；
- 未改为 MIT、Apache-2.0、`GPL-3.0-or-later` 或闭源许可证；
- 上游版权/许可证声明不得删除；
- 未来分发 Docker 镜像时，实际随镜像分发的 GPL 组件必须提供符合许可证要求的对应源码获取方式；
- 第三方组件说明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

---

_本 NOTICE 用于来源透明，不构成额外许可证条件。_
