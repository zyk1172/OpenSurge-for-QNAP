import { useEffect, useState } from 'react'
import { api, request, waitForOperation } from '../api'
import { SectionTitle } from '../components/Common'
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

  return <>
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

        <div className={hostRouting.tailscale_detected ? 'ok-notice' : 'notice'}>
          <strong>{t(hostRouting.tailscale_detected ? '检测到 NAS 本机 Tailscale' : '未检测到 NAS 本机 Tailscale')}</strong>
          {hostRouting.tailscale_detected ? <div className="inventory">
            <span><strong>{t('宿主接口')}</strong><br />{hostRouting.tailscale_interface || 'tailscale0'}</span>
            <span><strong>MagicDNS</strong><br />{hostRouting.tailscale_dns_protected ? t('已保留给 Tailscale') : t('需要检查')}</span>
            <span><strong>{t('Tailnet 路由')}</strong><br />{hostRouting.tailscale_routes_protected ? t('已保留给 Tailscale') : t('需要检查')}</span>
          </div> : <p>{t('共存保护会保留，Tailscale 启动后自动生效。')}</p>}
        </div>

        <div className={hostRouting.enabled ? 'ok-notice' : hostRouting.desired ? 'notice warn' : 'notice'}>
          <strong>{t(hostRouting.enabled ? 'NAS 主机接管已启用' : hostRouting.desired ? 'NAS 主机接管等待网关就绪' : 'NAS 主机接管未启用')}</strong>
          <p>{t(hostRouting.enabled
            ? 'NAS 公网 IPv4 经 OpenSurge；局域网、Docker 与 QTS 主路由保持不变。'
            : hostRouting.desired
              ? '网关就绪后自动恢复接管。'
              : '未启用；QTS 原有路由保持不变。')}</p>
          {hostRouting.error && <p className="muted">{hostRouting.error}</p>}
        </div>

        <div className="source-actions">
          <button className="primary" type="button" disabled={hostRoutingBusy || !hostPolicyDirty} onClick={() => void saveHostPolicy()}>{t(hostRoutingBusy ? '正在保存…' : '保存接管策略')}</button>
          <button type="button" className={hostRouting.desired ? 'danger' : 'primary'} disabled={hostRoutingBusy || (!hostRouting.desired && (!hostRouting.supported || !running || !hostRouting.gateway_ready))} onClick={() => void toggleHostRouting()}>{t(hostRoutingBusy ? '正在切换…' : hostRouting.desired ? '停止 NAS 接管' : '让 NAS 使用 OpenSurge')}</button>
          <button type="button" disabled={hostRoutingBusy} onClick={() => void load()}>{t('刷新状态')}</button>
        </div>
        <p className="muted compact-note">{t('仅接管 NAS 宿主机 IPv4；不管理 IPv6。')}</p>
      </> : <div className="empty">{t('正在读取 NAS 主机路由能力…')}</div>}
    </section>

    {draft && <section className="section qnap-runtime-section">
      <SectionTitle title="运行参数" subtitle="保存到 /data；运行中保存会重启网关" />
      <div className="source-import-grid qnap-runtime-grid">
        <article className="source-import-card">
          <label><span>{t('DNS 上游')}</span><input value={draft.dns.upstream} onChange={event => patch({ dns: { ...draft.dns, upstream: event.target.value } })} placeholder="127.0.0.1#1053" /></label>
          <p className="muted">{t('OpenSurge 内部 DNS 上游。')}</p>
        </article>
        <article className="source-import-card">
          <label className="sidebar-switch"><input type="checkbox" checked={draft.mihomo.store_fake_ip} onChange={event => patch({ mihomo: { ...draft.mihomo, store_fake_ip: event.target.checked } })} /><span><strong>Store fake-ip</strong><small>{t('持久化 fake-ip 映射')}</small></span></label>
          <label className="sidebar-switch"><input type="checkbox" checked={draft.transparent.strict_route} onChange={event => patch({ transparent: { ...draft.transparent, strict_route: event.target.checked } })} /><span><strong>TUN strict-route</strong><small>{t('启用 Mihomo TUN strict-route')}</small></span></label>
        </article>
      </div>
      <div className="notice warn"><strong>{t('IPv6 未接管')}</strong><p>{t('QNAP 数据面仅使用 IPv4；IPv6 继续由现有网络管理。')}</p></div>
      <div className="source-actions"><button type="button" onClick={() => void load()} disabled={saving}>{t('重新读取')}</button><button className="primary" type="button" onClick={() => void saveRuntime()} disabled={saving}>{saving ? t('正在保存…') : t(running ? '保存并重启网关' : '保存运行参数')}</button></div>
    </section>}

    <section className="section qnap-advanced-section">
      <SectionTitle title="高级代理配置" subtitle="Mihomo 高级项在“代理与规则源”中管理" />
      <div className="notice">
        <strong>{t('使用 Profile Overlay')}</strong>
        <p>{t('规则、Provider、策略组和 DNS 高级项可在“代理与规则源”中编辑并预览。')}</p>
      </div>
      <div className="source-actions"><button className="primary" type="button" onClick={onNavigate}>{t('打开代理与规则源')}</button></div>
    </section>

    <section className="section qnap-deployment-section">
      <SectionTitle title="部署网络修改" subtitle="需要重建容器" />
      <p className="muted">{t('QNET 父接口、IPv4、CIDR 或主路由需在 Compose 中修改；复用 /data 即可保留配置。')}</p>
    </section>
  </>
}
