import { useEffect, useState } from 'react'
import { api, request, waitForOperation } from '../api'
import { ActionBar, Empty, FormField, PageHeader, SectionTitle, Switch } from '../components/Common'
import type { OperationNotification } from '../components/OperationNotifications'
import type { ControlConfig, NetworkDefaults, Overview } from '../types'
import { t } from '../i18n'
import { product } from '../product'
import { resolverPresets, validateResolverDraft } from '../dnsResolvers'

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
  dns_preserved?: boolean
  effective_dns_mode?: HostDNSMode
  dns_mode: HostDNSMode
  protect_tailscale: boolean
  tailscale_detected: boolean
  tailscale_interface?: string
  tailscale_dns_protected: boolean
  tailscale_routes_protected: boolean
  error?: string
  checked_at: string
}

type DNSRuntime = {
  configured: boolean
  listen?: string
  default_view?: 'gateway' | 'resolver'
  gateway_upstream?: string
  resolver_upstreams: string[]
  resolver_proxy?: string
  mihomo_resolver_listen?: string
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
  const [dnsRuntime, setDNSRuntime] = useState<DNSRuntime | null>(null)
  const [dnsMode, setDNSMode] = useState<HostDNSMode>('auto')
  const [protectTailscale, setProtectTailscale] = useState(true)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [hostRoutingBusy, setHostRoutingBusy] = useState(false)
  const [hostPolicyDirty, setHostPolicyDirty] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [presetAddress, setPresetAddress] = useState(resolverPresets[0].address)
  const busy = loading || saving || hostRoutingBusy

  const running = overview?.status.gateway === 'running' || overview?.status.gateway === 'degraded'
  const interrupted = overview?.status.runtime_state === 'interrupted'

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const [config, network, host, dnsSnapshot] = await Promise.all([
        api.config(),
        api.networkDefaults('same_lan').catch(() => null),
        product.host_takeover ? request<QNAPHostRoutingStatus>('/api/v1/qnap-host-routing').catch(() => null) : Promise.resolve(null),
        request<DNSRuntime>('/api/v1/network/dns-runtime').catch(() => null),
      ])
      setDraft(config)
      setActual(network)
      setHostRouting(host)
      setDNSRuntime(dnsSnapshot)
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
    if (!hostRouting || busy) return
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
    if (!hostRouting || busy || !hostPolicyDirty) return
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
    if (!draft || busy) return
    setSaving(true)
    setError('')
    setMessage('')
    let stoppedForSave = false
    try {
      const resolverError = validateResolverDraft(draft)
      if (resolverError) throw new Error(t(resolverError))
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
      const resolverSnapshot = (cfg: ControlConfig) => JSON.stringify({
        mode: cfg.dns.resolver_mode || 'system',
        allow: cfg.dns.resolver_allow_plaintext || false,
        proxy: cfg.dns.resolver_proxy_enabled || false,
        target: cfg.dns.resolver_proxy_target?.trim() || '',
        mihomo: cfg.dns.mihomo_use_smartdns || false,
        upstreams: (cfg.dns.resolver_upstreams || []).map(upstream => ({ address: upstream.address.trim(), bootstrap_ipv4: upstream.bootstrap_ipv4?.trim() || '' })),
      })
      if (resolverSnapshot(ipv4OnlyDraft) !== resolverSnapshot(reread)) throw new Error(t('上游 DNS 保存后校验失败，请重新读取配置。'))
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

  const resolverMode = draft?.dns.resolver_mode || 'system'
  const resolverUpstreams = draft?.dns.resolver_upstreams || []
  const preset = resolverPresets.find(item => item.address === presetAddress) || resolverPresets[0]
  const presetAdded = resolverUpstreams.some(item => item.address.trim() === preset.address && item.bootstrap_ipv4?.trim() === preset.bootstrap_ipv4)
  const updateResolver = (index: number, next: Partial<(typeof resolverUpstreams)[number]>) => {
    if (!draft) return
    patch({ dns: { ...draft.dns, resolver_upstreams: resolverUpstreams.map((upstream, at) => at === index ? { ...upstream, ...next } : upstream) } })
  }
  const addResolver = (preset?: (typeof resolverPresets)[number]) => {
    if (!draft || resolverUpstreams.length >= 16) return
    const upstream = preset ? { address: preset.address, bootstrap_ipv4: preset.bootstrap_ipv4 } : { address: '', bootstrap_ipv4: '' }
    if (preset && resolverUpstreams.some(existing => existing.address === upstream.address && existing.bootstrap_ipv4 === upstream.bootstrap_ipv4)) return
    patch({ dns: { ...draft.dns, resolver_mode: 'custom', resolver_upstreams: [...resolverUpstreams, upstream] } })
  }

  const networkInterface = actual?.snapshot.interface || draft?.gateway.interface || overview?.status.interface || 'eth0'
  const networkIPv4 = actual?.snapshot.ipv4 || draft?.gateway.lan_ip || overview?.status.lan_ip || '—'
  const networkCIDR = actual?.snapshot.ipv4
    ? `${actual.snapshot.ipv4}/${draft?.gateway.lan_prefix_len ?? 24}`
    : draft ? `${draft.gateway.lan_ip}/${draft.gateway.lan_prefix_len}` : '—'
  const router = actual?.snapshot.router || t('Docker 网络配置')
  const lanProxy = draft?.lan_proxy ?? { enabled: false, socks_port: 7891 }
  const dnsListen = dnsRuntime?.listen || t('未读取运行配置')
  // Read the applied SmartDNS config, not raw Docker DNS which may contain
  // 127.0.0.11 or the QNAP host's 100.100.100.100 MagicDNS resolver.
  const resolverUpstreamLabel = dnsRuntime?.resolver_upstreams.length
    ? dnsRuntime.resolver_upstreams.join(' · ')
    : t('无已应用的上游记录')
  const dnsFrontendState = overview?.status.dns || (running ? 'unknown' : 'stopped')
  const dnsFrontendLabel = dnsFrontendState === 'running'
    ? t('运行中')
    : dnsFrontendState === 'legacy-dnsmasq'
      ? t('兼容运行')
      : dnsFrontendState === 'stopped'
        ? t('已停止')
        : t('需要检查')
  const defaultDNSView = dnsRuntime?.default_view
    ? (dnsRuntime.default_view === 'resolver' ? 'Resolver View' : 'Gateway View')
    : t('未读取运行配置')
  const effectiveHostDNSMode = hostRouting?.effective_dns_mode
  const hostDNSStatus = !hostRouting?.enabled ? t('未接管')
    : effectiveHostDNSMode === 'host' ? (hostRouting.dns_preserved ? t('经 QTS 路由') : t('需要检查'))
    : effectiveHostDNSMode === 'opensurge' ? (hostRouting.dns_redirect ? t('经 Mihomo TUN') : t('需要检查'))
    : t('需要检查')
  const defaultDNSViewNote = draft?.gateway.mode === 'same_lan'
    ? t('旁路由默认返回 Real-IP；设备策略可切换到 Gateway View。')
    : t('接管客户端默认进入 Fake-IP 视图。')

  return <>
    <PageHeader eyebrow="NAS NETWORK" title={t('网络设置')} description={t('查看容器网络、DNS 实际运行路径与网关运行参数。')} />
    {product.experimental && <div className="notice warn"><strong>{t('实验性 NAS 适配')}</strong><p>{t('部署与网关核心可共用；当前平台尚未完成实机客户端、重启与长期运行验证。')}</p></div>}

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
      <div className="notice"><strong>{t('容器网络由部署配置决定')} · {product.network_driver}</strong><p>{t('父网卡与静态网络在创建容器时绑定；容器内通常只显示 eth0。')}</p></div>
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
          <b>{dnsListen}</b>
          <small>UDP / TCP 53</small>
        </span>
        <span>
          <strong>Gateway View</strong>
          <b>Mihomo Fake-IP</b>
          <small>{dnsRuntime?.gateway_upstream || t('未读取运行配置')}</small>
        </span>
        <span>
          <strong>Resolver View</strong>
          <b>Real-IP</b>
          <small>{resolverUpstreamLabel}</small>
        </span>
      </div>
      <div className="qnap-dns-detail-grid">
        <article className="qnap-dns-detail-card">
          <span><strong>{t('Resolver View 查询出口')}</strong><small>{t('来自已写入的 SmartDNS 配置')}</small></span>
          <b>{dnsRuntime?.configured ? t(dnsRuntime.resolver_proxy ? 'OpenSurge 代理' : '直连上游') : t('未读取运行配置')}</b>
        </article>
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
          <b>UDP any:53 · TCP any:53</b>
        </article>
      </div>
      <div className="notice qnap-dns-note">
        <strong>{t(dnsRuntime?.configured && dnsFrontendState === 'running' ? '已读取 SmartDNS 运行配置' : 'DNS 运行配置待确认')}</strong>
        <p>{t('所列上游来自已写入的 SmartDNS 配置，不保证上游可达；NAS 宿主 DNS、qBittorrent 与此处的局域网 SmartDNS 可能是不同路径。')}</p>
      </div>
      {draft && <div className="ui-card smartdns-settings" aria-busy={saving}>
        <h3>{t('SmartDNS 上游 DNS 设置')}</h3>
        <p className="muted">{t('先选择可信的上游，再比较连接速度。加密 DNS 可防止传输途中被篡改，但不能保证服务商的答案没有污染。')}</p>
        <FormField label="Resolver View 上游来源">
          <select aria-label={t('Resolver View 上游来源')} value={resolverMode} disabled={busy} onChange={event => patch({ dns: { ...draft.dns, resolver_mode: event.target.value as 'system' | 'custom', ...(event.target.value === 'system' ? { resolver_proxy_enabled: false, mihomo_use_smartdns: false } : {}) } })}>
            <option value="custom">{t('自定义（推荐：加密优先）')}</option>
            <option value="system">{t('系统 DNS（兼容旧配置）')}</option>
          </select>
        </FormField>
        {resolverMode === 'system' ? <p className="muted">{t('当前继承系统 DNS，过滤不可用地址后可能回退到主路由；此模式不保证上游可信。选择自定义后只使用你填写的上游。')}</p> : <>
          <div className="smartdns-preset-toolbar">
            <FormField label="上游 DNS 预设">
              <select value={presetAddress} disabled={busy} onChange={event => setPresetAddress(event.target.value)}>
                {resolverPresets.map(item => <option key={item.address} value={item.address}>{t(item.name)} DoH</option>)}
              </select>
            </FormField>
            <div className="ui-actions ui-actions--start">
              <button className="ui-button" type="button" disabled={busy || presetAdded || resolverUpstreams.length >= 16} onClick={() => addResolver(preset)}>{t(presetAdded ? '已添加' : '添加预设')}</button>
              <button className="ui-button" type="button" disabled={busy || resolverUpstreams.length >= 16} onClick={() => addResolver()}>{t('添加自定义上游')}</button>
            </div>
          </div>
          <p className="muted">{t('预设只是配置示例，不是无污染认证。请选择你信任且在当前网络可达的服务商；Quad9 会过滤已知恶意域名。')}</p>
          {!resolverUpstreams.length && <Empty text="尚未添加上游 DNS" />}
          <div className="ui-form-grid">
            {resolverUpstreams.map((upstream, index) => <article className="ui-card smartdns-upstream-card" key={index}>
              <FormField label={`${t('上游地址')} · ${index + 1}`}>
                <input aria-label={`${t('上游地址')} ${index + 1}`} value={upstream.address} disabled={busy} spellCheck={false} autoCapitalize="none" placeholder="https://cloudflare-dns.com/dns-query" onChange={event => updateResolver(index, { address: event.target.value })} />
              </FormField>
              <FormField label="固定连接 IPv4（加密上游必填）">
                <input aria-label={`${t('固定连接 IPv4')} ${index + 1}`} value={upstream.bootstrap_ipv4 || ''} disabled={busy} inputMode="decimal" placeholder="1.1.1.1" onChange={event => updateResolver(index, { bootstrap_ipv4: event.target.value })} />
              </FormField>
              <div className="ui-actions"><button className="ui-button ui-button--danger" type="button" disabled={busy} onClick={() => patch({ dns: { ...draft.dns, resolver_upstreams: resolverUpstreams.filter((_, at) => at !== index) } })}>{t('移除上游')} · {index + 1}</button></div>
            </article>)}
          </div>
          <p className="muted">{t('DoH：https://域名/dns-query；DoT：tls://域名:853。固定连接 IPv4 只用于连接，TLS 仍严格验证域名和证书；不依赖系统 DNS 引导。')}</p>
          <article className="ui-card smartdns-option-card">
            <div className="smartdns-setting">
              <strong>{t('允许明文 DNS')}</strong>
              <Switch label="允许明文 DNS" checked={draft.dns.resolver_allow_plaintext || false} disabled={busy} onChange={checked => patch({ dns: { ...draft.dns, resolver_allow_plaintext: checked } })} />
            </div>
            <p className="muted">{t('明确允许后可填写 IPv4、udp://IPv4:53 或 tcp://IPv4:53。有加密上游时，明文只作为加密上游全部失败后的备选；仅填明文时无法防止途中污染。')}</p>
          </article>
          <div className="ui-form-grid">
            <article className="ui-card smartdns-option-card">
              <div className="smartdns-setting">
                <strong>{t('上游 DNS 查询经过 OpenSurge 代理')}</strong>
                <Switch label="上游 DNS 查询经过 OpenSurge 代理" checked={draft.dns.resolver_proxy_enabled || false} disabled={busy} onChange={checked => patch({ dns: { ...draft.dns, resolver_proxy_enabled: checked } })} />
              </div>
              {draft.dns.resolver_proxy_enabled && <FormField label="DNS 代理节点或策略组">
                <input aria-label={t('DNS 代理节点或策略组')} list="smartdns-proxy-targets" value={draft.dns.resolver_proxy_target || ''} disabled={busy} placeholder={t('填写当前配置中的名称')} onChange={event => patch({ dns: { ...draft.dns, resolver_proxy_target: event.target.value } })} />
                <datalist id="smartdns-proxy-targets">{(overview?.policies || []).filter(group => !group.name.startsWith('open-surge/') && !/^(DIRECT|REJECT|REJECT-DROP|REJECT-TINYGIF|PASS|COMPATIBLE|GLOBAL)$/i.test(group.name)).map(group => <option key={group.name} value={group.name} />)}</datalist>
              </FormField>}
              <p className="muted">{t('通过所选节点或策略组查询 DoH、DoT 或 TCP 上游，故障时不绕过代理直连。请选择使用代理节点的策略组；组内出口选择仍会影响实际路径。代理模式按可信上游的查询延迟选择答案，不使用 NAS 直连测速。')}</p>
            </article>
            <article className="ui-card smartdns-option-card">
              <div className="smartdns-setting">
                <strong>{t('用 SmartDNS 替换 Mihomo DNS 上游')}</strong>
                <Switch label="用 SmartDNS 替换 Mihomo DNS 上游" checked={draft.dns.mihomo_use_smartdns || false} disabled={busy} onChange={checked => patch({ dns: { ...draft.dns, mihomo_use_smartdns: checked } })} />
              </div>
              <p className="muted">{t('覆盖订阅中的普通解析、直连解析和备用 DNS 上游，使用 SmartDNS 的真实解析结果；保留 Fake-IP、过滤规则、hosts 和 TUN DNS 劫持。关闭后恢复配置文件的 DNS 设置。')}</p>
            </article>
          </div>
          <p className="muted">{t('加密上游的真实解析缓存可在重启后复用；修改上游、解析规则或代理配置会使用新缓存。允许明文时不保存缓存到磁盘。')}</p>
          <div className="notice">{t('两个开关默认关闭，互相独立。启用任一项后，代理节点域名通过所选加密上游直接解析，并使用固定连接 IPv4 引导，以避免启动时的解析循环；普通查询是否经过代理由第一个开关决定。自定义模式不自动回退到系统或主路由 DNS。')}</div>
        </>}
        <ActionBar status={t('已配置 {{count}} / 16 个上游', { count: resolverUpstreams.length })}><button className="ui-button ui-button--primary" type="button" aria-label={t('保存上游 DNS')} disabled={busy} onClick={() => void saveRuntime()}>{saving ? t('正在保存…') : t(running ? '保存并重启网关' : '保存上游 DNS')}</button></ActionBar>
      </div>}
    </section>

    {product.host_takeover ? <section className="section qnap-host-takeover-section">
      <SectionTitle title="NAS 主机接管" subtitle="让 NAS 宿主机 IPv4 通过 OpenSurge" />
      {hostRouting ? <>
        <div className="inventory">
          <span><strong>{t('NAS 主机')}</strong><br />{hostRouting.host_ipv4 || '—'}{hostRouting.host_interface ? ` · ${hostRouting.host_interface}` : ''}</span>
          <span><strong>{t('OpenSurge 下一跳')}</strong><br />{hostRouting.gateway_ipv4 || networkIPv4}</span>
          <span><strong>{t('QTS 回退网关')}</strong><br />{hostRouting.fallback_gateway || router}</span>
          <span><strong>{t('IPv4 DNS')}</strong><br />{hostDNSStatus}</span>
        </div>

        <div className="qnap-host-policy-grid">
          <article className="qnap-host-policy-card qnap-host-dns-card">
            <span className="qnap-host-policy-copy">
              <strong>{t('NAS DNS 接管模式')}</strong>
              <small>{t(dnsMode === 'auto'
                ? '自动：Tailscale 共存保护生效且检测到 Tailscale 时保留 QTS DNS 路由，否则经 Mihomo TUN。'
                : dnsMode === 'opensurge'
                  ? '强制将 NAS 的 UDP/TCP 53 端口查询交给 Mihomo TUN；不改变系统 DNS 地址。'
                  : '通过 QTS 主路由处理 NAS 的 UDP/TCP 53；不会更改 QTS 设置的 DNS 地址。')}</small>
            </span>
            <select className="qnap-host-policy-select" aria-label={t('NAS DNS 接管模式')} value={dnsMode} disabled={busy} onChange={event => { setDNSMode(event.target.value as HostDNSMode); setHostPolicyDirty(true) }}>
              <option value="auto">{t('自动（推荐）')}</option>
              <option value="opensurge">{t('经 OpenSurge')}</option>
              <option value="host">{t('保留宿主 DNS')}</option>
            </select>
          </article>
          <article className="qnap-host-policy-card qnap-host-tailscale-card">
            <span className="qnap-host-policy-copy">
              <strong>{t('Tailscale 共存保护')}</strong>
              <small>{t('优先保留 Tailscale 的已有路由规则；不负责修改或修复 QTS 的系统 DNS。')}</small>
              <small>{t('仅在需要覆盖宿主 VPN 路由时关闭。')}</small>
            </span>
            <Switch label="Tailscale 共存保护" checked={protectTailscale} disabled={busy} onChange={checked => { setProtectTailscale(checked); setHostPolicyDirty(true) }} />
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

        <div className="ui-actions">
          <button className="ui-button ui-button--primary" type="button" disabled={busy || !hostPolicyDirty} onClick={() => void saveHostPolicy()}>{t(hostRoutingBusy ? '正在保存…' : '保存接管策略')}</button>
          <button type="button" className={`ui-button ${hostRouting.desired ? 'ui-button--danger' : 'ui-button--primary'}`} disabled={busy || (!hostRouting.desired && (!hostRouting.supported || !running || !hostRouting.gateway_ready))} onClick={() => void toggleHostRouting()}>{t(hostRoutingBusy ? '正在切换…' : hostRouting.desired ? '停止 NAS 接管' : '让 NAS 使用 OpenSurge')}</button>
          <button className="ui-button" type="button" disabled={busy} onClick={() => void load()}>{t('刷新状态')}</button>
        </div>
        <div className="notice">{t('仅对所显示的 NAS 主机源 IPv4 应用接管；不主动修改第二网卡的 PT 做种源地址。若 QTS 系统 DNS 已被 Tailscale 改为 100.100.100.100，仍需单独检查系统 DNS。')}</div>
      </> : <div className="empty">{t('正在读取 NAS 主机路由能力…')}</div>}
    </section> : <section className="section"><SectionTitle title="NAS 宿主网络" subtitle="macvlan 宿主隔离" /><p>{t('macvlan 默认隔离 NAS 宿主机与容器。请从另一台局域网设备访问 Web；当前适配不提供 NAS 主机接管。')}</p></section>}

    {draft && <section className="section qnap-runtime-section">
      <SectionTitle title="运行参数" subtitle="保存到 /data；运行中保存会重启网关" />
      <div className="qnap-runtime-grid">
        <article className="ui-card qnap-runtime-card">
          <strong className="qnap-runtime-title">Store fake-ip</strong>
          <Switch label="Store fake-ip" checked={draft.mihomo.store_fake_ip} disabled={busy} onChange={checked => patch({ mihomo: { ...draft.mihomo, store_fake_ip: checked } })} />
          <small className="qnap-runtime-note">{t('持久化 fake-ip 映射')}</small>
        </article>
        <article className="ui-card qnap-runtime-card">
          <strong className="qnap-runtime-title">TUN strict-route</strong>
          <Switch label="TUN strict-route" checked={draft.transparent.strict_route} disabled title="QNAP 的 Linux 路由由 OpenSurge 管理，Mihomo auto-route 被强制禁用；此开关无路由控制效果。" />
          <small className="qnap-runtime-note">{t('QNAP 由 OpenSurge 管理 Linux 路由；auto-route 禁用，此项不适用。')}</small>
        </article>
        <article className="ui-card qnap-runtime-card qnap-runtime-card-wide qnap-runtime-socks-card">
          <strong className="qnap-runtime-title">LAN SOCKS5 / SOCKS5H</strong>
          <Switch label="LAN SOCKS5 / SOCKS5H" checked={lanProxy.enabled} disabled={busy} onChange={checked => patch({ lan_proxy: { ...lanProxy, enabled: checked } })} />
          <small className="qnap-runtime-note">{t('仅开放独立 SOCKS listener，不暴露内部 mixed-port')}</small>
          <FormField label="LAN SOCKS 端口">
            <input aria-label={t('LAN SOCKS 端口')} type="number" min={1} max={65535} value={lanProxy.socks_port} disabled={busy} onChange={event => patch({ lan_proxy: { ...lanProxy, socks_port: Number(event.target.value) } })} />
          </FormField>
          <p className="muted">{lanProxy.enabled
            ? `socks5://${networkIPv4}:${lanProxy.socks_port} · socks5h://${networkIPv4}:${lanProxy.socks_port}`
            : t('启用后，局域网客户端可使用 SOCKS5；使用 socks5h 时域名也交给代理端解析。')}</p>
        </article>
      </div>
      <ActionBar><button className="ui-button" type="button" onClick={() => void load()} disabled={busy}>{t('重新读取')}</button><button className="ui-button ui-button--primary" type="button" onClick={() => void saveRuntime()} disabled={busy}>{saving ? t('正在保存…') : t(running ? '保存并重启网关' : '保存运行参数')}</button></ActionBar>
    </section>}
  </>
}
