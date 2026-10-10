# 2026-10-10：PR #90 的 QNAP 实机验证

本文时间均为北京时间。测试对象为 QNAP 上的实际 `opensurge` 容器，
不以 CI、容器 running 或 healthcheck 单独代替功能验证。

## 镜像与部署

- 运行代码 revision：`5dfc2b9f956b54932a8bedbfeebdd3ed8b6fe7fc`。
- 来源：GitHub Actions `publish-qnap-test-image.yml`，run `37955485438` 的
  amd64 artifact。amd64 构建完成后取消剩余任务，未更新全局滚动测试发布。
- Docker image ID：`sha256:c0321e3fefaa9deda004380c582ccb77a2b075d472f49a91b98ff7a05d28fbc3`。
- 归档 SHA256：`dc4f3cc13f14057fe8a57028dc0a7f9b5222d21d82241785a18ccfc694dffe66`；
  Mac 与 NAS 校验一致，加载后核对镜像 revision。
- 最终使用标准部署：QNET `opensurge-qnet`、IPv4 `192.168.2.241`、
  MAC `02:42:1b:84:80:28`、上游 `192.168.2.1`。
- 保留原 `/share/SSD/container/opensurge:/data`、host-hosts 和 host-netns
  三个挂载，旧容器与镜像保留用于回滚。

## 测试时追加的代码修复

1. `81dfffcb`：完整恢复 handler 已实现，但未注册到 Control API mux。
   补充经过认证的 `POST /api/v1/gateway/recover-gateway` 路由，并添加
   认证、非容器 runner 和生命周期锁的回归测试。
2. `5dfc2b9f`：SmartDNS Resolver View 采用容器 `/etc/resolv.conf` 中的
   `127.0.0.11` 时，公网查询持续 SERVFAIL，尽管本地域名查询与进程状态正常。
   Docker daemon DNS 配置包含宿主 Tailscale DNS `100.100.100.100`；现场宿主
   查询该地址超时，上游路由器查询成功。但容器直接查询 Docker stub 曾成功，
   因此不能把 stub 描述为始终不可达，也未证明所有 SERVFAIL 的内部因果链。
   修复排除 namespace 内的 loopback stub、自身 LAN IP、未指定与组播地址，
   无真实系统 resolver 时使用已配置的真实 upstream gateway。
   Resolver View 与 Gateway View 继续隔离，后者仍走 Mihomo `127.0.0.1:1053`。
3. 实机冷启动另外触发了原 10 秒 TUN 初始化窗口；将该有界窗口提高至 30 秒。
   持续 I/O 阻塞仍可超过此上限，失败时仍保留恢复记录。

上述追加变更的相关 Go 测试与 vet 通过；`5dfc2b9f` 的 18 项 PR CI 检查均成功，
包括 Linux namespace、amd64/arm64 镜像构建与容器 smoke。

## 标准部署实测

故障注入只终止本容器 runtime 记录中的 owned DNS PID；没有停止无关容器。
使用 Mac 从 LAN 对 OpenSurge `:53` 发送实际 UDP/TCP 公网域名查询，
同时观察 readiness、持久运行意图、生命周期和自动恢复操作。

| 项目 | 实测结果 |
| --- | --- |
| SmartDNS 退出 | 00:29:44 终止；完整恢复操作 00:30:24 成功，00:30:30 LAN UDP/TCP DNS 与 readiness 恢复，约 46 秒 |
| dnsmasq 退出，DHCP 关闭 | 00:34:20 终止；状态识别 local_dns=stopped，选择完整恢复，00:34:33 LAN DNS 与 readiness 恢复，约 13 秒 |
| 正常停止 | 00:34:43 操作 succeeded，约 1 秒；00:34:55 仍 desired_running=false、网关停止、watchdog idle |
| 停止清理 | 专用 table 20241 与 owned policy rules 清除；main 的默认路由仍为 192.168.2.1 |
| 正常启动 | 00:35:05 操作 succeeded，约 6 秒，恢复全部组件 |
| 正常重载 | 00:35:19 操作 succeeded，约 7 秒；公网 UDP/TCP DNS 与 readiness 正常 |
| TCP/UDP 路由检查 | 从 eth0 ingress 的两个 forwarding lookup 均指向 tun0、table 20241 |
| 引擎显式 HTTP 代理 | 通过容器 127.0.0.1:7890 请求 generate_204，HTTP 204；本次标准部署采样耗时 7.05 秒 |

停止阶段 readiness=200 表示“符合停止意图”，不是 DNS 仍在提供服务。
测试工具要求 desired_running=true，故该停止阶段采样返回非零是预期结果。
路由 lookup 与显式代理请求不能代替真实客户端透明 TCP/UDP/QUIC 验证。

## 本轮仍复现了宿主磁盘阻塞

此前运行本 PR 的 `81dfffcb` 镜像时，约 00:00–00:18 期间，出现分钟级响应停顿：

- NAS 和容器 ping 正常，SSH/Web TCP 握手正常；QTS HTTP 能响应，
  OpenSurge health/live 与 readiness 在 5 秒内收到零字节，UDP/TCP DNS 超时。
- 容器仍 running，无 OOM、无容器重启；SSH 中的 Docker 诊断也延迟数分钟。
- 宿主 load 曾达 82.72，`md1` 在途 I/O 为 1374，29 个主进程处于 D 状态，
  同时涉及多个其他服务。此采样未确定哪个工作负载首先引发阻塞。
- Mihomo 的宿主 PID 8836 内核栈为 `__lock_page_killable → filemap_fault →
  ext4_filemap_fault → __do_fault → handle_mm_fault`；映射对象为
  `/usr/local/bin/mihomo`。这是等待可执行文件页的实际证据。
- `/data` 位于 SSD，但 Docker root 仍为机械盘上的
  `/share/CACHEDEV1_DATA/Container/container-station-data/lib/docker`，其设备链指向 md1。
- 在途 I/O 降至 1 后，Web 自行恢复 HTTP 200，没有通过重启容器恢复。

因此应用恢复缺陷和宿主存储停顿在本轮都有证据；修复恢复路径不等于消除
机械盘上的文件页阻塞，也不能把本次运行表现称为所有断网均已彻底解决。

## 临时 SSD 实验及恢复

从同一官方镜像提取 `/usr` 到 SSD，校验四个主要程序的 SHA256，临时只读挂载。
该实验下 SmartDNS 与 dnsmasq 退出后均约 14 秒恢复；手动完整恢复接口
实际操作 succeeded（约 1.13 秒）。但测试时未再次出现 load 82 的峰值，
没有证据证明这个实验可以排除严重宿主停顿。

实验后撤掉额外挂载，重新创建标准部署再进行上述测试，避免后续升级
继续使用旧 `/usr` 文件。最终运行容器不使用这份缓存；实验容器已停止并
从 QNET 断开。未迁移全部 Docker 数据、未重启 NAS、未改动整网 DHCP。

## 尚未验证

- 真实客户端作为默认 Gateway 的完整 DIRECT、PROXY、TCP、UDP/QUIC 路径。
- NAS 重启与 24h/72h soak。
- 最终代码在本轮已观测的严重 md1 阻塞条件下的持续可用性。
- 引发宿主存储饱和的首个工作负载及长期存储方案。

PR #90 保持开放，等待审查，未合并。
