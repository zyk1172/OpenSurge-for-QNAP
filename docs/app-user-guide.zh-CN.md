# OpenSurge for QNAP 使用指南

**简体中文** · [English](app-user-guide.md)

本指南面向通过 QNAP Container Station / Docker 使用 OpenSurge 的普通用户。部署细节见 [QNAP Docker 部署指南](../deploy/qnap/README.zh-CN.md)。

## 1. 打开 Web

容器启动后访问：

```text
http://<OpenSurge-IP>:8080
```

首次访问创建管理员账户。之后所有日常配置都在 Web 中完成。

QNAP 版默认是单容器产品，不需要额外的 Manager 或 Orchestrator 页面。

## 2. 容器网络与运行配置

以下参数在创建容器时确定：

- QNAP QNET 父网卡 / Virtual Switch；
- OpenSurge 静态 IPv4；
- LAN CIDR；
- 上游主路由 IPv4；
- `/data` 持久化目录。

这些值不是运行期 Web 配置。需要修改时，应保留 `/data`，修改 Compose/QNET 创建参数并重建容器。

Web 中管理的是 OpenSurge 自身的运行参数，例如 DNS、TUN、订阅、Provider、策略和设备规则。

## 3. 导入代理配置

进入 **代理与规则源**。

可以选择：

- HTTPS 订阅；
- 本地 `.yaml` / `.yml` Mihomo 配置。

导入后先生成持久化草稿，不会立即改变当前网关。

每个来源会显示配置结构、策略组、Provider、规则数量以及当前运行/下次启动状态。网关停止时可以 **设为下次启动版本**；网关运行时可以 **应用并重载**。

Web 会在后端操作后重新读取持久化状态，只有确认 `desired` 或 `applied` 已更新才显示成功。

## 4. 启动网关

进入 **网络设置** 或总览，点击 **启动网关**。

当前 QNAP same-LAN 模式使用 TUN + Linux policy routing。部分 QNAP 内核没有 `nf_tables`，这并不一定阻止透明代理；支持的 same-LAN 路径可以通过 ingress-interface policy routing 把客户端流量送入 TUN。

启动成功后应看到 Gateway、mihomo、DNS/dnsmasq、TUN 和 IPv4 forwarding 处于正常状态。

如果显示错误，先打开 **诊断**，运行 Doctor 并查看结构化错误和组件日志。

## 5. 让一台客户端接入

第一阶段不要修改主路由 DHCP。

只选一台测试设备，把：

```text
IPv4 Gateway = OpenSurge IP
DNS          = OpenSurge IP
```

然后依次验证：

1. 国内直连站点；
2. 需要代理的站点；
3. DNS；
4. UDP / QUIC；
5. 视频长连接；
6. 大文件下载。

验证完成后再逐步增加客户端。

## 6. 策略与设备

进入 **策略** 可以查看最终配置中的策略组、选择 Selector 当前节点、检查 Provider 和规则出口。

进入 **设备** 可以登记客户端，为设备指定独立出口或跟随全局规则，并查看当前连接和流量。

当前首版目标仍是 same-LAN manual gateway，不自动接管整个家庭 LAN 的 DHCP。

## 7. 修改 DNS / TUN 运行参数

进入 **网络设置 → 运行参数**。

可变值保存在：

```text
/data/config/opensurge.yaml
```

如果网关正在运行，Web 会执行：

```text
停止 → 持久化 → 重新读取校验 → 重新启动
```

任何一步失败都会明确报错，不应显示假成功。

## 8. 诊断

进入 **诊断** 可以查看 Doctor、Provider、活跃连接、组件日志、生命周期 Operations 和 Recovery 状态。

普通页面刷新不会自动运行完整 Doctor；需要时手工点击 **运行 Doctor**。

在缺少 `nf_tables` 的 QNAP 上，单独执行 `nft list ruleset` 失败并不代表 same-LAN 数据面失败。应以当前 data plane、TUN、`ip rule` 和专用 routing table 的实际状态为准。

## 9. 容器重建与升级

升级测试镜像时保留原来的 `/data` bind mount：

1. 下载并校验新测试镜像；
2. `docker load`；
3. 保留同一个 QNET 参数和 `/data`；
4. 重建 `opensurge` 容器；
5. 登录 Web 检查配置、订阅和管理员状态；
6. 再启动 Gateway。

不要为了升级删除 `/data/runtime`、`/data/control` 或整个持久化目录。

## 10. 异常重启后的恢复

如果容器或 NAS 在 Gateway 运行时被中断，OpenSurge 可能把上一次 runtime 标记为 interrupted。

此时先执行 **安全清理旧状态**。清理只针对 OpenSurge 持久化记录中能够证明 ownership 的对象，不应修改 QTS 默认网关、DNS 或其他容器网络。

清理完成后再重新启动 Gateway。

## 11. 当前边界

第一稳定版暂不提供：

- 自动修改 QNAP Network & Virtual Switch；
- 自动关闭/接管主路由 DHCP；
- 下游 IPv6 takeover；
- QPKG；
- 自动迁移整个家庭网络。

当前重点是把单容器、QNET、TUN、DNS、订阅、策略、设备管理和恢复流程在真实 QNAP 上做稳定。
