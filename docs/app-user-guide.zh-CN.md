# OpenSurge for QNAP 使用指南

这份说明面向已经完成部署、准备日常使用的用户。

部署还没完成时，请先看：[QNAP 部署指南](../deploy/qnap/README.zh-CN.md)。

## 1. 打开 Web

浏览器访问：

```text
http://<OpenSurge-IP>:8080
```

例如：

```text
http://192.168.2.241:8080
```

默认无需用户名、密码或首次启动令牌，打开即进入管理页面。之后的日常操作基本都可以在 Web 中完成。

## 2. 导入代理配置

进入 **代理与规则源**。

支持：

- HTTPS Mihomo 订阅；
- 本地 `.yaml` / `.yml` 配置。

导入完成后，检查订阅、Provider、策略组和规则是否正常。

如果网关还没启动，可以把配置设为下次启动使用；如果网关已经运行，可以直接应用并重载。

## 3. 启动网关

进入 **网络设置**，点击 **启动网关**。

正常情况下应看到这些组件处于可用状态：

```text
Gateway
Mihomo
DNS
TUN
IPv4 转发
```

如果启动失败，不要反复点击，直接进入 **诊断 → 运行 Doctor** 查看原因。

## 4. 让设备经过 OpenSurge

先只测试一台设备。

把该设备的网络设置改成：

```text
IPv4 网关 = OpenSurge IP
DNS       = OpenSurge IP
```

例如：

```text
IPv4 网关 = 192.168.2.241
DNS       = 192.168.2.241
```

然后测试：

- 普通直连网站；
- 需要代理的网站；
- DNS 是否正常；
- 视频播放；
- 下载；
- UDP / QUIC 应用。

确认这台设备没问题后，再逐步给其他设备使用。

**不需要先修改整个家庭网络的 DHCP。**

## 5. 切换节点和策略

进入 **策略**。

这里可以：

- 查看策略组；
- 切换 Selector 当前节点；
- 查看 Provider 状态；
- 检查规则最终走哪个出口。

如果只是想换代理节点，通常只需要在这里操作，不需要重新导入订阅。

## 6. 给不同设备设置不同出口

进入 **设备**。

可以给单独的手机、电脑、电视等设置：

- 跟随全局规则；
- 固定走某个代理出口；
- 使用独立设备策略。

同时可以查看该设备的连接和流量情况。

## 7. 修改 DNS、TUN 等运行参数

进入 **网络设置 → 运行参数**。

配置会保存到：

```text
/data/config/opensurge.yaml
```

修改需要重启数据面的参数时，Web 会自动完成停止、保存和重新启动。

如果中间失败，会直接显示错误，不需要手工猜当前到底有没有生效。

## 8. 查看连接和流量

可以使用：

- **连通性**：检查目标是否可访问；
- **流量分析**：查看流量和连接；
- **设备**：查看某台设备的实际出口；
- **策略**：查看规则和策略组；
- **诊断**：查看组件日志和 Doctor 结果。

遇到“网页能开，但某些应用不能用”时，优先看 **连通性** 和 **诊断**。

## 9. 让 QNAP NAS 自己也走 OpenSurge

这个功能是可选的。

前提是部署时使用了：

```text
docker-compose.host-takeover.yml
```

之后在 Web 的网络设置中开启 **让 NAS 使用 OpenSurge**。

如果没有使用 Host Takeover 的 Compose 配置，Web 不应强行接管 QNAP 宿主网络。

详细说明：[NAS Host Takeover](QNAP_NAS_HOST_TAKEOVER.zh-CN.md)

## 10. 更新容器

更新镜像时保留原来的 `/data`：

```sh
docker compose pull
docker compose up -d
```

不要删除整个持久化目录。

更新后建议检查：

- 管理员账户能否正常登录；
- 订阅和策略是否还在；
- 网关能否正常启动；
- 测试设备能否正常联网。

## 11. 容器异常重启后怎么办

如果 NAS 断电、容器被强制终止，OpenSurge 可能检测到上一次运行被中断。

此时按照 Web 提示先执行 **安全清理旧状态**，再重新启动网关。

清理只应处理 OpenSurge 自己创建并能确认归属的网络状态，不会主动删除 QTS 原来的默认网关。

## 12. 常见问题

### Web 能打开，但设备不能上网

先检查设备是否同时设置了：

```text
网关 = OpenSurge IP
DNS  = OpenSurge IP
```

然后进入 **诊断 → 运行 Doctor**。

### `nft list ruleset` 报错，是不是坏了？

不一定。

QNAP 的 Same-LAN 模式可以使用 TUN + Linux policy routing 工作，不要求所有机型都具备可用的 `nf_tables`。

应以 Web 状态、TUN、`ip rule` 和实际联网结果为准。

### 换网卡或改 OpenSurge IP 怎么办？

这些属于容器创建参数，需要修改 `.env` 后重建容器。

只要保留 `/data`，用户配置不会因为重建容器自动丢失。

## 13. 当前限制

- 仅支持 IPv4 数据面；
- 不自动修改主路由 DHCP；
- 不自动修改 QTS「网络与虚拟交换机」；
- 不提供 IPv6 takeover；
- 不提供 QPKG。

更多问题见：[FAQ](faq.zh-CN.md)

## SmartDNS 上游 DNS

在 **网络设置 → DNS 服务 → SmartDNS 上游 DNS 设置** 中选择“自定义（推荐：加密优先）”，添加你信任的上游。提供国内与海外的 DoH 预设，也可以填写自己的服务。

| 预设 | DoH 地址 | 固定连接 IPv4 |
| --- | --- | --- |
| [阿里 DNS](https://www.alidns.com/) | `https://dns.alidns.com/dns-query` | `223.5.5.5` |
| [腾讯 DNSPod](https://www.dnspod.cn/products/publicdns) | `https://doh.pub/dns-query` | `1.12.12.12` |
| Cloudflare | `https://cloudflare-dns.com/dns-query` | `1.1.1.1` |
| Google | `https://dns.google/dns-query` | `8.8.8.8` |
| Quad9 | `https://dns.quad9.net/dns-query` | `9.9.9.9` |

- DoH 地址示例：`https://cloudflare-dns.com/dns-query`。
- DoT 地址示例：`tls://dns.google:853`。
- 加密上游需同时填写服务商的固定连接 IPv4，例如上述两项分别使用 `1.1.1.1` 和 `8.8.8.8`。该地址用于连接，证书仍按 URL 中的域名验证；无需依赖系统 DNS 解析上游域名。若服务商更换地址，需要更新它。
- 默认不允许明文 DNS。明确启用“允许明文 DNS”后，可填写 `9.9.9.9`、`udp://9.9.9.9:53` 或 `tcp://9.9.9.9:53`，明文地址不填写固定连接 IPv4。当同时有加密上游时，明文只作为加密上游全部失败后的备选，不能因为更快而抢先回答。

自定义模式只使用你填写的上游，不自动回退到系统 DNS 或主路由。先选择可信的解析服务，再让 SmartDNS 在所选上游的结果中进行连接速度选择。DoH/DoT 可防止传输途中篡改，但不能证明服务商的答案没有污染；预设不是无污染认证，Quad9 还会过滤已知恶意域名。

点击“保存上游 DNS”后，Web 会重新读取并核对持久化设置。运行中的网关会先停止、保存并验证，再重新启动；停止状态下保存则在下次启动生效。上方运行状态卡显示实际已写入的配置，上游列表表单显示下一次应用的设置，两者可能在网关停止时不同。

基础设置用于 **Resolver View 的真实 IP 解析**。还可以独立开启以下功能，两个开关默认关闭：

- **上游 DNS 查询经过 OpenSurge 代理**：填写当前配置中的代理节点或策略组名称，使用所选出口访问 DoH、DoT 或 TCP 上游。UDP 上游不能启用代理查询，请先换成加密或 TCP 上游。代理入口仅在容器回环地址监听，不需要开启 LAN SOCKS。SmartDNS 不在代理故障时改为直连；所选策略组的出口选择仍然生效，因此应选择实际使用代理节点的组。此时只比较可信上游经代理查询的响应延迟，不用 NAS 直连 IP 测速判断代理线路。
- **用 SmartDNS 替换 Mihomo DNS 上游**：覆盖订阅中的普通、直连、备用解析和 DNS 分流上游，Mihomo 从专用的 SmartDNS Resolver View 获得真实答案；保留 Fake-IP、Fake-IP 过滤规则、hosts 和 TUN DNS 劫持。Gateway View 仍先经过 Mihomo，不会直接返回真实 IP。关闭此开关后恢复配置文件中的 DNS 上游。

启用任一项后，**代理节点域名**会使用所选加密上游直接解析，通过固定 IPv4 连接并验证证书域名。这条独立路径不依赖尚未启动的 SmartDNS，也不经过它正在引导的代理，避免启动或运行时的解析循环；至少需要一个加密上游。明文上游不参与节点域名引导。启用这两项时，DoH 地址不能含查询参数。普通查询是否经代理由第一个开关决定。

NAS 宿主 DNS 接管模式仍是独立设置。旧配置继续使用“系统 DNS（兼容旧配置）”，该模式不提供自定义上游的信任约束，也不能开启上述两项，建议手动切换到自定义。运行状态卡中的“Resolver View 查询出口”来自已写入的 SmartDNS 配置。

手动编辑配置文件时，新字段使用现有逗号分隔格式：

```yaml
dns:
  resolver_mode: "custom"
  resolver_upstreams: "https://cloudflare-dns.com/dns-query|1.1.1.1,tls://dns.google:853|8.8.8.8"
  resolver_allow_plaintext: false
  resolver_proxy_enabled: false
  resolver_proxy_target: ""
  mihomo_use_smartdns: false
```

每项 `|` 后为固定连接 IPv4；明文上游不使用 `|`。该片段需合并到已有 `dns` 节点中，并保留监听等其他设置。

要同时启用代理查询和替换解析，将 `resolver_proxy_enabled` 与 `mihomo_use_smartdns` 改为 `true`，并把 `resolver_proxy_target` 填为当前配置里的节点或策略组名称；名称不存在时保存会被拒绝。内部 `127.0.0.1:5354` 与 `127.0.0.1:7854` 分别用于这两项功能，启用时不得与其他配置端口重复。
