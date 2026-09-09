# Agent 指南 — OpenSurge for QNAP

本仓库的当前产品身份是 **OpenSurge for QNAP**。默认目标不是 macOS App，而是运行在 QNAP Container Station / Docker 中的单容器透明代理网关。

面向用户的主要入口是 QNAP Web UI；`omg` CLI 用于运维、诊断、自动化和恢复。当前代理引擎是 mihomo。

任何 coding agent 在修改网关、网络、部署、Web 文案或验证逻辑前，都应先阅读本文件和根目录 `README.md`。

## 当前默认产品架构

```text
LAN client
 Gateway / DNS = OpenSurge IP
        │
        ▼
QNAP QNET
┌──────────────────────────────┐
│ opensurge                    │
│ Web UI :8080                 │
│ Control API (loopback)       │
│ mihomo                       │
│ dnsmasq                      │
│ TUN                          │
│ Linux policy routing         │
│ /data persistence            │
└──────────────────────────────┘
```

默认部署只有一个 `opensurge` 容器。

不要重新引入以下默认架构：

- `opensurge-manager` + `opensurge-orchestrator` 双容器；
- LAN-facing Web 挂载 Docker Socket；
- host network Gateway；
- `privileged: true`；
- 运行中的 Web 自动修改 QTS Network & Virtual Switch。

## QNAP 网络模型

### 容器创建参数

以下值在 Docker/QNET 创建容器时确定：

- QNAP QNET 父网卡 / Virtual Switch；
- OpenSurge 静态 IPv4；
- LAN CIDR；
- 上游主路由 IPv4；
- `/data` 持久化目录。

容器内部接口通常为 `eth0`。不要因为宿主父接口是 `eth1` / `br0` 就把容器接口改成同名。

### Same-LAN 数据面

当前 QNAP same-LAN manual-gateway 是第一稳定目标。

支持的优先数据面是：

```text
eth0 ingress
→ ip rule iif eth0
→ OpenSurge dedicated routing table
→ tun0
→ mihomo
```

这条路径不要求 `nf_tables`。

部分 QNAP 5.10 内核有 TUN 和 policy routing，但没有可用的 nftables netlink。不要把 `nft -j list ruleset` 失败直接解释为 same-LAN Gateway 必须失败。

需要 NAT 的 isolated-LAN 拓扑仍保留 nftables + fwmark 后端，并继续要求严格能力检查。

## 安全边界

网络改动必须遵守：

1. 禁止 `nft flush ruleset`。
2. 禁止全局 `ip route flush` / `ip rule flush`。
3. OpenSurge 只能删除能够证明 ownership 的规则、路由、table 和进程。
4. runtime snapshot / write-ahead cleanup journal 必须保留跨进程恢复能力。
5. network namespace 是恢复边界；旧容器 namespace 的内核对象不能直接假定仍属于新容器。
6. Web 不得获得修改 QNAP 默认网关、DNS、DHCP 或 Virtual Switch 的任意权限。
7. `/data` 是产品持久化边界，正常升级/重建不得随意清空。
8. 真机测试不得因为方便而停止无关容器、重启 NAS 或修改整网 DHCP。

## QNAP Web 产品规则

QNAP build 应只显示与 NAS 产品有关的功能。

不要在 QNAP 用户表面出现或依赖：

- Finder；
- macOS 菜单栏；
- 合盖保持运行；
- Mac 本机 Wi-Fi DHCP 恢复；
- PF Anchor；
- LaunchAgent / launchd；
- PKG / Gatekeeper；
- “OpenSurge for Mac”产品身份；
- 上游内部 codename 作为 QNAP 版本标识。

如果共享 React 组件同时服务 Mac/QNAP，必须通过 build target 明确隔离，不要因为组件复用就让 Mac 文案出现在 QNAP 页面。

## 配置与订阅持久化

用户可变配置保存在 `/data`。

特别注意：

- 已存在 `/data/config/opensurge.yaml` 时，首次 seed 不得覆盖它；
- 导入订阅只创建草稿；
- `desired` / `applied` 状态必须真实持久化；
- Web 不能仅凭 HTTP 200 显示保存成功；必须重新读取后确认持久状态；
- 运行中保存需要时应执行 stop → persist → verify → start，并明确传播失败。

## 依赖与构建

QNAP NAS 不是构建服务器。

正常测试/发布流程：

```text
GitHub Actions build
→ amd64 / arm64 Docker archive
→ SHA256
→ GitHub prerelease
→ NAS docker load
```

不要把 Go、Node.js、pnpm、gcc 等开发依赖作为 QNAP 正常部署要求。

## 验证门槛

普通代码：

```sh
go test ./...
go vet ./...
```

Web：

```sh
cd web
pnpm install --frozen-lockfile
pnpm test
OPENSURGE_TARGET=qnap pnpm build
```

QNAP/Linux 网络改动必须至少运行相关 namespace/integration test。

涉及 same-LAN TUN ingress-interface routing 时，必须验证：

- nft backend 不可用时 preflight 仍能通过支持范围；
- `ip rule iif` 正确安装；
- OpenSurge 专用 routing table 正确安装；
- TCP forwarding lookup 进入 TUN；
- UDP forwarding lookup 进入 TUN；
- direct fallback 能切回真实 upstream gateway；
- Stop/rollback 精确清理；
- isolated-LAN 在缺少 nftables 时仍 fail closed。

CI 全绿之前不要合并网络路径变更。

## QNAP 真机验证

真机测试必须明确区分：

- CI / namespace 已验证；
- NAS-side 已验证；
- physical client 已验证；
- reboot / soak 尚未验证。

不能用 NAS 内部 `curl` 代替真实客户端 Gateway/DNS 测试。

当前真实 QNAP 测试的目标是先完成一台客户端的 DNS、DIRECT、PROXY、TCP、UDP/QUIC，然后再做 reboot 和 24h/72h soak。

## 文档事实来源

优先级：

1. `README.md` — 当前产品范围；
2. `deploy/qnap/README.zh-CN.md` — QNAP 部署；
3. `docs/app-user-guide.zh-CN.md` — 当前用户流程；
4. `docs/faq.zh-CN.md` — 当前 QNAP FAQ；
5. `internal/gateway/`、`internal/platform/linux/`、`internal/controlapi/` — 实际实现；
6. `.github/workflows/` — 当前 CI 门槛。

`docs/UPSTREAM.md`、`NOTICE.md`、`THIRD_PARTY_NOTICES.md` 用于来源和许可证关系。

仓库内名字明确包含 `macos`、`local-mac`、`lid-closed` 的旧文档/代码属于上游兼容或历史参考，除非当前 QNAP 代码明确引用，否则 **不能作为 QNAP 产品行为的事实来源**。

## 上游关系

本项目基于 OpenSurge for Mac 派生，继续遵守 GPL-3.0-only 并保留上游版权/来源记录。

不要为了“去除原作者残留”而删除 `LICENSE`、NOTICE、第三方许可证或来源说明。需要清理的是 QNAP 产品表面和当前开发文档中的错误产品身份，不是合法 attribution。
