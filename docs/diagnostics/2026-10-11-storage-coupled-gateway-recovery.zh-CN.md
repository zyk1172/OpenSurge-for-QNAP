# 2026-10-11：运行程序受机械盘阻塞及恢复失败后永久停机

## 本次 NAS 现场（北京时间）

- SSH 可连接；`opensurge` 容器仍为 10 月 10 日部署的 `5dfc2b9f`，running，
  restart=0、OOM=false。网关与 DNS/TUN 停止，desired_running=true，readiness=503。
- 12:54 自动完整恢复 succeeded。13:25:49 SmartDNS 仍返回 iCloud DNS 答案；
  13:25:50 收到 signal 15，13:25:56 dnsmasq 收到 SIGTERM。13:25:59 完整停止。
- 13:26:52 创建自动恢复；13:27:41 失败：Mihomo TUN API 30 秒窗口超时，回滚后无 runtime。
  保留的两代引擎日志显示 API 已监听，但监听与 TUN 初始化仍有十几秒至几十秒的停顿。
- 17:21 手动 start 最后停在 validating_network，17:22 返回 context deadline exceeded。
  容器中的 `ip` 曾处于 D 状态；17:23 宿主 load=49.43/62.44/71.14，md1 在途 I/O=433。
  多个宿主服务与 dm-thin/jbd2 也有 D 状态，不能单独据此指定某个其他服务为首因。
- Hosts 自动同步开启，周期 30 分钟；后台完整停启反复出现在该周期边界。
  SIGTERM 与停止前真实应答说明本次停止至少包含主动生命周期操作，不能简单归类为 DNS 自己崩溃。
  旧版本没有为后台 Hosts 应用保存操作记录，因此单次触发者还缺少直接的 operation ID 证据。

上一轮已捕获 Mihomo 在 `filemap_fault/ext4_filemap_fault` 等待读取机械盘程序页。
当前 `/data` 在 SSD，Docker root 仍在 CACHEDEV1_DATA 机械盘。只放配置到 SSD
不会迁移程序、动态库及网络预检命令；高负载下自动更新停止网关后，冷启动更容易失败。

## 更新到 PR #91 的修复

### 隔离运行程序与镜像层的机械盘读取

增加可选 `OPENSURGE_RUNTIME_IMAGE_CACHE=true`。在 Debian NAS 镜像启动时：

1. 读取镜像构建时生成的可信 `/usr` 文件与符号链接 manifest。
2. 在 `/data/runtime-image-cache/<manifest SHA256>/usr` 复制、核对完整 `/usr`。
3. 在容器自己的 mount namespace 内，将已验证缓存 bind mount 到 `/usr`，并设为只读。

因此引擎、Control/Web、DNS、ip/nft、ELF loader 与共享库使用同一镜像版本的
运行文件；`/bin` 与 `/lib` 的 Debian merged-/usr 链接也指向该树。
不是一直绑定某个旧版本的外部目录：新镜像 manifest 不同就使用新目录，每次重启
核对全部文件、链接、ownership 和写权限。损坏、额外文件及不安全目录均 fail closed，
不删除旧缓存或用户配置。

该选项默认关闭。仅对 SSD 上的 `/data` 有存储隔离效果；需要容器已有 SYS_ADMIN
以及允许 bind mount 的容器安全配置。QNAP Host Takeover 容器已有该能力时可使用，
不需要 privileged、Docker Socket 或全局 Docker root 迁移。不修改宿主 mount namespace。
首轮复制和每次完整校验会增加启动时间、SSD 空间与读取开销，Docker 启动期间的
初始镜像加载、非 /usr 文件、内存/CPU 调度和宿主整体失效仍不由该缓存解决。

### 暂时失败后恢复运行意图

旧 watchdog 每次事件只尝试一次；失败后的 attempted/failed 阻止所有后续自动操作，
即使宿主恢复也一直停机。本次仅对 NAS 完整恢复中确认的 deadline/command timeout
安排退避重试：30 秒、60 秒、120 秒递增，最多 15 分钟间隔。未知状态不会触发动作，
每次仍检查运行意图、生命周期锁、namespace 和 ownership；手动 stop 取消计划。
权限、identity/ownership 拒绝和配置错误仍不自动重试，复合错误中的拒绝优先。
API 保留原错误，并给出 next_retry_at 与 retry_attempts。成功需连续健康确认后清零。
手动 start 同样更新恢复控制器，避免旧失败事件压制这次新的明确运行意图。

### 自动更新的可追溯性

Hosts 自动应用现在保存 `sync-host-hosts` 操作、阶段及终态错误；无实际 profile
变化时不创建操作。不再只留下通用的 Gateway stopped 日志。

## 验证边界

回归测试覆盖：完整缓存复用、镜像升级版本隔离、文件/链接篡改、额外文件、
写权限、路径穿越；恢复超时退避、上限、未知观测、正常停止及永久错误。
CI 的私有临时容器实际验证 `/usr` 与 `/data` 使用同一设备、只读挂载，以及
Control、Mihomo、dnsmasq 和网络命令可执行。实机结果应另行记录；静态测试
不能证明机械盘工作负载已消除，也不能代替真实客户端或 24h/72h 稳定性测试。
