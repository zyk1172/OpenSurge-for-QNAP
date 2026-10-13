# 2026-10-13：SmartDNS 代理端口启动失败与恢复中断

## 真机证据

故障容器 revision 为 `cd425ccc6b68bdeaa3c087972c15b9b8d6f9492a`。
Docker 状态为 running/unhealthy，restart count 为 0，OOMKilled 为 false。
Web 与 Control 仍在运行；停止的是网关数据面。不能将这个现象直接称为
Docker 容器自行退出，也不能据此断定 NAS SSH 服务退出。

本次安装同时启用了 `resolver_proxy_enabled` 与 `mihomo_use_smartdns`，
代理目标为用户配置的代理组。报错中的端口 `127.0.0.1:7854` 由 **Mihomo**
提供，是 SmartDNS 的 SOCKS5 代理入口，区别于 SmartDNS 自身的 DNS 监听。

以下时间均为 UTC：

- 2026-10-11 20:39:12，容器启动后的自动恢复成功，Mihomo、DNS 与 TUN
  均正常启动。本次故障没有证据表明是开机没有恢复网关造成的。
- 2026-10-12 21:03:04，`auto-host-hosts-154e71b2a731f5a4` 开始更新
  QNAP Hosts 有效配置。21:03:08，原 SmartDNS 收到 SIGTERM。
- 该操作的新配置启动与旧配置回退启动，均失败于
  `SmartDNS proxy listener not ready after 30s`。
- 随后的完整自动恢复也出现同一错误；恢复状态成为 failed，没有
  `next_retry_at`。2026-10-13 01:01:43 的手动启动再次失败。
- 失败启动日志完成 GeoSite 初始化，并显示 Control API 已监听，随后
  30 秒内没有后续 SOCKS/DNS/TUN 监听日志。实际 log-level 为 info。
- NAS 同期 load average 达 75–81，HDD RAID 的 inflight I/O 达 782–1080，
  多个宿主进程处于 D 状态；SSD RAID inflight 为 0。Docker root 在 HDD，
  `/data` 与已缓存 `/usr` 在 SSD。这证明启动处于严重负载环境，但尚未
  定位到 Mihomo 初始化的某一个阻塞调用，不能把负载直接当成已证实的唯一根因。

诊断时一次启动在约 6 秒内成功，其后用于提取 Go 堆栈的受控 SIGQUIT
发生在启动已经完成后。堆栈显示正常接受连接和等待信号，**没有捕获到
失败启动的阻塞点**。该引擎退出后暴露了另一处恢复状态停滞。
随后一次完整恢复在约 7 秒内成功，并通过 UDP/TCP DNS 查询。

## 已证实的代码缺陷与修复

1. **启动超时丢失错误类型。** SmartDNS 代理监听等待返回普通字符串
   `not ready after 30s`，恢复控制器不识别为超时，因而耗尽一次恢复后永久
   停止。API/TUN 等启动阶段也有类似丢失类型的返回分支。统一保留
   `context.DeadlineExceeded` 与最后一个错误原因，接入已有 NAS 退避策略。
2. **就绪依赖 INFO 日志。** 旧检查仅搜索已监听文字，会受日志级别影响，
   也不能证明监听还存在。本次 NAS 配置为 info，因此这不是已证实的本次
   首次失败原因。修复改为当前 PID 的确切监听 socket ownership 与 SOCKS5
   无认证握手；日志仅用于提早报告明确的 bind 失败。
3. **成功操作后再次失败会永久停下。** 完整恢复命令返回成功之后，两次
   健康确认之前的新失败会被一次尝试保护挡住。NAS 现在只在新的、明确的
   故障观测后安排退避重试。连续未知状态仍不能触发破坏性恢复；如果随后
   明确发现故障，则可重新进入退避恢复。
4. **NAS 部分引擎恢复不能覆盖依赖。** 已确认的引擎退出或连续两次
   Controller connection refused 改走完整网关恢复，重建 DNS/TUN/路由。
   Mac 的部分恢复策略不变。
5. **开机锁竞争可能消耗恢复机会。** 开机 worker/CLI 在预检查后取得同一
   生命周期锁时，恢复动作尚未执行。该竞争改为继续观察，而不是永久失败。
   包含 ownership/权限/配置拒绝的复合错误仍保持失败边界。

重试间隔从 30 秒指数增加至最多 15 分钟；手动停止会清除重试，明确配置
错误、权限拒绝、进程身份变化不自动反复尝试。启动等待预算仍为 30 秒，
没有通过无限延长等待掩盖失败。只清理能够证明 ownership 的当前 namespace
对象，保留跨进程 cleanup journal。

## 验证边界

回归测试覆盖真实父子进程的监听 ownership、错误端口/通配地址/其他 socket、
关闭 INFO 日志、SOCKS5 握手拒绝、类型化超时、退避、未知状态、手动停止、
生命周期锁竞争及永久拒绝边界。

Linux namespace 集成使用固定版本的真实 Mihomo 与 SmartDNS，增加 warning
与 silent 日志配置，验证专用代理的节点 bootstrap、加密 DNS、fake-IP 与
代理故障不绕行。执行结果应以本次 PR CI 为准，不能用 macOS 的普通单元
测试声称这些 namespace 用例已运行。

NAS 验证需核对分支 commit、归档 SHA256、镜像 revision、新版本 `/usr`
缓存与运行容器；保留实际 QNET、持久配置和安全参数。再验证 reload、引擎
退出后的自动完整恢复、运行意图与停止意图下的容器重启以及持续 DNS 查询。
短期验证不能替代真实客户端 Gateway/PROXY/QUIC、NAS 重启或 24h/72h soak。
