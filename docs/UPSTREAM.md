# Upstream Relationship

本文件记录 OpenSurge for QNAP 与上游 OpenSurge for Mac 的派生关系、同步策略与平台级差异。
**本项目不隐藏、不弱化上游来源。**

## 1. 上游信息

| 项目 | 值 |
| --- | --- |
| Original project | OpenSurge for Mac |
| Original author / organization | YTwsy |
| Original repository | <https://github.com/YTwsy/OpenSurge-for-Mac> |
| Upstream license | `GPL-3.0-only` |
| Upstream default branch | `master` |
| Upstream description | Surge-style whole-home gateway and control plane for macOS with IPv4/IPv6 support — mihomo TUN, dnsmasq-powered DHCP/DNS, per-device routing, and an agent-friendly validation workspace. |

## 2. Fork 信息

| 项目 | 值 |
| --- | --- |
| Fork repository | <https://github.com/zyk1172/OpenSurge-for-QNAP> |
| **Fork baseline commit** | `b03bf2f8a2b02a6fffba9c879ce1980ddb831a67` |
| Baseline upstream ref | `master`（Merge pull request #33 from YTwsy/codex/release-v0.2.2） |
| Baseline commit date | 2026-09-07 |
| Fork date | 2026-09-09 |
| Porting branch | `port/qnap-linux-docker` |
| 正式产品显示名称 | **OpenSurge for QNAP** |
| 仓库名 | `OpenSurge-for-QNAP` |
| 本项目许可证 | `GPL-3.0-only`（继承自上游，未修改） |

## 3. 命名与归属边界

已改名的**产品名称**相关字符串：

- `OpenSurge for Mac` → `OpenSurge for QNAP`
- `opensurge-for-mac` → `opensurge-for-qnap`

**刻意未修改**的内容：

- upstream Git 历史（保留完整 commit 链与作者信息）
- 第三方项目名称（mihomo、dnsmasq、MetaCubeX、React 等）
- 引用上游项目的来源说明（本文件、README Fork Attribution、NOTICE.md）
- 第三方许可证文字（`LICENSE`、`third_party/licenses/*`）
- 必须保留的 copyright 声明
- Go module path 中的历史命名在必要时才迁移（避免一次提交引入大量无关 diff）

## 4. 同步 upstream 的策略

1. **不自动 merge**。上游仍在活跃开发，自动同步会把未审计的 macOS 改动直接带入网关数据面。
2. **定期人工挑选（cherry-pick）**。优先挑选：
   - 与平台无关的 bug 修复（配置校验、profile 解析、规则编译、设备策略）
   - Web UI 改进与前端测试
   - 测试与文档改进
3. **不挑选**的类别：
   - 触碰 `internal/pf`、`internal/macosnetwork`、`internal/macosipv6`、
     `internal/ipv6packet` 的改动（这些模块在本 fork 已删除或替换）
   - macOS 打包、公证、菜单栏 App 相关改动
   - launchd / 特权助手相关改动
4. **每次同步前**先确认 `docs/QNAP_PORTING_AUDIT.md` 的分类仍然成立；若新增了平台耦合，
   必须先补抽象再合并。
5. 合并时保留 upstream 的 `Original-Commit:` 引用，便于追溯。

## 5. 平台级改造（本项目相对上游做了什么）

| 维度 | 上游（macOS） | 本项目（Linux / QNAP Docker） |
| --- | --- | --- |
| 运行环境 | macOS 13+ 原生 App + 特权助手 | Docker / OCI 容器（linux/amd64、linux/arm64） |
| 管理界面 | macOS 菜单栏 App + 本机 Web GUI | **Web UI 是唯一主要管理界面**，监听 `0.0.0.0` |
| 认证 | 本机 loopback + 随机 token，无账号密码 | 首次启动 setup wizard + Argon2id + session + CSRF + 登录限流 |
| 防火墙 / NAT | `pfctl` + anchor（`nat on` + `pass all`） | `nftables`，仅维护 `table inet opensurge`，**禁止 flush ruleset** |
| 转发开关 | `sysctl net.inet.ip.forwarding` | `/proc/sys/net/ipv4/ip_forward` |
| 引流机制 | mihomo TUN + macOS 路由 | mihomo TUN + **fwmark 策略路由**（`auto-route: false`，OpenSurge 全权掌管） |
| 接口发现 | `networksetup` / `scutil` | netlink / `/sys/class/net` |
| 邻居发现 | `/usr/sbin/arp` | `ip neigh` / netlink |
| 隧道设备 | `utun`（默认 `utun123`） | Linux TUN（`/dev/net/tun`，默认设备名改 Linux 语义） |
| 本地系统代理 | `networksetup -setwebproxy` | 不存在该语义，移除 |
| 休眠 / 合盖 | `pmset -a disablesleep` 防休眠 | 无对应语义，移除 |
| IPv6 下游接管 | dnsmasq RA + BPF 包代理（`bpf_darwin.go`） | **v1 不支持**，代码留边界，UI 明确提示 |
| 数据目录 | `~/Library/Application Support/OpenSurge` | `/data/{config,profiles,providers,runtime,state,logs,backups,licenses}` |
| 配置写入 | `policy_workspace` 内原子写 | **全局统一原子写入**（temp → fsync → validate → rename）+ 保留最近 10 份备份 |
| 生命周期 | start/stop/reload + 回滚 | **事务化**：预检 → 快照 → 应用 → 连通性验证 → 提交，失败逆序回滚 |
| 状态机 | state 文件 + bool | `STOPPED/STARTING/RUNNING/RELOADING/STOPPING/FAILED/RECOVERING/INTERRUPTED` |
| 崩溃恢复 | boot session + PID 指纹 | boot session + PID 指纹 + **reconciliation**（观测状态 vs 期望状态）+ bounded watchdog |
| 日志 | stdout + 少量文件 | 结构化日志（component/event）+ rotation + Web tail + **secret redaction** |
| 诊断 | `omg doctor` | `opensurge doctor` + Web Diagnostics + **一键脱敏诊断包** |
| 测试 | macOS Lima + socket_vmnet 实验室 | **Linux network namespace 实验室**（client → gateway → upstream）+ 故障注入 |

## 6. 保留的上游资产

本项目**不是重写**。以下上游模块被直接保留或仅作适配（详见
[QNAP_PORTING_AUDIT.md](QNAP_PORTING_AUDIT.md)）：

- `internal/gateway/manager.go` 的事务骨架与 `gatewayDeps` 依赖注入
- `internal/device/*`：设备模型、策略编译、bundle（含 `schema_version`）
- `internal/mihomo/*`：mihomo 配置渲染、profile 解析、overlay、API 客户端
- `internal/config/*`：配置结构、校验、digest、渲染
- `internal/dhcp/*`：dnsmasq 配置模板与管理
- `internal/process/*`：进程启动/存活/指纹（无 shell 拼接）
- `internal/runtime/*`：状态原子写、boot session、生命周期锁
- `internal/controlapi/*`：`/api/v1` 路由、统一错误信封、session 与 CSRF 骨架、
  proxy health、connectivity、policy workspace、doctor controller
- `web/`：整套 React UI 的设计、组件与测试（仅替换 macOS 文案与新增 QNAP 面板）

## 7. 许可证

本项目沿用上游的 **`GPL-3.0-only`**，未改为 MIT / Apache-2.0 / GPL-3.0-or-later，也未闭源。
上游版权声明与 `LICENSE` 文件完整保留。

第三方组件的实际分发版本以 [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) 为准，
由 CI 在构建时根据实际版本生成/校验，不复制上游旧版本号。

来源说明另见 [NOTICE.md](../NOTICE.md)。
