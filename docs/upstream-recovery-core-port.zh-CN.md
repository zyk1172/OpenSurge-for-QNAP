# 恢复流程与 Mihomo 1.19.33 移植

本次吸收 OpenSurge for Mac 的平台无关恢复设计，不整体合并上游。

## 原始来源

- [PR #100](https://github.com/YTwsy/OpenSurge-for-Mac/pull/100)，
  `617b306197`：重载阶段、独立恢复时限、最终健康核验和 Helper 失败元数据。
- [PR #109](https://github.com/YTwsy/OpenSurge-for-Mac/pull/109)，
  `7338dc53fc`、`0d9a148e43`：恢复专用配置读取、独立资源继续清理。
- [Mihomo v1.19.33](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.33)，
  固定源码 `3f27ef39f8c96bbfbc8b0b9bb10f5c9ef60149d8`。
  包含已放弃 DoH 查询的拨号堆积修复，以及热重载保留 Fake-IP 分配状态的修复。

## NAS 行为

1. 重载错误携带 validation、stop、start 阶段。配置事务恢复原文件后，
   只有确认没有残留 runtime 且不是 stop 失败才尝试启动旧网关。
   恢复使用独立 90 秒 context，最终状态探测最多 5 秒。
2. 状态文件存在不等于恢复成功。最终核验要求运行中的核心、DNS 与本地 DNS、
   启用的 DHCP、TUN、转发、策略路由及适用的 nft 状态；无法确认时保留未知/降级状态。
   start 失败后的健康候选不能冒充旧 applied 配置。恢复未确认时保留候选文件。
3. Profile 和 Tailscale 共用恢复判断，错误响应保留结构化 rollback 信息。
   DHCP 接管恢复记录根据实际状态更新，不再解析错误文字来判定阶段。
4. `LoadRecovery` 保留语法、未知字段、类型和必要 runtime 路径检查，
   不校验下一次部署的 LAN、DNS 或策略草稿。停止、状态和诊断可在部署参数无效时使用；
   启动、重载和新配置应用仍完整校验。停止后允许用有效候选修正旧部署参数，
   runtime 路径、二进制和密钥等受保护字段继续禁止通过编辑器更改。
   Helper 的可信路径和目标检查继续执行。
5. 预备引擎清理失败会保留错误，并继续清理其他独立确认归属的服务和网络资源。
   未完成的清理记录保留以供重试；身份不匹配的进程仍不会被终止。
6. DNS 租约读取错误单独作为 `lease_error` 报告，不遮蔽生命周期状态。
7. Mihomo 保留 NAS 的 30 秒 API/TUN 等待窗口，等待遵循生命周期 context。
   Docker 使用 Go 1.26.11 及现有的 crypto/net/compress 安全修补重建 1.19.33。
   Mac 的 packet-listener 补丁与打包脚本不属于本次 NAS 更新。

## 验证方法与边界

- Go 单元与竞态回归覆盖请求取消、恢复失败、部分 stop、健康候选残留、
  不完整数据面、Helper 元数据、无效部署配置下停止与诊断、进程身份保护。
- `TestPinnedCorePreservesFakeIPAcrossReload` 启动真实 1.19.33 核心，使用随机
  loopback controller/DNS 端口；关闭 TUN 和 Fake-IP 磁盘持久化，连续热重载，
  检查旧域名映射稳定且新域名不重用已分配地址。该门槛纳入 Linux CI。
- Linux namespace lab 继续验证 same-LAN 路由、精确清理、完整 DNS 恢复，以及
  真实 SmartDNS/Mihomo 的 DoH、DoT、代理节点独立解析和代理故障行为。
- 本机原生核心测试不等同于 Linux 镜像或 NAS 验证。Docker 镜像构建与
  namespace lab 以本 PR 对应的 CI 结果为准；NAS、物理客户端、重启和长期运行
  需单独验收。这次变更不能用来认定历史断网的具体根因。
