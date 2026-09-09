export type RequestedLanguage = 'system' | 'zh-Hans' | 'en'
export type ResolvedLanguage = 'zh-Hans' | 'en'

const languageCacheKey = 'opensurge-ui-language'
const productTarget = (import.meta.env.VITE_OPENSURGE_TARGET ?? 'mac').trim().toLowerCase()

// Shared Web pages still serve the upstream Mac target as well as QNAP. Keep
// platform-neutral business components shared, but replace desktop-only copy at
// the translation boundary so the QNAP bundle never describes its container as
// a Mac or claims PF/macOS network behavior.
const qnapSourceOverrides: Record<string, string> = {
  '本机 Mac': 'OpenSurge 网关',
  '分别设置当前 Mac 和下游设备如何选择出口；两者互不影响。': '设置 OpenSurge 网关与下游设备的出口和分流规则；两者互不影响。',
  '当前 Mac 的设备设置': 'OpenSurge 网关本机设置',
  '尚未登记设备。使用上方“登记新设备”可从当前经过 Mac 的设备开始。': '尚未登记设备。使用上方“登记新设备”可从当前经过 OpenSurge 网关的设备开始。',
  'OpenSurge 会先验证完整候选配置。验证通过后，DHCP/DNS、mihomo、PF 与 IPv4 forwarding 会短暂重启。': 'OpenSurge 会先验证完整候选配置。验证通过后，DNS、mihomo 与 TUN 数据面会短暂重载。',
  '继续使用订阅或托管的网关规则；不跟随 Mac 本机的规则 / 全局 / 直连开关。': '继续使用订阅或托管的网关规则；与 OpenSurge 网关本机出口选择相互独立。',
  '已观察到邻居 MAC {{mac}}，等待该 IPv4 经过 Mac': '已观察到邻居 MAC {{mac}}，等待该 IPv4 经过 OpenSurge 网关',
  '固定 IPv4 已登记，等待流量经过 Mac': '固定 IPv4 已登记，等待流量经过 OpenSurge 网关',
  '静态配置身份：等待该 IPv4 经过 Mac': '静态配置身份：等待该 IPv4 经过 OpenSurge 网关',
  '固定 IPv4 策略：等待该地址经过 Mac': '固定 IPv4 策略：等待该地址经过 OpenSurge 网关',
  '从当前经过 Mac 的 LAN 流量发现设备，再确认静态身份与路由方式': '从当前经过 OpenSurge 网关的 LAN 流量发现设备，再确认静态身份与路由方式',
  '当前经过 Mac 的设备': '当前经过 OpenSurge 网关的设备',
  '经过 Mac': '经过网关',
  '当前尚未观察到经过 Mac 的 LAN 设备；可以直接按固定 IPv4 手工登记。': '当前尚未观察到经过 OpenSurge 网关的 LAN 设备；可以直接按固定 IPv4 手工登记。',
  '默认推荐；继续使用订阅或托管的网关规则，不跟随 Mac 本机模式。': '默认推荐；继续使用订阅或托管的网关规则，与网关本机出口选择相互独立。',
  '通过当前 applied 配置与 Mac 本机运行模式访问真实服务，展示三轮中位延迟、命中规则和实际出口链。': '通过当前 applied 配置从 OpenSurge 网关容器访问真实服务，展示三轮中位延迟、命中规则和实际出口链。',
  'Mac 本机运行路径': 'OpenSurge 网关路径',
  '本机浏览器线路': '当前浏览器线路',
  '这会验证当前 applied 配置并恢复 Mihomo。DHCP/DNS、PF、IPv4 forwarding 和 Mac 网络设置不会改变；现有代理连接会重新建立。继续吗？': '这会验证当前 applied 配置并恢复 Mihomo。DNS、TUN 与容器网络配置不会改变；现有代理连接会重新建立。继续吗？',
  '连续异常确认后会自动执行一次 Mihomo-only 恢复，不改动 DHCP/DNS、PF 或 IPv4 forwarding。': '连续异常确认后会自动执行一次 Mihomo-only 恢复，不改动 DNS、TUN 策略路由或 QNAP 宿主网络。',
  '可以手动重试这条 Mihomo-only 恢复路径；不会停止 DHCP/DNS、卸载 PF 或修改 Mac 网络设置，旧 Mihomo 日志会先归档。': '可以手动重试这条 Mihomo-only 恢复路径；不会修改 QNAP 宿主网络或持久化网络配置，旧 Mihomo 日志会先归档。',
  '证据范围：': '证据范围：',
  '这里的请求由 Mac 上的 Control Service 经 mihomo mixed-port 发起，会经过当前 Mac 本机规则 / 全局 / 直连模式。它不证明下游设备的网关规则、设备级 SRC-IP、DHCP、DNS 或 TUN；选择全局或直连时，分流判断基线出现差异可能正是当前模式的结果。HTTP 响应表示网络可达，不等同于已登录后的完整产品功能可用。': '这里的请求由 OpenSurge 容器内 Control Service 经 mihomo mixed-port 发起，只证明网关本机的 applied 路径。它不证明下游客户端的实际网关、DNS、设备级 SRC-IP 或应用行为；HTTP 响应表示网络可达，不等同于已登录后的完整产品功能可用。',
}

const english: Record<string, string> = {
  '语言': 'Language',
  '界面语言': 'Interface language',
  '选择 OpenSurge Web GUI 和菜单栏使用的语言': 'Choose the language used by the OpenSurge Web GUI and menu bar',
  '跟随系统': 'Follow System',
  '简体中文': '简体中文',
  '英语': 'English',
  '系统语言：{{language}}': 'System language: {{language}}',
  '正在保存语言…': 'Saving language…',
  '总览': 'Overview',
  '网络设置': 'Network',
  '代理与规则源': 'Sources',
  '设备': 'Devices',
  '策略': 'Policies',
  '连通性': 'Connectivity',
  '诊断': 'Diagnostics',
  '设备页还有尚未保存的修改，确定离开并放弃这些修改吗？': 'The Devices page has unsaved changes. Leave and discard them?',
  '阻止空闲睡眠和合盖睡眠。合盖运行可能明显增加耗电与发热，请勿放入不通风的包内。': 'Prevent idle and lid-closed sleep. Running with the lid closed can noticeably increase power use and heat; do not place the Mac in an unventilated bag.',
  '正在切换…': 'Changing…',
  '合盖保持运行': 'Keep running with lid closed',
  '系统睡眠已临时禁用': 'System sleep is temporarily disabled',
  '默认关闭 · 本次运行有效': 'Off by default · This session only',
  '切换为浅色模式': 'Switch to light mode',
  '切换为深色模式': 'Switch to dark mode',
  '浅色模式': 'Light mode',
  '深色模式': 'Dark mode',
  'Web GUI 与 OpenSurge 的安全连接已过期': 'The secure connection between the Web GUI and OpenSurge has expired',
  '请点击 macOS 菜单栏中的 OpenSurge 图标，然后选择“打开 OpenSurge 面板”。': 'Click the OpenSurge icon in the macOS menu bar, then choose “Open OpenSurge Dashboard”.',
  '重试': 'Retry',
  '局域网 DHCP 接管': 'Same-LAN DHCP Takeover',
  '让现有局域网设备自动接入 OpenSurge': 'Automatically connect devices on the existing LAN to OpenSurge',
  '自动接管': 'Automatic takeover',
  '保持自动获取': 'Keep automatic network settings',
  '关闭主路由 DHCP': 'Disable DHCP on the main router',
  '局域网中使用自动网络设置的设备': 'LAN devices using automatic network settings',
  'OpenSurge 会通过引导流程，协助你逐步完成网络设置、启动确认和停止后的网络恢复。': 'OpenSurge guides you through network setup, startup confirmation, and network recovery after stopping.',
  '主路由关闭 DHCP，OpenSurge 为现有局域网中的设备提供 DHCP、DNS 和默认网关。': 'The main router stops DHCP, and OpenSurge provides DHCP, DNS, and the default gateway to devices on the existing LAN.',
  '旁路由模式': 'Selective Gateway',
  '仅让局域网内的部分设备通过 OpenSurge 上网': 'Route only selected LAN devices through OpenSurge',
  '部分设备': 'Selected clients',
  '主路由': 'Main router',
  '在部分设备上手工设置网关与 DNS': 'Set the gateway and DNS manually on selected devices',
  '只修改需要接入的设备': 'Change only the devices you want to connect',
  '手工设置为使用 OpenSurge 的设备': 'Devices manually configured to use OpenSurge',
  '主路由和其他设备保持原有网络设置；没有手工设置网关的设备不受影响。': 'The main router and other devices keep their existing settings; devices not pointed to OpenSurge are unaffected.',
  '主路由保持 DHCP，只在部分设备上手工把固定 IPv4、默认网关和 DNS 指向 OpenSurge。': 'The main router keeps DHCP enabled; only selected devices manually use a fixed IPv4 and point their default gateway and DNS to OpenSurge.',
  '独立下游 LAN': 'Isolated Downstream LAN',
  '通过独立 AP、SSID 或 VLAN 接入 OpenSurge': 'Connect to OpenSurge through a dedicated AP, SSID, or VLAN',
  '独立网络': 'Isolated network',
  'OpenSurge（仅下游）': 'OpenSurge (downstream only)',
  '连接下游网络后自动获取': 'Connect downstream and obtain settings automatically',
  '准备独立下游网络和接口': 'Prepare a separate downstream network and interface',
  '连接到下游网络的设备': 'Devices connected to the downstream network',
  '上游路由器通常无需改变；OpenSurge 只接管独立下游网络中的设备。': 'The upstream router usually needs no changes; OpenSurge takes over only devices on the isolated downstream network.',
  'Mac 使用不同接口连接上游路由器和独立下游网络，并为全部下游设备提供 DHCP、DNS 和默认网关。': 'The Mac uses separate interfaces for the upstream router and isolated downstream network, providing DHCP, DNS, and the default gateway to all downstream devices.',
  'DHCP 来源': 'DHCP source',
  '设备设置': 'Device setup',
  '需要操作': 'Required action',
  '接入范围': 'Affected devices',
  '公网': 'Public network',
  '现有局域网 · Wi-Fi 或以太网': 'Existing LAN · Wi-Fi or Ethernet',
  '主路由 / AP': 'Router / AP',
  'DHCP 关闭': 'DHCP off',
  'LAN 地址保留': 'Keep LAN address',
  'IPv6：主路由 RA 关闭': 'IPv6: router RA off',
  '规则与转发': 'Rules & forwarding',
  'IPv6 用户态': 'IPv6 userspace',
  '局域网设备': 'LAN devices',
  '手机 · 电视': 'Phones · TVs',
  '游戏机 · IoT': 'Consoles · IoT',
  'IPv6 可选': 'Optional IPv6',
  '自动获取设置': 'Automatic setup',
  '自动下发 DHCP / DNS': 'Automatic DHCP / DNS',
  '可选 IPv6 RA / DNS': 'Optional IPv6 RA / DNS',
  '设备上网路径': 'Device traffic',
  '可选 IPv6 下发': 'Optional IPv6',
  'DHCP 保持开启': 'DHCP stays on',
  '其他设备不变': 'Others unchanged',
  '选定设备移除 IPv6 默认路由': 'No router IPv6 route',
  'DNS · 不发 DHCP': 'DNS · No DHCP',
  'IPv6 不发 RA': 'No IPv6 RA',
  '固定 IPv4': 'Fixed IPv4',
  '网关 / DNS → Mac': 'Gateway / DNS → Mac',
  'IPv6 ULA · 手工': 'IPv6 ULA · Manual',
  '仅选定设备': 'Selected only',
  '手工设置固定 IPv4、网关与 DNS': 'Set IPv4, gateway & DNS',
  'IPv6：ULA / 链路本地网关与 DNS': 'IPv6: ULA / link-local gateway & DNS',
  'IPv4 手工设置': 'Manual IPv4 setup',
  '可选 IPv6 手工接入': 'Optional manual IPv6',
  '上游网络': 'Upstream network',
  '上游路由': 'Router',
  'DHCP 开启': 'DHCP on',
  '设置不变': 'Unchanged',
  '上游接口': 'Upstream',
  '连接路由器': 'To router',
  '下游接口': 'Downstream',
  '独立 SSID': 'Dedicated SSID',
  '独立网段': 'Separate subnet',
  '下游设备': 'Clients',
  '自动获取': 'Automatic',
  '全部接入': 'All connected',
  '仅向下游下发 DHCP / DNS': 'Downstream DHCP / DNS only',
  '可选下发 IPv6 RA / DNS': 'Optional downstream IPv6',
  '下游 IPv4 DHCP / DNS': 'Downstream IPv4 DHCP / DNS',

  // QNAP product surface. Keep these in the eager catalog so the QNAP-only
  // bundle has a complete English fallback before the lazy catalog arrives.
  'QNAP 网关，一眼可见': 'QNAP gateway at a glance',
  '上一次网关运行被容器或 NAS 重启中断。': 'The previous gateway run was interrupted by a container or NAS restart.',
  '查看 QNAP 容器的数据面、Provider、连接和日志；不会执行任何 QTS 宿主网络修改。': 'Inspect the QNAP container data plane, providers, connections, and logs. No QTS host-network changes are performed.',
  '点击“运行 Doctor”后才会执行完整检查；普通 Web 刷新不会触发。': 'A full check runs only after you click “Run Doctor”; ordinary Web refreshes do not trigger it.',
  '从这里观察和刷新 Provider': 'Inspect and refresh providers here',
  '正在后台执行完整检查，不阻塞总览刷新': 'Running the full check in the background without blocking overview refreshes',
  '旧运行状态已安全清理。': 'Stale runtime state was cleaned safely.',
  '网关已停止。': 'The gateway has stopped.',
  '网关已启动。': 'The gateway has started.',
  'QNAP 恢复完成': 'QNAP recovery complete',
  'QNAP 网关已启动': 'QNAP gateway started',
  'QNAP 网关已停止': 'QNAP gateway stopped',
  'QNAP 网关操作失败': 'QNAP gateway operation failed',
  '配置保存后的版本校验失败，请重新读取后再试。': 'Post-save revision verification failed. Reload the configuration and try again.',
  'QNAP 运行参数已保存': 'QNAP runtime settings saved',
  '网关已使用保存后的配置重新启动。': 'The gateway restarted with the persisted configuration.',
  '配置已持久化并重新启动网关。': 'The configuration was persisted and the gateway restarted.',
  '配置已持久化到 /data；下次启动仍会保留。': 'The configuration was persisted to /data and will remain on the next start.',
  '保存 QNAP 运行参数失败': 'Failed to save QNAP runtime settings',
  '由 Docker/QNET 创建参数确定': 'Determined by Docker/QNET creation parameters',
  'QNAP 网关网络': 'QNAP gateway network',
  '物理网卡、QNET、静态 IP、CIDR 与主路由在创建容器时确定；运行中的容器不再伪装成可以修改 QNAP 宿主网络。': 'The physical NIC, QNET, static IP, CIDR, and upstream router are fixed when the container is created; a running container no longer pretends it can modify QNAP host networking.',
  '检测到上一次容器或 NAS 重启留下的运行状态。': 'Runtime state left by a previous container or NAS restart was detected.',
  '先执行安全清理；QNAP 版不会触碰宿主 QTS 网络设置。': 'Run safe cleanup first. The QNAP build will not modify host QTS network settings.',
  '操作完成': 'Operation completed',
  '容器创建时网络': 'Container creation network',
  '只读 · 修改以下任一项需要重建 OpenSurge 容器': 'Read-only · changing any item below requires recreating the OpenSurge container',
  '正在读取容器实际网络…': 'Reading the container network…',
  '容器接口': 'Container interface',
  'QNET 父网卡不在容器内二次选择': 'The QNET parent NIC is not selected again inside the container',
  'QNAP 物理网卡 / Virtual Switch 由 Docker Compose 的 qnet iface 在创建容器时绑定。容器内部通常只看到 eth0，这是正常现象。': 'The QNAP physical NIC / Virtual Switch is bound by Docker Compose qnet iface when the container is created. Seeing only eth0 inside the container is normal.',
  '运行参数': 'Runtime settings',
  '这些值保存在 /data/config/opensurge.yaml；网关运行时保存会自动执行停止 → 持久化 → 重新启动。': 'These values are stored in /data/config/opensurge.yaml. Saving while the gateway is running automatically performs stop → persist → restart.',
  'DNS 上游': 'DNS upstream',
  '仅控制 OpenSurge DNS 行为，不修改 QNAP 宿主网络。': 'Controls OpenSurge DNS behavior only; it does not modify QNAP host networking.',
  '持久化 mihomo fake-ip 映射。': 'Persist mihomo fake-IP mappings.',
  '仅影响容器内透明代理数据面。': 'Affects only the transparent-proxy data plane inside the container.',
  '重新读取': 'Reload',
  '保存并重启网关': 'Save and restart gateway',
  '保存运行参数': 'Save runtime settings',
  '如何修改物理网卡或静态 IP': 'How to change the physical NIC or static IP',
  '这是容器部署操作，不是 OpenSurge 运行配置': 'This is a container deployment operation, not an OpenSurge runtime setting',
  '停止并重建容器时修改 Compose 中的 QNET 父接口、IPv4、CIDR 或主路由。保留同一个 /data 持久化目录即可保留管理员、订阅、规则、设备策略和运行配置。': 'Change the QNET parent interface, IPv4, CIDR, or upstream router in Compose when recreating the container. Reuse the same /data directory to keep administrators, subscriptions, rules, device policies, and runtime settings.',
  '{{name}} 已导入并保存为草稿。': '{{name}} was imported and saved as a draft.',
  '后端返回成功，但重新读取后没有发现 desired/运行版本标记；本次操作未被视为成功。': 'The backend returned success, but a reread found neither desired nor applied state. This operation is not treated as successful.',
  '{{name}} 已持久化并成为当前运行版本。': '{{name}} was persisted and is now the running version.',
  '订阅已应用': 'Subscription applied',
  '重新读取配置后已确认当前网关正在使用该版本。': 'A configuration reread confirmed that the gateway is using this version.',
  '{{name}} 已持久化为下次启动版本；重新读取配置后已确认 desired 状态。': '{{name}} was persisted for the next start; a reread confirmed the desired state.',
  '下次启动版本已保存': 'Next-start version saved',
  '该选择已写入 /data/config，容器或网关重启后仍会保留。': 'This selection was written to /data/config and will survive container or gateway restarts.',
  '订阅应用失败': 'Subscription apply failed',
  '{{name}} 的容器内快照路径已复制。': 'The in-container snapshot path for {{name}} was copied.',
  '状态已重新确认': 'State reconfirmed',
  '导入只创建持久化草稿；点击应用后会再次做完整候选配置校验。': 'Import creates a persistent draft only. Applying it runs full candidate validation again.',
  '订阅已保存为草稿。': 'The subscription was saved as a draft.',
  '正在导入…': 'Importing…',
  '状态来自重新读取后的持久化配置，不把一次 HTTP 200 当成已经保存。': 'State comes from a reread of persisted configuration; one HTTP 200 is not treated as proof of persistence.',
  '容器持久化快照': 'Persistent container snapshot',
  '位于 /data 下，由 OpenSurge 管理': 'Stored under /data and managed by OpenSurge',
  '候选配置可应用': 'Candidate configuration is applicable',
  '候选配置存在问题': 'Candidate configuration has issues',
  '{{name}} 已刷新为新草稿。': '{{name}} was refreshed into a new draft.',
  '应用订阅并重载 QNAP 网关？': 'Apply the subscription and reload the QNAP gateway?',
  '当前网关未运行。确认后会把所选版本写入 /data/config，随后重新读取配置确认 desired 状态；容器重启不会丢失该选择。': 'The gateway is stopped. Confirming writes the selected version to /data/config and rereads it to verify desired state; recreating or restarting the container will not lose the selection.',
  '正在验证并持久化…': 'Validating and persisting…',
  '浏览器未允许复制路径，请手动复制上方容器路径。': 'The browser did not allow copying. Copy the container path above manually.',

  'OpenSurge 网关': 'OpenSurge gateway',
  '设置 OpenSurge 网关与下游设备的出口和分流规则；两者互不影响。': 'Configure egress and routing rules for the OpenSurge gateway and downstream devices independently.',
  'OpenSurge 网关本机设置': 'OpenSurge gateway-local settings',
  '尚未登记设备。使用上方“登记新设备”可从当前经过 OpenSurge 网关的设备开始。': 'No devices are registered yet. Use “Register device” above to start from clients currently passing through the OpenSurge gateway.',
  'OpenSurge 会先验证完整候选配置。验证通过后，DNS、mihomo 与 TUN 数据面会短暂重载。': 'OpenSurge validates the complete candidate first. After validation, DNS, mihomo, and the TUN data plane briefly reload.',
  '继续使用订阅或托管的网关规则；与 OpenSurge 网关本机出口选择相互独立。': 'Continue using imported or managed gateway rules independently of the gateway-local egress selection.',
  '已观察到邻居 MAC {{mac}}，等待该 IPv4 经过 OpenSurge 网关': 'Neighbor MAC {{mac}} observed; waiting for this IPv4 to pass through the OpenSurge gateway',
  '固定 IPv4 已登记，等待流量经过 OpenSurge 网关': 'Fixed IPv4 registered; waiting for traffic to pass through the OpenSurge gateway',
  '静态配置身份：等待该 IPv4 经过 OpenSurge 网关': 'Static identity: waiting for this IPv4 to pass through the OpenSurge gateway',
  '固定 IPv4 策略：等待该地址经过 OpenSurge 网关': 'Fixed-IPv4 policy: waiting for this address to pass through the OpenSurge gateway',
  '从当前经过 OpenSurge 网关的 LAN 流量发现设备，再确认静态身份与路由方式': 'Discover devices from LAN traffic currently passing through the OpenSurge gateway, then confirm static identity and routing.',
  '当前经过 OpenSurge 网关的设备': 'Devices currently passing through OpenSurge',
  '经过网关': 'Through gateway',
  '当前尚未观察到经过 OpenSurge 网关的 LAN 设备；可以直接按固定 IPv4 手工登记。': 'No LAN device has been observed through OpenSurge yet; you can register one manually by fixed IPv4.',
  '默认推荐；继续使用订阅或托管的网关规则，与网关本机出口选择相互独立。': 'Recommended. Continue using imported or managed gateway rules independently of the gateway-local egress selection.',
  '通过当前 applied 配置从 OpenSurge 网关容器访问真实服务，展示三轮中位延迟、命中规则和实际出口链。': 'Probe real services from the OpenSurge gateway container through the currently applied configuration and show three-sample median latency, matched rules, and the actual egress chain.',
  'OpenSurge 网关路径': 'OpenSurge gateway path',
  '当前浏览器线路': 'Current browser path',
  '这会验证当前 applied 配置并恢复 Mihomo。DNS、TUN 与容器网络配置不会改变；现有代理连接会重新建立。继续吗？': 'This validates the currently applied configuration and recovers Mihomo. DNS, TUN, and container network configuration are not changed; existing proxy connections will be re-established. Continue?',
  '连续异常确认后会自动执行一次 Mihomo-only 恢复，不改动 DNS、TUN 策略路由或 QNAP 宿主网络。': 'After consecutive failures are confirmed, OpenSurge performs one Mihomo-only recovery without changing DNS, TUN policy routing, or QNAP host networking.',
  '可以手动重试这条 Mihomo-only 恢复路径；不会修改 QNAP 宿主网络或持久化网络配置，旧 Mihomo 日志会先归档。': 'You can manually retry this Mihomo-only recovery path. It does not modify QNAP host networking or persisted network configuration; old Mihomo logs are archived first.',
  '这里的请求由 OpenSurge 容器内 Control Service 经 mihomo mixed-port 发起，只证明网关本机的 applied 路径。它不证明下游客户端的实际网关、DNS、设备级 SRC-IP 或应用行为；HTTP 响应表示网络可达，不等同于已登录后的完整产品功能可用。': 'These requests originate from the Control Service inside the OpenSurge container through the mihomo mixed port and prove only the gateway-local applied path. They do not prove a downstream client’s actual gateway, DNS, device-level SRC-IP routing, or application behavior. An HTTP response proves reachability, not complete signed-in product functionality.',
}

let englishCatalogPromise: Promise<void> | undefined

export function prepareLanguage(requested: RequestedLanguage): Promise<void> {
  if (resolveLanguage(requested) !== 'en') return Promise.resolve()
  englishCatalogPromise ??= import('./i18n.en').then(({ englishMessages }) => {
    Object.assign(english, englishMessages)
  })
  return englishCatalogPromise
}

let activeLanguage: ResolvedLanguage = import.meta.env.MODE === 'test' ? 'zh-Hans' : resolveSystemLanguage()

export function isRequestedLanguage(value: unknown): value is RequestedLanguage {
  return value === 'system' || value === 'zh-Hans' || value === 'en'
}

export function resolveSystemLanguage(languages: readonly string[] = typeof navigator === 'undefined' ? [] : navigator.languages): ResolvedLanguage {
  const first = languages[0]?.toLowerCase() ?? ''
  return first === 'zh' || first.startsWith('zh-') ? 'zh-Hans' : 'en'
}

export function resolveLanguage(requested: RequestedLanguage): ResolvedLanguage {
  return requested === 'system' ? resolveSystemLanguage() : requested
}

export function initialRequestedLanguage(): RequestedLanguage {
  if (import.meta.env.MODE === 'test') return 'zh-Hans'
  if (typeof window === 'undefined') return 'system'
  const cached = window.localStorage.getItem(languageCacheKey)
  return isRequestedLanguage(cached) ? cached : 'system'
}

export function activateLanguage(requested: RequestedLanguage) {
  activeLanguage = resolveLanguage(requested)
  if (typeof document !== 'undefined') document.documentElement.lang = activeLanguage
}

export function cacheRequestedLanguage(requested: RequestedLanguage) {
  if (typeof window !== 'undefined') window.localStorage.setItem(languageCacheKey, requested)
}

export function currentLanguage(): ResolvedLanguage {
  return activeLanguage
}

export function localeIdentifier(): string {
  return activeLanguage === 'zh-Hans' ? 'zh-CN' : 'en-US'
}

export function copyForTarget(source: string, target: string = productTarget): string {
  return target === 'qnap' ? qnapSourceOverrides[source] ?? source : source
}

export function t(source: string, values: Record<string, string | number> = {}): string {
  const productSource = copyForTarget(source)
  const template = activeLanguage === 'en' ? english[productSource] ?? english[source] ?? productSource : productSource
  return template.replace(/\{\{([A-Za-z0-9_]+)\}\}/g, (_, key: string) => String(values[key] ?? `{{${key}}}`))
}

export function languageDisplayName(language: ResolvedLanguage): string {
  return language === 'zh-Hans' ? '简体中文' : 'English'
}

export function hasEnglishTranslation(source: string): boolean {
  return Object.hasOwn(english, source)
}

export function registerEnglishMessages(messages: Record<string, string>) {
  Object.assign(english, messages)
}
