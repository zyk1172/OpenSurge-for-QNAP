# 流量与规则分析

OpenSurge for QNAP 的 **流量分析** 页面位于 Web UI 的“诊断”下方，用来把真实网络活动、Mihomo 规则命中和近期诊断日志放到同一个工作流中。

## 目标

这个功能不是自动判断订阅规则“对/错”，而是提供可核对的证据：

- 当前活动连接持续刷新；
- 显示域名/目标 IP、来源 IPv4、端口、协议、Mihomo 命中规则、rule payload、出口 chain 和上下行流量；
- 点击任意连接可立即创建一条高优先级规则；
- 最近观察到的连接按域名聚合；同一目标出现多个规则或多个出口时标记为“建议复核”；
- 从诊断日志中筛选 `error`、`warn`、`timeout`、`reset`、`refused` 等异常信号，作为人工或 AI 分析的辅助证据。

页面当前把最多 3000 条连接元数据保存在浏览器 `localStorage` 中，保留时间最多 7 天。该历史用于 Web 端聚合，不包含数据包正文、HTTP Body、Cookie、密码或完整请求内容。OpenSurge Control API 原有 `/diagnostics` 仍是实时连接和脱敏日志的权威来源。

## 从连接创建修正规则

点击实时连接或域名记录后，右侧规则面板会显示：

- 当前命中的规则和 rule payload；
- 当前出口 chain；
- 来源 IPv4；
- 目标 IPv4/端口；
- 可选的 `DOMAIN`、`DOMAIN-SUFFIX` 或 `IP-CIDR` 匹配方式；
- `DIRECT`、`REJECT` 和当前可用策略组。

确认后，规则不会修改订阅源，而是写入现有 **高级：全局附加配置**：

```yaml
rules:
  prepend:
    - DOMAIN,api.example.com,Proxy
```

`rules.prepend` 固定插入订阅规则之前，所以可以覆盖订阅中的错误匹配；删除这条覆盖规则即可恢复订阅原始行为。

如果当前存在 `desired` 或 `applied` 的 mihomo profile 来源，Web UI 会在保存覆盖规则后重新应用该来源，使规则进入当前运行配置。如果没有已选择来源，则规则保存为高级附加配置草稿，在下一次应用来源或启动时生效。

## AI 管理 Token

无需新增第二套 AI Token。现有 QNAP 局域网管理 Token 已经能够访问此工作流需要的 API：

```text
GET  /api/remote/v1/diagnostics
GET  /api/remote/v1/policies
GET  /api/remote/v1/profile-overlay
PUT  /api/remote/v1/profile-overlay
GET  /api/remote/v1/sources
POST /api/remote/v1/sources/{id}/apply
```

推荐 AI 工作流：

1. 读取 `/diagnostics`，分析当前连接、规则命中、出口 chain 和近期脱敏日志；
2. 读取 `/profile-overlay`，确认已有高优先级规则；
3. 给出“当前命中 → 证据 → 建议规则 → 预期变化”，不要修改订阅源；
4. 用户确认后，将规则插入 `profile-overlay.document.rules.prepend`，并使用当前 revision/ETag 保存；
5. 如有当前来源，重新应用来源；
6. 再次观察实时连接，确认新连接确实命中新的覆盖规则。

AI 不应仅因为一次连接经过 `DIRECT` 或 `PROXY` 就断言规则错误。优先使用多次观察、失败日志、同域名不同出口等证据，并把不确定项标记为“建议复核”。

## 隐私边界

流量分析只处理路由所需的连接元数据和已经脱敏的进程日志。该功能不进行 TLS 解密，也不保存数据包 payload、HTTP Body、Cookie、认证头或密码。
