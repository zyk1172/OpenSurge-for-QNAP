# Agent Wiki 索引 — OpenSurge for QNAP

这个索引面向在本仓库工作的 coding agent。当前产品事实以 **QNAP/Linux 单容器 Gateway** 为主，不再以 macOS App 作为默认实现模型。

开始任何网关、网络、部署或产品文案任务前，先读：

1. 根目录 `AGENTS.md`；
2. 根目录 `README.md`；
3. `deploy/qnap/README.zh-CN.md`；
4. 当前相关实现和 CI workflow。

## 当前核心模型

```text
LAN client
 Gateway / DNS = OpenSurge IP
        │
        ▼
QNAP QNET
┌───────────────────────────┐
│ opensurge                 │
│ Web + Control API         │
│ mihomo + dnsmasq          │
│ TUN + Linux policy route  │
│ /data persistence         │
└───────────────────────────┘
```

默认部署只有一个 `opensurge` 容器。

### Same-LAN transparent proxy

第一稳定目标是 IPv4 same-LAN manual gateway。

支持 QNAP 的优先路径：

```text
client traffic enters eth0
→ ip rule iif eth0
→ OpenSurge dedicated table
→ tun0
→ mihomo
```

该路径不要求 `nf_tables`。部分 QNAP 5.10 内核缺少 nftables netlink，但仍有 TUN 与 Linux policy routing，不能因此直接判定透明代理不可用。

需要 NAT 的 isolated-LAN 仍保留 nftables + fwmark 后端。

## 关键事实来源

### 产品与部署

- 当前产品范围：`README.md`
- QNAP 部署：`deploy/qnap/README.zh-CN.md`
- 用户流程：`docs/app-user-guide.zh-CN.md`
- FAQ：`docs/faq.zh-CN.md`
- 持久化：`deploy/qnap/PERSISTENCE.md`

### 代码

- Gateway 生命周期：`internal/gateway/`
- Linux/QNAP 网络后端：`internal/platform/linux/`
- 配置：`internal/config/`
- Control API：`internal/controlapi/`
- Web：`web/`
- 容器入口：`cmd/opensurge-container/`、`docker/`
- QNAP Compose：`deploy/qnap/docker-compose.yml`

### 验证

- 通用 CI：`.github/workflows/ci.yml`
- Linux/QNAP Docker gate：`.github/workflows/phase2-docker.yml`
- QNAP single-container gate：当前 QNAP single-container workflow
- nft-free TUN iif gate：`.github/workflows/phase11-qnap-iif.yml`
- 测试镜像发布：`.github/workflows/publish-qnap-test-image.yml`

网络结论必须区分：

- 单元测试通过；
- namespace/integration test 通过；
- NAS-side 真机通过；
- physical client 通过；
- reboot / soak 通过。

不能把其中一层替代另一层。

## 当前重要决策

- QNAP 默认单容器，不回到 Manager/Orchestrator 双容器。
- QNET 父接口和静态 IP 是容器创建参数，不由运行中的 Web 修改。
- `/data` 是完整持久化边界。
- Mihomo `auto-route` 在受管 Linux/QNAP 路径保持关闭，OpenSurge 自己管理 policy routing。
- same-LAN QNAP 可以使用 nft-free `ip rule iif` 数据面。
- isolated-LAN 的 nftables backend 继续 fail closed。
- Stop/rollback/recovery 必须按 ownership 精确清理。
- LAN-facing Web 不获得 Docker Socket。
- 正常 QNAP 部署不要求 NAS 安装 Go/Node/gcc，也不在 NAS 本地构建镜像。

## Web 产品边界

QNAP build 不应向用户显示：

- Finder；
- macOS 菜单栏；
- 合盖保持运行；
- PF Anchor；
- LaunchAgent / launchd；
- PKG / Gatekeeper；
- Mac 本机 Wi-Fi/DHCP 恢复流程；
- `OpenSurge for Mac` 产品身份；
- 上游 codename 作为 QNAP 版本名。

共享代码可以继续保留 Mac target，但必须通过 build target 隔离。

## 上游 / 历史资料

`docs/UPSTREAM.md`、`NOTICE.md`、`THIRD_PARTY_NOTICES.md` 是来源与许可证事实。

仓库中明确带有 `macos`、`local-mac`、`lid-closed` 等名称的历史决策或兼容代码，可以作为上游背景资料，但 **不能优先于当前 QNAP README、AGENTS 和实际代码作为产品事实来源**。

如果未来需要重新整理这些历史资料，优先移动到清晰的 upstream/archive 区域，不要删除必须保留的许可证和 attribution。
