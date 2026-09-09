# 为 OpenSurge for QNAP 做贡献

感谢你关注 OpenSurge for QNAP。

## 开始之前

- 先阅读 `README.md`，了解当前 QNAP 产品范围。
- 涉及部署时阅读 `deploy/qnap/README.zh-CN.md`。
- 涉及网关、透明代理、恢复或验证时，阅读 `AGENTS.md`。
- 上游来源与许可证关系见 `docs/UPSTREAM.md`、`NOTICE.md` 和 `THIRD_PARTY_NOTICES.md`。

## 当前工程方向

默认产品是 QNAP/Linux 单容器 Gateway：

- QNET 独立 LAN IP；
- `/dev/net/tun`；
- `NET_ADMIN` / `NET_RAW`；
- Web + Control API + mihomo + dnsmasq + policy routing；
- `/data` 持久化；
- same-LAN manual gateway 作为第一稳定目标。

不要在没有明确产品决策的情况下重新引入 Mac-only UI、双容器 Manager/Orchestrator、Docker Socket Web 控制或 `privileged: true`。

## 提交改动

- 尽量让每次改动保持小而清楚。
- 行为、约束或验证方式发生变化时，同步更新 QNAP 文档。
- 不要提交订阅、节点、密钥、个人网络信息或未经脱敏的日志。
- 网络改动必须说明验证范围，不能只凭单元测试宣称真机通过。

基础检查：

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

涉及 TUN、policy routing、DNS、Gateway 生命周期或 rollback 时，还需要运行对应的 Linux namespace / integration gate。

PR 必须在当前 head 的 required CI 全绿后才合并。

## 上游代码

欢迎人工挑选上游平台无关修复，例如 mihomo profile/provider/policy、配置解析、测试和通用 Web 逻辑。

不要自动合并上游 macOS runtime，并注意不要让 Finder、菜单栏、PF、合盖、LaunchAgent 等桌面端概念重新进入 QNAP 产品表面。
