# 多 NAS 适配依据与验证边界

调研日期：2026-09-30。目标是让现有 Linux 单容器网关支持更多 NAS，不要求 NAS 安装 Go/Node，不改变主路由 DHCP。

## 结论

Mihomo、dnsmasq、TUN、IPv4 `ip rule iif` 策略路由和 `/data` 持久化可以复用。QNET 是 QNAP 特有部署接口；其他平台先使用 Docker macvlan，同样给容器一个独立 LAN IPv4。品牌身份通过 `OPENSURGE_NAS_PLATFORM` 显式指定，不能根据容器里的 `/etc/os-release` 猜测宿主品牌。

| 平台 | 容器与网络入口 | 当前范围 | 验证状态 |
| --- | --- | --- | --- |
| QNAP | Container Station / Compose + QNET | 现有 IPv4 same-LAN 旁路由；原有可选 Host Takeover | 沿用现有验证记录；本次不新增实机验收声明 |
| 群晖 | 支持 Container Manager 的 DSM 7 机型；SSH + Compose + macvlan | IPv4 same-LAN 客户端旁路由 | 实验性；未在 DSM 真机验证 |
| 飞牛 | fnOS Docker / Compose + macvlan | IPv4 same-LAN 客户端旁路由 | 实验性；未在 fnOS 真机验证 |
| 其他 Linux NAS | rootful Docker、Compose、macvlan、TUN、策略路由 | 满足能力检查的 IPv4 same-LAN 部署 | 按具体系统和型号验证，不承诺全部品牌 |

新平台不提供宿主机接管。Web 与远程 API 均阻止该入口，Control 不启动 QNAP Host Takeover 管理器。无需给新平台添加 `SYS_ADMIN`、host namespace 或 Docker socket。

## 官方来源及对应决策

1. [Docker macvlan 驱动](https://docs.docker.com/engine/network/drivers/macvlan/)：只支持 Linux、不支持 rootless；需要真实 parent；网络设备须接受同一端口上的多个 MAC。官方明确说明宿主机与 macvlan 容器默认无法直接通信。部署采用单个 macvlan 网络，Web 必须从另一台 LAN 设备访问。NAS 自身使用原有网关/DNS。
2. [群晖 Container Manager 项目](https://kb.synology.com/en-global/DSM/help/ContainerManager/docker_project?version=7)：项目支持上传或编辑 `docker-compose.yml`。文档描述删除项目会删除项目数据，因此数据目录应独立于项目工作目录，并先备份。
3. [群晖 Container Manager 网络](https://kb.synology.com/en-global/DSM/help/ContainerManager/docker_network?version=7)：GUI 网络创建文档列出的驱动是 bridge/host，不能据此声称界面可直接建立 macvlan。本项目推荐 SSH + Compose，是否可通过项目界面保持全部设置须实机确认。
4. [群晖 Container Manager 套件](https://www.synology.com/en-global/dsm/packages/ContainerManager)：套件有适用型号限制。ARM64 镜像可构建并不代表所有 ARM 群晖都支持套件；32 位 ARM 不在当前镜像范围。
5. [飞牛官方产品说明](https://www.fnnas.com/)：fnOS 基于 Linux / Debian。不能据此推定每台机器的 TUN、网卡名称和权限相同，仍须运行预检。
6. [飞牛官方 Docker 应用案例](https://developer.fnnas.com/docs/examples/docker/)：官方明确支持 Compose 交付、Docker 项目资源与 `.fpk` 打包，强调镜像需支持目标 x86/ARM 架构。其案例使用宿主端口映射，不能原样用于独立 IP 的旁路由。第一阶段交付 Compose；原生 `.fpk` 桌面入口和生命周期另行验收。
7. [群晖套件开发指南](https://help.synology.com/developer-guide/)：原生 `.spk` 是另一层安装包适配，不能把“Docker 可运行”等同于“可发布套件中心”。第一阶段不发布 `.spk`。

`ovs_eth0`、`eth0`、`bond0`、`enp1s0` 与 `/vol1/1000/...` 都只是示例；网卡、卷名、用户 UID/GID 必须由目标 NAS 的实际输出确定。没有官方依据说明它们是所有设备的固定值。

## 没有设备时如何推进

- 自动化检查：四个平台的 Compose、负面配置、平台标识、登录、Web/远程 API 能力边界与已有网关测试。
- 临时 Linux 容器：验证 TUN 创建、iif 策略路由、转发 lookup 和精确删除；不以此冒充 DSM/fnOS 验证。
- fnOS 可参照官方虚拟机安装帮助搭建测试环境，验证安装与恢复；虚拟交换机须允许多个 MAC。虚拟机测试不能代替物理交换机/网卡测试。
- 群晖可邀请拥有受支持型号的用户提供预检与客户端结果；无需提供订阅、令牌或管理密码。

## 本次本地验证记录

- `go test ./...`、`go vet ./...` 通过；平台身份、登录页与浏览器/远程 API 的宿主路由隔离有回归测试。
- Web 26 个测试文件、242 项测试通过；`OPENSURGE_TARGET=nas` 与 `qnap` 构建通过。
- 四个平台的 Compose / 静态预检通过；15 组错误配置被拒绝，包括越界/冲突 IP、非标准 CIDR、根用户与共享根目录。
- 一次性 Linux 容器实际创建 macvlan、真实 TUN，验证 TCP/UDP 的 ingress lookup；现有 ingress 路由、直连回退、精确清理与断路保护两项 integration test 通过，无跳过。该内核没有 dummy 模块，测试使用真实 TUN 回退。
- Docker daemon 实际接受单 IP `/32` 的 macvlan IPAM，临时测试网络已删除。
- 新增 GitHub Actions 门槛，但本次尚未运行远端 Actions；未构建/发布完整生产镜像，未取得 DSM/fnOS 真机或虚拟机验收结果。

## 每个平台升级为正式支持前的门槛

记录 NAS 型号、OS/内核/Docker/Compose 版本、CPU 架构、父接口与交换机环境，再完成：

1. 镜像加载、完整预检、独立 IP、Web 登录、账户与订阅重建后保留。
2. 一台真实客户端使用 OpenSurge 网关/DNS：DNS、国内 DIRECT、PROXY、TCP、UDP/QUIC、设备固定出口与 fake-IP。
3. Stop / 启动失败回滚后路由清理准确，NAS 管理和无关容器正常。
4. 容器重启与 NAS 重启自动恢复；接口、TUN 或存储晚于容器就绪时可诊断和重试。
5. 至少 24h / 72h 持续运行与系统升级后回归。

发布说明分别列出 CI、Linux namespace、NAS-side、真实客户端、reboot 和 soak 的证据；没有完成的项目继续标为未验证。
