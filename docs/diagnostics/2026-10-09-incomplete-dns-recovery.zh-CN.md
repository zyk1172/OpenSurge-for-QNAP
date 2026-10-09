# 2026-10-09：重载失败后 DNS 未恢复

## NAS 现场证据

本次读取的容器镜像 revision 为 `2b5127ae6880404151dbbfe76d9149c0b3362777`。
容器自身仍运行，无 OOM、无容器重启；Mihomo、TUN、策略路由存在，但
SmartDNS 和 dnsmasq 均未运行。只有 Mihomo 的 loopback `1053` 监听，LAN
`53` 与本地 `5353` 均无监听，网关状态为 degraded。

下列时间为北京时间：

| 时间 | 持久化操作或服务日志 |
| --- | --- |
| 20:32:48 | 创建 reload 操作 |
| 20:34:26 | SmartDNS 收到 SIGTERM 并退出 |
| 20:34:29 | dnsmasq 收到 SIGTERM 并退出 |
| 20:34:30 | 完整网关停止、旧运行状态清除 |
| 20:34:40 | 新 Mihomo 开始初始化；geosite 加载耗时约 1897 ms |
| 20:34:43 | 启动因 `mihomo API not ready after 2s` 失败，进入回滚 |
| 20:34:47 | 回滚的 policy rule 检查超时，保留运行清理记录 |
| 20:34:51 | 创建自动 restart-mihomo 操作 |
| 20:35:00 | restart-mihomo 成功；没有重新启动任何 DNS 服务 |

## 已确认的代码缺陷

1. 运行记录在启动任何服务前写入，原格式没有启动完成标记。
2. 同一 boot/namespace 的记录都被视作 active，清理记录也符合引擎 watchdog 的条件。
3. 引擎的局部恢复只启动 Mihomo 并重新安装路由。
4. SmartDNS 缺少自动恢复；dnsmasq watchdog 检查 DHCP 状态，关闭 DHCP 时
   无法发现本地 DNS 缺失。因此局部恢复成功不能恢复 LAN DNS。
5. 完整启动会截断 Mihomo 旧日志，首次卡顿前的引擎日志因此无法追回。

旧镜像保留的 partial runtime 没有 DNS PID，但包含网络清理配方。
`TestLegacyPartialRuntimeCannotRestartOnlyMihomo` 在上述 revision 的独立源码
快照中能复现 engine-only restart 被错误接受；修复版拒绝这条局部路径。

## 宿主阻塞与证据边界

本次同时确认：持久数据位于 SSD，而 Docker root 位于
`/share/CACHEDEV1_DATA/Container/container-station-data/lib/docker`。
该卷的 device-mapper 链指向 `md1`。阻塞的容器 `ip` 内核栈包含
`__lock_page_killable`、`filemap_fault`、`ext4_filemap_fault`；采样时 `md1`
在途 I/O 曾达 874，宿主约有 3.6 GiB swap 占用。

这些证据确认现场存在文件页读取阻塞，可以触发短启动窗口和清理探测超时。
它们不能单独证明某个其他服务是源头，也不能追回 reload 之前首次 DNS
延迟/失败的完整因果链。升级镜像不会自动迁移 Docker root。

## 修复与验证范围

- 明确记录 starting/running/stopping/cleanup；不把未完成事务当成正常运行。
- 根据真实 DNS 与本地 DNS 状态选择完整恢复，仍检查持久运行意图和生命周期锁。
- 完整恢复复用 ownership/namespace 清理，然后依次启动引擎、本地 DNS、路由、LAN DNS。
- 普通引擎单独退出且 DNS 仍健康时继续使用局部恢复。
- 每次故障只自动尝试一次；操作完成后还需连续两次真实健康状态确认。
  持续阻塞或恢复失败时保留错误，并提供完整网关的手动恢复入口。
- Mihomo API 启动窗口改为有上限的 30 秒；完整启动保留两代旧日志。
- 故障注入覆盖启动失败、回滚失败、legacy journal、DHCP 关闭时的 DNS 缺失、
  手动停止意图及失败后不循环重启。
- Linux 隔离 namespace 测试使用真实 SmartDNS/dnsmasq 验证恢复后 DNS 包应答；
  该测试的引擎与网络后端为故障注入 seam，不等于真实 Mihomo/TUN 客户端验证。

NAS 镜像部署、真实客户端 TCP/UDP/QUIC、重启和长时间运行应单独报告，
不能以本地测试或 CI 成功替代。
