import { useEffect, useState } from 'react'
import { api, request, waitForOperation } from '../api'
import { PageHeader, SectionTitle } from '../components/Common'
import type { OperationNotification } from '../components/OperationNotifications'
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

type TrafficSelector = {
  id: string
  label?: string
  enabled: boolean
  type: 'source_ingress'
  source_ipv4: string
  ingress_interface: string
  main_priority?: number
  proxy_priority?: number
  active?: boolean
  error?: string
}

type TrafficScopeStatus = {
  schema_version: number
  supported: boolean
  gateway_ready: boolean
  gateway_ipv4?: string
  gateway_interface?: string
  selectors: TrafficSelector[]
  error?: string
  checked_at: string
}

type TrafficDiscoveryCandidate = {
  selector_id: string
  source_ipv4: string
  ingress_interface: string
  mac?: string
  neighbor_state?: string
  bridge_cidr: string
  bridge_ipv4: string
}

type TrafficDiscovery = {
  schema_version: number
  gateway_interface?: string
  candidates: TrafficDiscoveryCandidate[]
  checked_at: string
}

export function QNAPNetworkPage({
  overview,
  onChanged,
  onNotify,
}: {
  overview: Overview | null
  onChanged: () => void | Promise<void>
  onNotify: (notification: OperationNotification) => void
  onNavigate?: () => void
}) {
  const [draft, setDraft] = useState<ControlConfig | null>(null)
  const [actual, setActual] = useState<NetworkDefaults | null>(null)
  const [hostRouting, setHostRouting] = useState<QNAPHostRoutingStatus | null>(null)
  const [trafficScopes, setTrafficScopes] = useState<TrafficScopeStatus | null>(null)
  const [trafficDiscovery, setTrafficDiscovery] = useState<TrafficDiscovery | null>(null)
  const [trafficDraft, setTrafficDraft] = useState<TrafficSelector[]>([])
  const [trafficBusy, setTrafficBusy] = useState(false)
  const [dnsMode, setDNSMode] = useState<HostDNSMode>('auto')
  const [protectTailscale, setProtectTailscale] = useState(true)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [hostRoutingBusy, setHostRoutingBusy] = useState(false)
  const [hostPolicyDirty, setHostPolicyDirty] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  const running = overview?.status.gateway === 'running' || overview?.status.gateway === 'degraded'
  const interrupted = overview?.status.runtime_state === 'interrupted'

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const [config, network, host, traffic, discovery] = await Promise.all([
        api.config(),
        api.networkDefaults('same_lan').catch(() => null),
        request<QNAPHostRoutingStatus>('/api/v1/qnap-host-routing').catch(() => null),
        request<TrafficScopeStatus>('/api/v1/qnap-traffic-scopes').catch(() => null),
        request<TrafficDiscovery>('/api/v1/qnap-traffic-scopes/discovery').catch(() => null),
      ])
      setDraft(config)
      setActual(network)
      setHostRouting(host)
      setTrafficScopes(traffic)
      setTrafficDiscovery(discovery)
      setTrafficDraft(traffic?.selectors ?? [])
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
    if (nextEnabled && !window.confirm(t('启用 NAS 主机接管？公网 IPv4 将优先经过 OpenSurge，QTS 默认网关保持不变。'))) return
    setHostRoutingBusy(true)
    setError('')
    setMessage('')
    try {
      await applyHostRouting(nextEnabled)
      const success = nextEnabled ? 'NAS 主机 IPv4 已通过 OpenSurge 接管。' : 'NAS 主机已恢复使用 QTS 原有路由。'
      setMessage(t(success))
      onNotify({ tone: 'success', title: t('NAS 主机接管'), message: t(success) })
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

  const addTrafficSelector = () => {
    setTrafficDraft(current => [...current, {
      id: `container-${Date.now().toString(36)}`,
      label: '',
      enabled: false,
      type: 'source_ingress',
      source_ipv4: '',
      ingress_interface: 'lxcbr0',
    }])
  }

  const patchTrafficSelector = (id: string, next: Partial<TrafficSelector>) => {
    setTrafficDraft(current => current.map(selector => selector.id === id ? { ...selector, ...next } : selector))
  }

  const toggleDiscoveredCandidate = (candidate: TrafficDiscoveryCandidate, enabled: boolean) => {
    setTrafficDraft(current => {
      const index = current.findIndex(selector =>
        selector.source_ipv4 === candidate.source_ipv4 &&
        selector.ingress_interface === candidate.ingress_interface)
      if (index >= 0) {
        return current.map((selector, selectorIndex) => selectorIndex === index ? { ...selector, enabled } : selector)
      }
      if (!enabled) return current
      return [...current, {
        id: candidate.selector_id,
        label: '',
        enabled: true,
        type: 'source_ingress',
        source_ipv4: candidate.source_ipv4,
        ingress_interface: candidate.ingress_interface,
      }]
    })
  }

  const removeTrafficSelector = (id: string) => {
    setTrafficDraft(current => current.filter(selector => selector.id !== id))
  }

  const saveTrafficScopes = async () => {
    if (trafficBusy) return
    setTrafficBusy(true)
    setError('')
    setMessage('')
    try {
      const selectors = trafficDraft.map(({ id, label, enabled, type, source_ipv4, ingress_interface }) => ({
        id, label, enabled, type, source_ipv4, ingress_interface,
      }))
      const status = await request<TrafficScopeStatus>('/api/v1/qnap-traffic-scopes', {
        method: 'PUT',
        body: JSON.stringify({ selectors }),
      })
      setTrafficScopes(status)
      setTrafficDraft(status.selectors)
      const active = status.selectors.filter(selector => selector.enabled && selector.active).length
      const pending = status.selectors.filter(selector => selector.enabled && !selector.active).length
      const success = pending > 0
        ? `容器接管策略已保存；${pending} 项等待网关就绪或需要检查。`
        : active > 0 ? `容器接管策略已保存，${active} 项已生效。` : '容器接管策略已保存。'
      setMessage(t(success))
      onNotify({ tone: pending > 0 ? 'warning' : 'success', title: t('容器流量接管'), message: t(success) })
    } catch (cause) {
      const failure = cause instanceof Error ? cause.message : String(cause)
      setError(failure)
      onNotify({ tone: 'error', title: t('容器接管策略保存失败'), message: failure })
      await load()
    } finally {
      setTrafficBusy(false)
    }
  }

  const saveRuntime = async () => {
    if (!draft || saving) return
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
        onNotify({ tone: 'success', title: t('运行参数已保存'), message: t('网关已使用新配置重新启动。') })
      }
      setMessage(t(stoppedForSave ? '配置已保存并重启网关。' : '配置已保存。'))
      await Promise.all([load(), Promise.resolve(onChanged())])
    } catch (cause) {
      const failure = cause instanceof Error ? cause.message : String(cause)
      setError(failure)
      onNotify({ tone: 'error', title: t('保存运行参数失败'), message: failure })
    } finally {
      setSaving(false)
    }
  }

  const networkInterface = actual?.snapshot.interface || draft?.gateway.interface || overview?.status.interface || 'eth0'
  const networkIPv4 = actual?.snapshot.ipv4 || draft?.gateway.lan_ip || overview?.status.lan_ip || '—'
  const networkCIDR = actual?.snapshot.ipv4
    ? `${actual.snapshot.ipv4}/${draft?.gateway.lan_prefix_len ?? 24}`
    : draft ? `${draft.gateway.lan_ip}/${draft.gateway.lan_prefix_len}` : '—'
  const router = actual?.snapshot.router || t('Docker/QNET 配置')
  const lanProxy = draft?.lan_proxy ?? { enabled: false, socks_port: 7891 }

  return <>
    <PageHeader eyebrow="QNAP NETWORK" title={t('网络设置')} description={t('查看容器网络，并管理 NAS 主机 IPv4 接管与网关运行参数。')} />

    {interrupted && <div className="notice warn" role="status"><strong>{t('网关状态待恢复')}</strong><p>{t('请在“总览”执行安全清理。')}</p></div>}
    {error && <div className="notice warn" role="alert"><strong>{t('操作未完成')}</strong><p>{error}</p></div>}
    {message && <div className="ok-notice" role="status"><strong>{t('操作完成')}</strong><p>{message}</p></div>}

    <section className="section qnap-network-section">
      <SectionTitle title="容器网络" subtitle="创建容器时确定 · 只读" />
      {loading ? <div className="empty">{t('正在读取网络配置…')}</div> : <div className="inventory">
        <span><strong>{t('容器接口')}</strong><br />{networkInterface}</span>
        <span><strong>{t('OpenSurge IPv4')}</strong><br />{networkIPv4}</span>
        <span><strong>{t('LAN')}</strong><br />{networkCIDR}</span>
        <span><strong>{t('主路由')}</strong><br />{router}</span>
      </div>}
      <div className="notice"><strong>{t('QNET 由容器部署配置决定')}</strong><p>{t('父网卡与静态网络在创建容器时绑定；容器内通常只显示 eth0。')}</p></div>
    </section>

    <section className="section qnap-host-takeover-section">
      <SectionTitle title="NAS 主机接管" subtitle="让 NAS 宿主机 IPv4 通过 OpenSurge" />
      {hostRouting ? <>
        <div className="inventory">
          <span><strong>{t('NAS 主机')}</strong><br />{hostRouting.host_ipv4 || '—'}{hostRouting.host_interface ? ` · ${hostRouting.host_interface}` : ''}</span>
          <span><strong>{t('OpenSurge 下一跳')}</strong><br />{hostRouting.gateway_ipv4 || networkIPv4}</span>
          <span><strong>{t('QTS 回退网关')}</strong><br />{hostRouting.fallback_gateway || router}</span>
          <span><strong>{t('IPv4 DNS')}</strong><br />{dnsMode === 'host' ? t('保留宿主 DNS') : hostRouting.dns_redirect ? t('已透明接管') : t('未接管')}</span>
        </div>

        <div className="source-import-grid qnap-host-policy-grid">
          <article className="source-import-card">
            <label>
              <span>{t('NAS DNS 接管模式')}</span>
              <select value={dnsMode} disabled={hostRoutingBusy} onChange={event => { setDNSMode(event.target.value as HostDNSMode); setHostPolicyDirty(true) }}>
                <option value="auto">{t('自动（推荐）')}</option>
                <option value="opensurge">{t('经 OpenSurge')}</option>
                <option value="host">{t('保留宿主 DNS')}</option>
              </select>
            </label>
            <p className="muted">{t(dnsMode === 'auto'
              ? '普通 DNS 经 OpenSurge；保留宿主 VPN 的更高优先级。'
              : dnsMode === 'opensurge'
                ? 'NAS DNS 经 OpenSurge；Tailscale 优先级由共存保护决定。'
                : '不接管 NAS DNS。')}</p>
          </article>
          <article className="source-import-card">
            <label className="sidebar-switch">
              <input type="checkbox" checked={protectTailscale} disabled={hostRoutingBusy} onChange={event => { setProtectTailscale(event.target.checked); setHostPolicyDirty(true) }} />
              <span><strong>{t('Tailscale 共存保护')}</strong><small>{t('保留宿主 VPN 与 MagicDNS 的优先级。')}</small></span>
            </label>
            <p className="muted">{t('仅在需要覆盖宿主 VPN 路由时关闭。')}</p>
          </article>
        </div>

        <div className="qnap-host-status-panel" aria-label={t('NAS 主机接管与 Tailscale 状态')}>
          <div className="qnap-host-status-main">
            <small>{t('NAS 主机接管')}</small>
            <strong>{t(hostRouting.enabled ? '启用' : '未启用')}</strong>
            <small>{t(hostRouting.enabled ? '公网 IPv4 经 OpenSurge' : hostRouting.desired ? '等待网关就绪' : 'QTS 路由保持不变')}</small>
            {hostRouting.error && <small>{hostRouting.error}</small>}
          </div>
          <div><small>{t('Tailscale')}</small><strong>{t(hostRouting.tailscale_detected ? '已检测' : '未检测')}</strong><small>{hostRouting.tailscale_interface || t('共存保护待命')}</small></div>
          <div><small>MagicDNS</small><strong>{t(hostRouting.tailscale_detected ? hostRouting.tailscale_dns_protected ? '已保护' : '需要检查' : '等待 Tailscale')}</strong><small>{t('保持宿主 VPN 优先级')}</small></div>
          <div><small>{t('Tailnet 路由')}</small><strong>{t(hostRouting.tailscale_detected ? hostRouting.tailscale_routes_protected ? '已保护' : '需要检查' : '等待 Tailscale')}</strong><small>{t('避免被公网接管覆盖')}</small></div>
        </div>

        <div className="source-actions">
          <button className="primary" type="button" disabled={hostRoutingBusy || !hostPolicyDirty} onClick={() => void saveHostPolicy()}>{t(hostRoutingBusy ? '正在保存…' : '保存接管策略')}</button>
          <button type="button" className={hostRouting.desired ? 'danger' : 'primary'} disabled={hostRoutingBusy || (!hostRouting.desired && (!hostRouting.supported || !running || !hostRouting.gateway_ready))} onClick={() => void toggleHostRouting()}>{t(hostRoutingBusy ? '正在切换…' : hostRouting.desired ? '停止 NAS 接管' : '让 NAS 使用 OpenSurge')}</button>
          <button type="button" disabled={hostRoutingBusy} onClick={() => void load()}>{t('刷新状态')}</button>
        </div>
      </> : <div className="empty">{t('正在读取 NAS 主机路由能力…')}</div>}
    </section>

    <section className="section qnap-traffic-scope-section">
      <SectionTitle title="容器流量接管" subtitle="自动发现 NAT / 私有桥接容器" />
      <div className="notice">
        <strong>{t('QNET 桥接与私有 bridge 分开处理')}</strong>
        <p>{t('QNAP 的 QNET / Virtual Switch 桥接容器直接处于 LAN 二层网络，不等同于 lxcbr0、docker0、br-* 这类 NAT / 私有 bridge。本功能只自动发现后者，避免把 QNET 流量误判为宿主三层转发。')}</p>
      </div>
      {trafficScopes?.error && <div className="notice warn"><strong>{t('当前能力不可用')}</strong><p>{trafficScopes.error}</p></div>}

      <div className="source-import-grid qnap-runtime-grid">
        {(trafficDiscovery?.candidates ?? []).map(candidate => {
          const selector = trafficDraft.find(item => item.source_ipv4 === candidate.source_ipv4 && item.ingress_interface === candidate.ingress_interface)
          const observed = trafficScopes?.selectors.find(item => item.source_ipv4 === candidate.source_ipv4 && item.ingress_interface === candidate.ingress_interface)
          const state = !selector?.enabled ? t('未选择') : observed?.active ? t('已接管') : t('等待生效')
          return <article className="source-import-card" key={candidate.selector_id}>
            <span><strong>{candidate.source_ipv4}</strong><small>{candidate.ingress_interface} · {candidate.bridge_cidr}</small></span>
            <p className="muted">{candidate.mac ? `${candidate.mac} · ${candidate.neighbor_state || 'NEIGH'}` : candidate.neighbor_state || t('已观察到流量')}</p>
            <label className="sidebar-switch">
              <input type="checkbox" aria-label={t('通过 OpenSurge：{{ip}}', { ip: candidate.source_ipv4 })} checked={selector?.enabled === true} disabled={trafficBusy} onChange={event => toggleDiscoveredCandidate(candidate, event.target.checked)} />
              <span><strong>{t('通过 OpenSurge')}</strong><small>{state}</small></span>
            </label>
          </article>
        })}
      </div>

      {(trafficDiscovery?.candidates.length ?? 0) === 0 && <div className="empty">{t('暂未观察到 NAT / 私有桥接容器。启动目标容器并产生一次网络流量后刷新即可自动出现。')}</div>}

      {trafficDraft.some(selector => !(trafficDiscovery?.candidates ?? []).some(candidate => candidate.source_ipv4 === selector.source_ipv4 && candidate.ingress_interface === selector.ingress_interface)) && <>
        <p className="muted">{t('以下是已保存但本次邻居表中未观察到的策略，可继续保留或删除。')}</p>
        <div className="source-import-grid qnap-runtime-grid">
          {trafficDraft.filter(selector => !(trafficDiscovery?.candidates ?? []).some(candidate => candidate.source_ipv4 === selector.source_ipv4 && candidate.ingress_interface === selector.ingress_interface)).map(selector => {
            const observed = trafficScopes?.selectors.find(item => item.id === selector.id)
            return <article className="source-import-card" key={selector.id}>
              <label>
                <span>{t('名称（可选）')}</span>
                <input aria-label={t('名称（可选）')} value={selector.label ?? ''} disabled={trafficBusy} onChange={event => patchTrafficSelector(selector.id, { label: event.target.value })} />
              </label>
              <label>
                <span>{t('容器 IPv4')}</span>
                <input aria-label={t('容器 IPv4')} value={selector.source_ipv4} disabled={trafficBusy} onChange={event => patchTrafficSelector(selector.id, { source_ipv4: event.target.value })} placeholder="10.0.3.6" inputMode="decimal" />
              </label>
              <label>
                <span>{t('私有 bridge 接口')}</span>
                <input aria-label={t('私有 bridge 接口')} value={selector.ingress_interface} disabled={trafficBusy} onChange={event => patchTrafficSelector(selector.id, { ingress_interface: event.target.value })} placeholder="lxcbr0" />
              </label>
              <label className="sidebar-switch">
                <input type="checkbox" checked={selector.enabled} disabled={trafficBusy} onChange={event => patchTrafficSelector(selector.id, { enabled: event.target.checked })} />
                <span><strong>{t('通过 OpenSurge')}</strong><small>{observed?.active ? t('已接管') : selector.enabled ? t('等待生效') : t('停用')}</small></span>
              </label>
              <div className="source-actions"><button type="button" className="danger" disabled={trafficBusy} onClick={() => removeTrafficSelector(selector.id)}>{t('删除')}</button></div>
            </article>
          })}
        </div>
      </>}

      <details>
        <summary>{t('手动添加（仅作为自动发现失败时的后备）')}</summary>
        <div className="source-actions"><button type="button" disabled={trafficBusy} onClick={addTrafficSelector}>{t('手动添加私有 bridge 地址')}</button></div>
      </details>
      <div className="source-actions">
        <button className="primary" type="button" disabled={trafficBusy || !trafficScopes?.supported} onClick={() => void saveTrafficScopes()}>{trafficBusy ? t('正在保存…') : t('保存容器接管')}</button>
        <button type="button" disabled={trafficBusy} onClick={() => void load()}>{t('重新扫描')}</button>
      </div>
      <p className="muted">{t('自动发现依据宿主路由与邻居表，不需要 Docker Socket，也不会获得容器管理权限；规则故障时采用 fail-open，容器恢复 QTS 原有默认路由。')}</p>
    </section>

    {draft && <section className="section qnap-runtime-section">
      <SectionTitle title="运行参数" subtitle="保存到 /data；运行中保存会重启网关" />
      <div className="source-import-grid qnap-runtime-grid">
        <article className="source-import-card qnap-runtime-card">
          <span><strong>Store fake-ip</strong><small>{t('持久化 fake-ip 映射')}</small></span>
          <button className={`overlay-switch ${draft.mihomo.store_fake_ip ? 'on' : ''}`} type="button" role="switch" aria-label="Store fake-ip" aria-checked={draft.mihomo.store_fake_ip} onClick={() => patch({ mihomo: { ...draft.mihomo, store_fake_ip: !draft.mihomo.store_fake_ip } })}><i aria-hidden="true" /><span>{t(draft.mihomo.store_fake_ip ? '已启用' : '已停用')}</span></button>
        </article>
        <article className="source-import-card qnap-runtime-card">
          <span><strong>TUN strict-route</strong><small>{t('严格接管 TUN 路由')}</small></span>
          <button className={`overlay-switch ${draft.transparent.strict_route ? 'on' : ''}`} type="button" role="switch" aria-label="TUN strict-route" aria-checked={draft.transparent.strict_route} onClick={() => patch({ transparent: { ...draft.transparent, strict_route: !draft.transparent.strict_route } })}><i aria-hidden="true" /><span>{t(draft.transparent.strict_route ? '已启用' : '已停用')}</span></button>
        </article>
        <article className="source-import-card qnap-runtime-card">
          <span><strong>LAN SOCKS5 / SOCKS5H</strong><small>{t('仅开放独立 SOCKS listener，不暴露内部 mixed-port')}</small></span>
          <button className={`overlay-switch ${lanProxy.enabled ? 'on' : ''}`} type="button" role="switch" aria-label="LAN SOCKS5 / SOCKS5H" aria-checked={lanProxy.enabled} onClick={() => patch({ lan_proxy: { ...lanProxy, enabled: !lanProxy.enabled } })}><i aria-hidden="true" /><span>{t(lanProxy.enabled ? '已启用' : '已停用')}</span></button>
          <label>
            <span>{t('LAN SOCKS 端口')}</span>
            <input aria-label={t('LAN SOCKS 端口')} type="number" min={1} max={65535} value={lanProxy.socks_port} onChange={event => patch({ lan_proxy: { ...lanProxy, socks_port: Number(event.target.value) } })} />
          </label>
          <p className="muted">{lanProxy.enabled
            ? `socks5://${networkIPv4}:${lanProxy.socks_port} · socks5h://${networkIPv4}:${lanProxy.socks_port}`
            : t('启用后，局域网客户端可使用 SOCKS5；使用 socks5h 时域名也交给代理端解析。')}</p>
        </article>
      </div>
      <div className="source-actions"><button type="button" onClick={() => void load()} disabled={saving}>{t('重新读取')}</button><button className="primary" type="button" onClick={() => void saveRuntime()} disabled={saving}>{saving ? t('正在保存…') : t(running ? '保存并重启网关' : '保存运行参数')}</button></div>
    </section>}
  </>
}
