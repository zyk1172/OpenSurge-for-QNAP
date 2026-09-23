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
  const dnsListen = draft?.dns.listen || networkIPv4
  const resolverUpstreams = (actual?.snapshot.dns || [])
    .filter(value => value && value !== dnsListen && value !== '127.0.0.1')
  const resolverUpstreamLabel = resolverUpstreams.length ? resolverUpstreams.join(' · ') : router
  const dnsFrontendState = overview?.status.dns || (running ? 'unknown' : 'stopped')
  const dnsFrontendLabel = dnsFrontendState === 'running'
    ? t('运行中')
    : dnsFrontendState === 'legacy-dnsmasq'
      ? t('兼容运行')
      : dnsFrontendState === 'stopped'
        ? t('已停止')
        : t('需要检查')
  const defaultDNSView = draft?.gateway.mode === 'same_lan' ? 'Resolver View' : 'Gateway View'
  const defaultDNSViewNote = draft?.gateway.mode === 'same_lan'
    ? t('旁路由默认返回 Real-IP；设备策略可切换到 Gateway View。')
    : t('接管客户端默认进入 Fake-IP 视图。')

  return <>
    <PageHeader eyebrow="QNAP NETWORK" title={t('网络设置')} description={t('查看容器网络、DNS 实际运行路径，并管理 NAS 主机 IPv4 接管与网关运行参数。')} />

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

    <section className="section qnap-dns-section">
      <SectionTitle title="DNS 服务" subtitle="SmartDNS 双视图 · 显示实际生效的 DNS 运行路径" />
      <div className="inventory qnap-dns-overview" aria-label={t('DNS 实际运行状态')}>
        <span>
          <strong>{t('DNS 前门')}</strong>
          <b className={dnsFrontendState === 'running' || dnsFrontendState === 'legacy-dnsmasq' ? 'ok' : ''}>{dnsFrontendLabel}</b>
          <small>SmartDNS</small>
        </span>
        <span>
          <strong>{t('LAN 监听')}</strong>
          <b>{dnsListen}:53</b>
          <small>UDP / TCP 53</small>
        </span>
        <span>
          <strong>Gateway View</strong>
          <b>Mihomo Fake-IP</b>
          <small>127.0.0.1:1053</small>
        </span>
        <span>
          <strong>Resolver View</strong>
          <b>Real-IP</b>
          <small>{resolverUpstreamLabel}</small>
        </span>
      </div>
      <div className="qnap-dns-detail-grid">
        <article className="qnap-dns-detail-card">
          <span>
            <strong>{t('默认 DNS 视图')}</strong>
            <small>{defaultDNSViewNote}</small>
          </span>
          <b>{defaultDNSView}</b>
        </article>
        <article className="qnap-dns-detail-card">
          <span>
            <strong>{t('TUN DNS 劫持')}</strong>
            <small>{t('TUN 内的 53 端口查询交给 Mihomo DNS 处理。')}</small>
          </span>
          <b>any:53</b>
        </article>
      </div>
      <div className="notice qnap-dns-note">
        <strong>{t('DNS 双视图已生效')}</strong>
        <p>{t('Gateway View 只使用 Mihomo Fake-IP DNS；Resolver View 使用系统真实 DNS。设备级 DNS 视图由设备策略决定，NAS 自身的 DNS 接管仍在下方“NAS 主机接管”中单独控制。')}</p>
      </div>
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

        <div className="qnap-host-policy-grid">
          <article className="qnap-host-policy-card qnap-host-dns-card">
            <span className="qnap-host-policy-copy">
              <strong>{t('NAS DNS 接管模式')}</strong>
              <small>{t(dnsMode === 'auto'
                ? '普通 DNS 经 OpenSurge；保留宿主 VPN 的更高优先级。'
                : dnsMode === 'opensurge'
                  ? 'NAS DNS 经 OpenSurge；Tailscale 优先级由共存保护决定。'
                  : '不接管 NAS DNS。')}</small>
            </span>
            <select className="qnap-host-policy-select" aria-label={t('NAS DNS 接管模式')} value={dnsMode} disabled={hostRoutingBusy} onChange={event => { setDNSMode(event.target.value as HostDNSMode); setHostPolicyDirty(true) }}>
              <option value="auto">{t('自动（推荐）')}</option>
              <option value="opensurge">{t('经 OpenSurge')}</option>
              <option value="host">{t('保留宿主 DNS')}</option>
            </select>
          </article>
          <article className="qnap-host-policy-card qnap-host-tailscale-card">
            <span className="qnap-host-policy-copy">
              <strong>{t('Tailscale 共存保护')}</strong>
              <small>{t('保留宿主 VPN 与 MagicDNS 的优先级。')}</small>
              <small>{t('仅在需要覆盖宿主 VPN 路由时关闭。')}</small>
            </span>
            <button className={`overlay-switch ${protectTailscale ? 'on' : ''}`} type="button" role="switch" aria-label={t('Tailscale 共存保护')} aria-checked={protectTailscale} disabled={hostRoutingBusy} onClick={() => { setProtectTailscale(current => !current); setHostPolicyDirty(true) }}><i aria-hidden="true" /><span>{t(protectTailscale ? '已启用' : '已停用')}</span></button>
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

    {draft && <section className="section qnap-runtime-section">
      <SectionTitle title="运行参数" subtitle="保存到 /data；运行中保存会重启网关" />
      <div className="source-import-grid qnap-runtime-grid">
        <article className="source-import-card qnap-runtime-card">
          <strong className="qnap-runtime-title">Store fake-ip</strong>
          <button className={`overlay-switch ${draft.mihomo.store_fake_ip ? 'on' : ''}`} type="button" role="switch" aria-label="Store fake-ip" aria-checked={draft.mihomo.store_fake_ip} onClick={() => patch({ mihomo: { ...draft.mihomo, store_fake_ip: !draft.mihomo.store_fake_ip } })}><i aria-hidden="true" /><span>{t(draft.mihomo.store_fake_ip ? '已启用' : '已停用')}</span></button>
          <small className="qnap-runtime-note">{t('持久化 fake-ip 映射')}</small>
        </article>
        <article className="source-import-card qnap-runtime-card">
          <strong className="qnap-runtime-title">TUN strict-route</strong>
          <button className={`overlay-switch ${draft.transparent.strict_route ? 'on' : ''}`} type="button" role="switch" aria-label="TUN strict-route" aria-checked={draft.transparent.strict_route} onClick={() => patch({ transparent: { ...draft.transparent, strict_route: !draft.transparent.strict_route } })}><i aria-hidden="true" /><span>{t(draft.transparent.strict_route ? '已启用' : '已停用')}</span></button>
          <small className="qnap-runtime-note">{t('严格接管 TUN 路由')}</small>
        </article>
        <article className="source-import-card qnap-runtime-card qnap-runtime-card-wide qnap-runtime-socks-card">
          <strong className="qnap-runtime-title">LAN SOCKS5 / SOCKS5H</strong>
          <button className={`overlay-switch ${lanProxy.enabled ? 'on' : ''}`} type="button" role="switch" aria-label="LAN SOCKS5 / SOCKS5H" aria-checked={lanProxy.enabled} onClick={() => patch({ lan_proxy: { ...lanProxy, enabled: !lanProxy.enabled } })}><i aria-hidden="true" /><span>{t(lanProxy.enabled ? '已启用' : '已停用')}</span></button>
          <small className="qnap-runtime-note">{t('仅开放独立 SOCKS listener，不暴露内部 mixed-port')}</small>
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
