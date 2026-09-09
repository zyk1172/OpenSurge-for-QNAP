# QNAP 网关生命周期

当任务涉及 gateway start、stop、reload、`restart-mihomo`、runtime state、TUN、策略路由或故障恢复时，先读这个页面。

当前 QNAP 产品是单容器架构。QNAP/QTS 宿主负责提供 QNET、静态容器地址和 `/dev/net/tun`；OpenSurge 只在自己的容器网络命名空间内管理数据面，不应修改 QTS 默认网关、宿主 DNS、Virtual Switch 或其他容器网络。

## 运行时职责

- `mihomo`：加载已应用配置，提供 mixed-port、REST API 和透明 TUN。
- `dnsmasq`：提供受管 DNS；仅在启用 DHCP 的拓扑中承担 DHCP。
- IPv4 forwarding：必须在容器网络命名空间内可用。
- same-LAN/QNET：使用 `ip rule iif <lan>` + OpenSurge 专用路由表，不依赖 nftables。
- isolated-LAN：保留 nftables/fwmark 后端；该模式必须实际具备 nftables 内核能力。
- runtime state：记录 boot session、子进程指纹、TUN、策略路由配方及 cleanup intent，是异常恢复的依据。

## same-LAN 数据面

QNAP 单容器的首选路径是：

```text
LAN client
  -> QNET eth0
  -> ip rule iif eth0 lookup <OpenSurge table>
  -> LAN CIDR dev eth0
  -> default dev tun0
  -> mihomo
```

这一模式不要求 `nf_tables`、TPROXY 或 REDIRECT。策略路由健康不能只看 Mihomo 或 `tun0` 是否存在；必须同时确认：

1. 精确的 `ip rule` selector、priority 和 table；
2. 专用表中的 LAN route；
3. 专用表中的 default TUN route，或 direct fallback 时的真实 upstream route；
4. 专用表不存在额外、可能绕过 TUN 的路由。

只要以上任一条件无法确认，status 必须是 degraded，而不能报告 healthy。

## Start 顺序

`internal/gateway/manager.go` 是生命周期权威实现。正常启动顺序：

1. 要求容器内 root；
2. 拒绝已有未清理 runtime state 的重复启动；
3. normalize/validate desired config；
4. preflight dnsmasq、mihomo、TUN、capabilities、interface、forwarding 和拓扑；
5. 写并验证 Mihomo/dnsmasq 配置；
6. 在网络变更前保存 runtime snapshot；
7. 启用或确认 IPv4 forwarding；
8. 启动 Mihomo，并记录 PID + process fingerprint；
9. 启动 dnsmasq，并记录 PID + process fingerprint；
10. 等待实际 TUN 设备 ready；
11. isolated-LAN 才应用 nftables；same-LAN 跳过 nftables；
12. 应用策略路由；
13. 立即从内核回读并严格核验策略规则和专用路由表；
14. 核验成功后才把 `RoutingApplied=true` 持久化并宣布 Gateway running。

Start 中任何网络或进程步骤失败都必须 rollback。不能因为 Mihomo API 可访问或 TUN 已出现就跳过策略路由验证。

## Runtime state 与进程身份

Linux/QNAP 进程身份通过 `/proc/<pid>` 生成指纹，不依赖 `ps/procps`。status、stop 和 restart 在信号子进程前必须验证 PID 与指纹匹配，避免 PID reuse 时误杀其他进程。

正常 restart 期间，runtime state 必须先进入保守状态：

```text
PIDMihomo = 0
RoutingApplied = false
```

随后才停止旧 Mihomo。这样即使容器或控制流程中途异常，也不会留下“路由仍健康”的错误持久化声明。

## restart-mihomo

`restart-mihomo` 是运行时修复，不是配置 reload。它只使用已经应用并记录在 runtime snapshot 中的配置和路由配方。

顺序：

1. 校验当前 boot/runtime ownership；
2. 确认 desired profile digest 与 applied runtime 一致；
3. 对已生成的 Mihomo config 执行真实校验；
4. 把 runtime 中的 Mihomo PID 清零，并把 `RoutingApplied` 标为 false 后持久化；
5. 停止旧 Mihomo；
6. 归档旧 Mihomo 日志；
7. 启动 replacement Mihomo，记录新 PID/fingerprint；
8. 等待 replacement TUN ready；
9. 从 runtime snapshot 重新应用策略路由；
10. 严格核验 rule + 完整专用路由表；
11. 核验成功后将 routing 标为 applied 并持久化。

这个动作不停止 dnsmasq，不恢复 IPv4 forwarding，也不修改 QNAP/QTS 宿主网络、QNET、Virtual Switch、容器创建参数或其他容器。

如果路由修复失败，replacement Mihomo 必须停止，并把 runtime 持久化为 degraded。如果最后一次“路由已恢复”状态写盘失败，则不能为了表面整洁再杀掉已经被前一份 durable state 追踪的 replacement PID；应保持它可被后续 recovery/stop 精确识别，同时 durable state 继续维持 `RoutingApplied=false`，让 status fail closed。

## Status

status 不可信任 runtime 布尔值本身。对于 active runtime，应同时检查：

- PID/fingerprint 与实际进程；
- Mihomo runtime/TUN；
- dnsmasq；
- IPv4 forwarding；
- `PolicyRoutingPresent()` 的真实内核结果；
- isolated-LAN 时的 nftables ownership/存在性。

QNAP Web 至少应展示：

```text
data_plane
routing
routing_error
mihomo
TUN
DNS/DHCP
IPv4 forwarding
```

same-LAN 上 `nftables=not_required` 是正常状态，不是故障。

## Stop 与 interrupted recovery

正常 stop：

1. 读取并验证当前 runtime；
2. 按 snapshot 精确恢复/删除 OpenSurge 自己拥有的网络对象；
3. 停止 dnsmasq；
4. 停止 Mihomo；
5. 只有 cleanup 全部成功后才移除 runtime state 和 applied device-policy snapshot。

不允许执行全局 `ip route flush`、`ip rule flush` 或 `nft flush ruleset`。所有删除都必须限定在 OpenSurge 自己的 selector、table 和对象。

如果 runtime 属于旧 boot/container network namespace，进入 interrupted reconciliation：不根据旧 PID 发信号，只按持久化 snapshot 做内容限定的网络恢复，然后清理旧 runtime。

## Reload

Reload 是完整配置切换：先在不改网络的情况下生成并验证 candidate；通过后执行完整 stop，再按 candidate 完整 start。它不承诺零中断，也不能用 `restart-mihomo` 代替。

运行中应用订阅/profile 时，只有 reload/start 成功并且新的 profile digest 被 runtime 持久化后，来源才可以标记为 applied。

## 不变量

- QNAP normal mode 是单个 `opensurge` 容器。
- QNET parent/static IP/subnet/upstream router 是容器创建时参数，不由 Web 运行时修改。
- same-LAN 不依赖 nftables。
- 生命周期不能修改 QTS 宿主默认网络。
- 进程、TUN、策略路由三者必须作为一个数据面整体判断。
- runtime snapshot 是 recovery 的权威配方，不能在恢复时偷偷改用新的 desired topology。
- 所有成功声明都必须有真实运行时证据。

## 验证

普通代码验证运行 `go test ./...` 和 `go vet ./...`。Linux/QNAP 策略路由变更必须通过 Phase 11 namespace gate，真实执行 TCP/UDP `iif -> dedicated table -> TUN` 验证。修改容器、Web 或持久化边界时还必须通过 Phase 2 Docker CI 与 Phase 10 QNAP Single-Container CI。

自动化测试通过并不等于真实 TS-264C 已验收。发布测试镜像后仍要在真实 QNAP 上确认 TUN、`ip rule`、专用路由表、DNS/Mihomo、stop/start cleanup 和一台实际 LAN 客户端。
