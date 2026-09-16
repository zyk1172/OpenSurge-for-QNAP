import { useEffect, useState } from 'react'
import { api, request, waitForOperation } from '../api'
import { PageHeader, SectionTitle } from '../components/Common'
import type { OperationNotification } from '../components/OperationNotifications'
import { RemoteManagementCard } from '../components/RemoteManagementCard'
import type { ControlConfig, NetworkDefaults, Overview } from '../types'
import { t } from '../i18n'

type HostDNSMode = 'auto' | 'opensurge' | 'host'

type QNAPHostRoutingStatus = {
  schema_version: number
  supported: boolean
  desired: boolean
  enabled: boolean
  gateway_ready: boolean
  host_ipv4?: string
  host_interface?: string
  gateway_ipv4?: string
  fallback_gateway?: string
  dns_redirect: boolean
  dns_mode: HostDNSMode
  protect_tailscale: boolean
  tailscale_detected: boolean
  tailscale_interface?: string
  tailscale_dns_protected: boolean
  tailscale_routes_protected: boolean
  error?: string
  checked_at: string
}

export function QNAPNetworkPage({
  overview,
  onChanged,
  onNavigate,
  onNotify,
}: {
  overview: Overview | null
  onChanged: () => void | Promise<void>
  onNavigate: () => void
  onNotify: (notification: OperationNotification) => void
}) {
  const [draft, setDraft] = useState<ControlConfig | null>(null)
  const [actual, setActual] = useState<NetworkDefaults | null>(null)
  const [hostRouting, setHostRouting] = useState<QNAPHostRoutingStatus | null>(null)
  const [dnsMode, setDNSMode] = useState<HostDNSMode>('auto')
  const [protectTailscale, setProtectTailscale] = useState(true)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [lifecycleBusy, setLifecycleBusy] = useState(false)
  const [hostRoutingBusy, setHostRoutingBusy] = useState(false)
  const [hostPolicyDirty, setHostPolicyDirty] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  const running = overview?.status.gateway === 'running' || overview?.status.gateway === 'degraded'
  const interrupted = overview?.status.runtime_state === 'interrupted'
  const stopped = overview?.status.gateway === 'stopped'

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const [config, network, host] = await Promise.all([
        api.config(),
        api.networkDefaults('same_lan').catch(() => null),
        request<QNAPHostRoutingStatus>('/api/v1/qnap-host-routing').catch(() => null),
      ])
      setDraft(config)
      setActual(network)
      setHostRouting(host)
      if (host) {
        setDNSMode(host.dns_mode || 'auto')
        setProtectTailscale(host.protect_tailscale !== false)
        setHostPolicyDirty(false)
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [overview?.revision])

  const patch = (next: Partial<ControlConfig>) => {
    setDraft(current => current ? { ...current, ...next } : current)
  }

  const runLifecycle = async () => {
    if (lifecycleBusy || saving || (!running && !stopped && !interrupted)) return
    setLifecycleBusy(true)
    setError('')
    setMessage('')
    const action = interrupted || running ? 'stop' : 'start'
    try {
      const operation = await api.gateway(action)
      await waitForOperation(operation.id)
      const success = interrupted ? '旧运行状态已安全清理。' : running ? '网关已停止。' : '网关已启动。'
      setMessage(t(success))
      onNotify({ tone: 'success', title: t(interrupted ? 'QNAP 恢复完成' : action === 'start' ? 'QNAP 网关已启动' : 'QNAP 网关已停止'), message: t(success) })
      await Promise.all([load(), Promise.resolve(onChanged())])
    } catch (cause) {
      const failure = cause instanceof Error ? cause.message : String(cause)
      setError(failure)
      onNotify({ tone: 'error', title: t('QNAP 网关操作失败'), message: failure })
    } finally {
      setLifecycleBusy(false)
    }
  }

  const applyHostRouting = async (enabled: boolean) => {
    const status = await request<QNAPHostRoutingStatus>('/api/v1/qnap-host-routing', {
      method: 'PUT',
      body: JSON.stringify({ enabled, dns_mode: dnsMode, protect_tailscale: protectTailscale }),
    })
    setHostRouting(status)
    setDNSMode(status.dns_mode || dnsMode)
    setProtectTailscale(status.protect_tailscale)
    setHostPolicyDirty(false)
    return status
  }

  const toggleHostRouting = async () => {
    if (!hostRouting || hostRoutingBusy) return
    const nextEnabled = !hostRouting.desired
    if (nextEnabled && !window.confirm(t('启用后，NAS 本机发起的 IPv4 公网流量将优先经过 OpenSurge；DNS 与 Tailscale 的处理方式按下方接管策略执行。QTS 主路由配置不会被修改。继续吗？'))) return
    setHostRoutingBusy(true)
    setError('')
    setMessage('')
    try {
      await applyHostRouting(nextEnabled)
      const success = nextEnabled ? 'NAS 主机 IPv4 已通过 OpenSurge 接管。' : 'NAS 主机已恢复使用 QTS 原有路由。'
      setMessage(t(success))
      onNotify({ tone: 'success', title: t('NAS 主机上网'), message: t(success) })
    } catch (cause) {
      const failure = cause instanceof Error ? cause.message : String(cause)
      setError(failure)
      onNotify({ tone: 'error', title: t('NAS 主机路由切换失败'), message: failure })
      await load()
    } finally {
      setHostRoutingBusy(false)
    }
  }

  const saveHostPolicy = async () => {
    if (!hostRouting || hostRoutingBusy || !hostPolicyDirty) return
    setHostRoutingBusy(true)
    setError('')
    setMessage('')
    try {
      await applyHostRouting(hostRouting.desired)
      const success = hostRouting.desired ? 'NAS 接管策略已保存并重新校验。' : 'NAS 接管策略已保存，下次启用时生效。'
      setMessage(t(success))
      onNotify({ tone: 'success', title: t('NAS 接管策略已保存'), message: t(success) })
    } catch (cause) {
      const failure = cause instanceof Error ? cause.message : String(cause)
      setError(failure)
      onNotify({ tone: 'error', title: t('NAS 接管策略保存失败'), message: failure })
      await load()
    } finally {
      setHostRoutingBusy(false)
    }
  }

  const saveRuntime = async () => {
    if (!draft || saving || lifecycleBusy) return
    setSaving(true)
    setError('')
    setMessage('')
    let stoppedForSave = false
    try {
      if (running) {
        const stop = await api.gateway('stop')
        await waitForOperation(stop.id)
        stoppedForSave = true
      }

      const ipv4OnlyDraft: ControlConfig = {
        ...draft,
        dns: { ...draft.dns, ipv6: false },
        transparent: {
          ...draft.transparent,
          tun_ipv6: 'off',
          ipv6_shared_l2_ready: false,
        },
      }
      const saved = await api.saveConfig(ipv4OnlyDraft)
      const reread = await api.config()
      if (saved.revision !== reread.revision) throw new Error(t('配置保存后的版本校验失败，请重新读取后再试。'))
      setDraft(reread)

      if (stoppedForSave) {
        const start = await api.gateway('start')
        await waitForOperation(start.id)
        onNotify({ tone: 'success', title: t('QNAP 运行参数已保存'), message: t('网关已使用保存后的配置重新启动。') })
      }
      setMessage(t(stoppedForSave ? '配置已持久化并重新启动网关。' : '配置已持久化到 /data；下次启动仍会保留。'))
      await Promise.all([load(), Promise.resolve(onChanged())])
    } catch (cause) {
      const failure = cause instanceof Error ? cause.message : String(cause)
      setError(failure)
      onNotify({ tone: 'error', title: t('保存 QNAP 运行参数失败'), message: failure })
    } finally {
      setSaving(false)
    }
  }

  const networkInterface = actual?.snapshot.interface || draft?.gateway.interface || overview?.status.interface || 'eth0'
  const networkIPv4 = actual?.snapshot.ipv4 || draft?.gateway.lan_ip || overview?.status.lan_ip || '—'
  const networkCIDR = actual?.snapshot.ipv4
    ? `${actual.snapshot.ipv4}/${draft?.gateway.lan_prefix_len ?? 24}`
    : draft ? `${draft.gateway.lan_ip}/${draft.gateway.lan_prefix_len}` : '—'
  const router = actual?.snapshot.router || '由 Docker/QNET 创建参数确定'

  return <>
    <PageHeader
      eyebrow="QNAP NETWORK"
      title="QNAP 网关网络"
      description="物理网卡、QNET、静态 IP、CIDR 与主路由在创建容器时确定；宿主机接管使用独立策略路由，不修改 QTS 保存的默认网关。"
      action={<button id="gateway-control" className={running ? 'danger' : 'primary'} type="button" disabled={lifecycleBusy || saving || (!running && !stopped && !interrupted)} onClick={() => void runLifecycle()}>{t(lifecycleBusy ? '正在执行…' : interrupted ? '安全清理旧状态' : running ? '停止网关' : '启动网关')}</button>}
    />

    {interrupted && <div className="notice warn" role="status"><strong>{t('检测到上一次容器或 NAS 重启留下的运行状态。')}</strong><p>{t('先执行安全清理；QNAP 版不会触碰宿主 QTS 保存的默认网关。')}</p></div>}
    {error && <div className="notice warn" role="alert"><strong>{t('操作未完成')}</strong><p>{error}</p></div>}
    {message && <div className="ok-notice" role="status"><strong>{t('操作完成')}</strong><p>{message}</p></div>}

    <section className="section">
      <SectionTitle title="容器创建时网络" subtitle="只读 · 修改以下任一项需要重建 OpenSurge 容器" />
      {loading ? <div className="empty">{t('正在读取容器实际网络…')}</div> : <div className="inventory">
        <span><strong>{t('容器接口')}</strong><br />{networkInterface}</span>
        <span><strong>{t('OpenSurge IPv4')}</strong><br />{networkIPv4}</span>
        <span><strong>{t('LAN')}</strong><br />{networkCIDR}</span>
        <span><strong>{t('主路由')}</strong><br />{router}</span>
      </div>}
      <div className="notice"><strong>{t('QNET 父网卡不在容器内二次选择')}</strong><p>{t('QNAP 物理网卡 / Virtual Switch 由 Docker Compose 的 qnet iface 在创建容器时绑定。容器内部通常只看到 eth0，这是正常现象。')}</p></div>
    </section>

    <section className="section">
      <SectionTitle title="NAS 主机上网" subtitle="接管 QNAP 宿主机 IPv4，同时显式保留 Tailscale 等宿主网络服务的优先级" />
      {hostRouting ? <>
        <div className="inventory">
          <span><strong>{t('NAS 主机')}</strong><br />{hostRouting.host_ipv4 || '—'}{hostRouting.host_interface ? ` · ${hostRouting.host_interface}` : ''}</span>
          <span><strong>{t('OpenSurge 下一跳')}</strong><br />{hostRouting.gateway_ipv4 || networkIPv4}</span>
          <span><strong>{t('QTS 回退网关')}</strong><br />{hostRouting.fallback_gateway || router}</span>
          <span><strong>{t('IPv4 DNS')}</strong><br />{dnsMode === 'host' ? t('保留宿主 DNS') : hostRouting.dns_redirect ? t('已透明接管') : t('未接管')}</span>
        </div>

        <div className="source-import-grid">
          <article className="source-import-card">
            <label>
              <span>{t('NAS DNS 接管模式')}</span>
              <select value={dnsMode} disabled={hostRoutingBusy} onChange={event => { setDNSMode(event.target.value as HostDNSMode); setHostPolicyDirty(true) }}>
                <option value="auto">{t('自动兼容（推荐）')}</option>
                <option value="opensurge">{t('全部 DNS 交给 OpenSurge')}</option>
                <option value="host">{t('保留宿主 DNS')}</option>
              </select>
            </label>
            <p className="muted">{t(dnsMode === 'auto'
              ? '普通 NAS DNS 进入 OpenSurge；较早的宿主 VPN 规则保持优先，因此 Tailscale MagicDNS 不会被抢走。'
              : dnsMode === 'opensurge'
                ? '安装 NAS DNS 透明接管规则；是否仍保留 Tailscale 优先级由右侧共存开关决定。'
                : '不安装专用 TCP/UDP 53 接管规则，适合完全保留 QTS 或其他宿主 DNS 管理。')}</p>
          </article>
          <article className="source-import-card">
            <label className="sidebar-switch">
              <input type="checkbox" checked={protectTailscale} disabled={hostRoutingBusy} onChange={event => { setProtectTailscale(event.target.checked); setHostPolicyDirty(true) }} />
              <span><strong>{t('Tailscale 共存保护')}</strong><small>{t('让宿主机已有的较早 VPN / from-all 策略先于 OpenSurge；默认开启。')}</small></span>
            </label>
            <p className="muted">{t('关闭后 OpenSurge 可以抢在这类宿主规则之前，仅用于明确希望覆盖宿主 VPN 路由的场景。')}</p>
          </article>
        </div>

        <div className={hostRouting.tailscale_detected ? 'ok-notice' : 'notice'}>
          <strong>{t(hostRouting.tailscale_detected ? '检测到 NAS 本机 Tailscale' : '未检测到 NAS 本机 Tailscale')}</strong>
          {hostRouting.tailscale_detected ? <div className="inventory">
            <span><strong>{t('宿主接口')}</strong><br />{hostRouting.tailscale_interface || 'tailscale0'}</span>
            <span><strong>Quad100 / MagicDNS</strong><br />{hostRouting.tailscale_dns_protected ? t('已保留给 Tailscale') : t('需要检查')}</span>
            <span><strong>{t('Tailnet 路由')}</strong><br />{hostRouting.tailscale_routes_protected ? t('已保留给 Tailscale') : t('需要检查')}</span>
          </div> : <p>{t('当前没有 tailscale0；共存保护仍会持久化，之后安装或启动 Tailscale 时无需重新配置。')}</p>}
        </div>

        <div className={hostRouting.enabled ? 'ok-notice' : hostRouting.desired ? 'notice warn' : 'notice'}>
          <strong>{t(hostRouting.enabled ? 'NAS 主机接管已启用' : hostRouting.desired ? 'NAS 主机接管正在等待网关就绪' : 'NAS 主机接管未启用')}</strong>
          <p>{t(hostRouting.enabled
            ? 'NAS 本机产生的 IPv4 公网流量通过策略路由进入 OpenSurge；局域网、Docker 本地路由和 QTS 主路由配置保持原样。'
            : hostRouting.desired
              ? 'OpenSurge 数据面恢复为 ready 后会自动重新接管；未就绪期间 NAS 使用 QTS 原有主路由。'
              : '启用后只增加 OpenSurge 自己的宿主策略路由；宿主 VPN 的优先级是否保留由上方共存策略决定。')}</p>
          {hostRouting.error && <p className="muted">{hostRouting.error}</p>}
        </div>

        <div className="source-actions">
          <button className="primary" type="button" disabled={hostRoutingBusy || !hostPolicyDirty} onClick={() => void saveHostPolicy()}>{t(hostRoutingBusy ? '正在保存…' : '保存接管策略')}</button>
          <button type="button" className={hostRouting.desired ? 'danger' : 'primary'} disabled={hostRoutingBusy || (!hostRouting.desired && (!hostRouting.supported || !running || !hostRouting.gateway_ready))} onClick={() => void toggleHostRouting()}>{t(hostRoutingBusy ? '正在切换…' : hostRouting.desired ? '停止 NAS 接管' : '让 NAS 使用 OpenSurge')}</button>
          <button type="button" disabled={hostRoutingBusy} onClick={() => void load()}>{t('刷新状态')}</button>
        </div>
        <p className="muted">{t('此功能仅接管 NAS 宿主机本身产生的 IPv4 流量；IPv6 仍不由 OpenSurge 管理。容器正常停止或重建时会先释放宿主策略，避免 NAS 被留在失效的下一跳上。')}</p>
      </> : <div className="empty">{t('正在读取 NAS 主机路由能力…')}</div>}
    </section>

    {draft && <section className="section">
      <SectionTitle title="运行参数" subtitle="常用设置直接在界面中修改；这些值持久化到 /data，网关运行时保存会自动停止并重新启动。" />
      <div className="source-import-grid">
        <article className="source-import-card">
          <label><span>{t('DNS 上游')}</span><input value={draft.dns.upstream} onChange={event => patch({ dns: { ...draft.dns, upstream: event.target.value } })} placeholder="127.0.0.1#1053" /></label>
          <p className="muted">{t('这里是 OpenSurge 内部 dnsmasq 到 Mihomo DNS 的上游，不是主路由要填写的 DNS 地址。')}</p>
        </article>
        <article className="source-import-card">
          <label className="sidebar-switch"><input type="checkbox" checked={draft.mihomo.store_fake_ip} onChange={event => patch({ mihomo: { ...draft.mihomo, store_fake_ip: event.target.checked } })} /><span><strong>Store fake-ip</strong><small>{t('持久化 mihomo fake-ip 映射。')}</small></span></label>
          <label className="sidebar-switch"><input type="checkbox" checked={draft.transparent.strict_route} onChange={event => patch({ transparent: { ...draft.transparent, strict_route: event.target.checked } })} /><span><strong>TUN strict-route</strong><small>{t('仅影响容器内透明代理数据面。')}</small></span></label>
        </article>
      </div>
      <div className="notice warn"><strong>IPv6：OFF / 不支持</strong><p>OpenSurge for QNAP 当前仅支持 IPv4 数据面。容器不会接管、分配、代理或管理 IPv6；IPv6 继续完全由光猫、主路由和运营商处理。旧配置中的 IPv6 开关会在加载时自动迁移为 OFF。</p></div>
      <div className="source-actions"><button type="button" onClick={() => void load()} disabled={saving || lifecycleBusy}>{t('重新读取')}</button><button className="primary" type="button" onClick={() => void saveRuntime()} disabled={saving || lifecycleBusy}>{saving ? t('正在保存…') : t(running ? '保存并重启网关' : '保存运行参数')}</button></div>
    </section>}

    <section className="section">
      <SectionTitle title="高级代理配置" subtitle="不再直接编辑 opensurge.yaml；代理规则和 Mihomo 高级项使用持久化 Profile Overlay。" />
      <div className="notice">
        <strong>{t('OpenSurge 控制面配置不再作为通用 YAML 编辑器')}</strong>
        <p>{t('订阅、规则、Provider、策略组、DNS 过滤和其他 Mihomo 高级修改请进入“代理与规则源”中的 Profile Overlay。那里可以使用引导式设置或专家 YAML，并可预览最终生成的 Mihomo 配置。')}</p>
      </div>
      <div className="source-actions"><button className="primary" type="button" onClick={onNavigate}>{t('打开代理与规则源')}</button></div>
    </section>

    <RemoteManagementCard />

    <section className="section" id="gateway-control-bottom">
      <SectionTitle title="如何修改物理网卡或静态 IP" subtitle="这是容器部署操作，不是 OpenSurge 运行配置" />
      <p>{t('停止并重建容器时修改 Compose 中的 QNET 父接口、IPv4、CIDR 或主路由。保留同一个 /data 持久化目录即可保留管理员、订阅、规则、设备策略和运行配置。')}</p>
    </section>
  </>
}
