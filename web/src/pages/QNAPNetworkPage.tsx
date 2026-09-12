import { useEffect, useState } from 'react'
import { api, request, waitForOperation } from '../api'
import { PageHeader, SectionTitle } from '../components/Common'
import type { OperationNotification } from '../components/OperationNotifications'
import { RemoteManagementCard } from '../components/RemoteManagementCard'
import type { ControlConfig, NetworkDefaults, Overview } from '../types'
import { t } from '../i18n'

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
  onNavigate: () => void
  onNotify: (notification: OperationNotification) => void
}) {
  const [draft, setDraft] = useState<ControlConfig | null>(null)
  const [actual, setActual] = useState<NetworkDefaults | null>(null)
  const [hostRouting, setHostRouting] = useState<QNAPHostRoutingStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [lifecycleBusy, setLifecycleBusy] = useState(false)
  const [hostRoutingBusy, setHostRoutingBusy] = useState(false)
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

  const toggleHostRouting = async () => {
    if (!hostRouting || hostRoutingBusy) return
    const nextEnabled = !hostRouting.desired
    if (nextEnabled && !window.confirm(t('启用后，NAS 本机发起的 IPv4 公网流量和 IPv4 DNS 将优先经过 OpenSurge。QTS 主路由配置不会被修改。继续吗？'))) return
    setHostRoutingBusy(true)
    setError('')
    setMessage('')
    try {
      const status = await request<QNAPHostRoutingStatus>('/api/v1/qnap-host-routing', {
        method: 'PUT',
        body: JSON.stringify({ enabled: nextEnabled }),
      })
      setHostRouting(status)
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

      // QNAP is deliberately IPv4-only. Keep the schema fields for older API
      // clients, but never allow this page to persist an old IPv6 experiment.
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
      <div className="notice">
        <strong>{t('QNET 父网卡不在容器内二次选择')}</strong>
        <p>{t('QNAP 物理网卡 / Virtual Switch 由 Docker Compose 的 qnet iface 在创建容器时绑定。容器内部通常只看到 eth0，这是正常现象。')}</p>
      </div>
    </section>

    <section className="section">
      <SectionTitle title="NAS 主机上网" subtitle="一键让 QNAP 宿主机自己的 IPv4 流量通过 OpenSurge；QTS 默认网关仍保持原设置" />
      {hostRouting ? <>
        <div className="inventory">
          <span><strong>{t('NAS 主机')}</strong><br />{hostRouting.host_ipv4 || '—'}{hostRouting.host_interface ? ` · ${hostRouting.host_interface}` : ''}</span>
          <span><strong>{t('OpenSurge 下一跳')}</strong><br />{hostRouting.gateway_ipv4 || networkIPv4}</span>
          <span><strong>{t('QTS 回退网关')}</strong><br />{hostRouting.fallback_gateway || router}</span>
          <span><strong>{t('IPv4 DNS')}</strong><br />{hostRouting.dns_redirect ? t('已透明接管') : t('未接管')}</span>
        </div>
        <div className={hostRouting.enabled ? 'ok-notice' : hostRouting.desired ? 'notice warn' : 'notice'}>
          <strong>{t(hostRouting.enabled ? 'NAS 主机接管已启用' : hostRouting.desired ? 'NAS 主机接管正在等待网关就绪' : 'NAS 主机接管未启用')}</strong>
          <p>{t(hostRouting.enabled
            ? 'NAS 本机产生的 IPv4 公网流量通过策略路由进入 OpenSurge；局域网、Docker 本地路由和 QTS 主路由配置保持原样。'
            : hostRouting.desired
              ? 'OpenSurge 数据面恢复为 ready 后会自动重新接管；未就绪期间 NAS 使用 QTS 原有主路由。'
              : '启用后只增加 OpenSurge 自己的宿主策略路由与 DNS 重定向，不会把 QTS GUI 的默认网关改成容器 IP。')}</p>
          {hostRouting.error && <p className="muted">{hostRouting.error}</p>}
        </div>
        {!hostRouting.supported && <div className="notice warn"><strong>{t('当前容器缺少宿主网络接管能力')}</strong><p>{t('请使用本版本的 Compose 重建一次容器；需要只读挂载宿主 network namespace，并只把 SYS_ADMIN 保留给 root Control 进程。')}</p></div>}
        <div className="source-actions">
          <button
            type="button"
            className={hostRouting.desired ? 'danger' : 'primary'}
            disabled={hostRoutingBusy || (!hostRouting.desired && (!hostRouting.supported || !running || !hostRouting.gateway_ready))}
            onClick={() => void toggleHostRouting()}
          >{t(hostRoutingBusy ? '正在切换…' : hostRouting.desired ? '停止 NAS 接管' : '让 NAS 使用 OpenSurge')}</button>
          <button type="button" disabled={hostRoutingBusy} onClick={() => void load()}>{t('刷新状态')}</button>
        </div>
        <p className="muted">{t('此功能仅接管 NAS 宿主机本身产生的 IPv4 流量；IPv6 仍不由 OpenSurge 管理。容器正常停止或重建时会先释放宿主策略，避免 NAS 被留在失效的下一跳上。')}</p>
      </> : <div className="empty">{t('正在读取 NAS 主机路由能力…')}</div>}
    </section>

    {draft && <section className="section">
      <SectionTitle title="运行参数" subtitle="这些值保存在 /data/config/opensurge.yaml；网关运行时保存会自动执行停止 → 持久化 → 重新启动。" />
      <div className="source-import-grid">
        <article className="source-import-card">
          <label>
            <span>{t('DNS 上游')}</span>
            <input value={draft.dns.upstream} onChange={event => patch({ dns: { ...draft.dns, upstream: event.target.value } })} placeholder="127.0.0.1#1053" />
          </label>
          <p className="muted">{t('这里是 OpenSurge 内部 dnsmasq 到 Mihomo DNS 的上游，不是主路由要填写的 DNS 地址。')}</p>
        </article>
        <article className="source-import-card">
          <label className="sidebar-switch">
            <input type="checkbox" checked={draft.mihomo.store_fake_ip} onChange={event => patch({ mihomo: { ...draft.mihomo, store_fake_ip: event.target.checked } })} />
            <span><strong>Store fake-ip</strong><small>{t('持久化 mihomo fake-ip 映射。')}</small></span>
          </label>
          <label className="sidebar-switch">
            <input type="checkbox" checked={draft.transparent.strict_route} onChange={event => patch({ transparent: { ...draft.transparent, strict_route: event.target.checked } })} />
            <span><strong>TUN strict-route</strong><small>{t('仅影响容器内透明代理数据面。')}</small></span>
          </label>
        </article>
      </div>

      <div className="notice warn">
        <strong>IPv6：OFF / 不支持</strong>
        <p>OpenSurge for QNAP 当前仅支持 IPv4 数据面。容器不会接管、分配、代理或管理 IPv6；IPv6 继续完全由光猫、主路由和运营商处理。旧配置中的 IPv6 开关会在加载时自动迁移为 OFF。</p>
      </div>

      <div className="source-actions">
        <button type="button" onClick={() => void load()} disabled={saving || lifecycleBusy}>{t('重新读取')}</button>
        <button className="primary" type="button" onClick={() => void saveRuntime()} disabled={saving || lifecycleBusy}>{saving ? t('正在保存…') : t(running ? '保存并重启网关' : '保存运行参数')}</button>
      </div>
    </section>}

    <RemoteManagementCard />

    <section className="section" id="gateway-control-bottom">
      <SectionTitle title="如何修改物理网卡或静态 IP" subtitle="这是容器部署操作，不是 OpenSurge 运行配置" />
      <p>{t('停止并重建容器时修改 Compose 中的 QNET 父接口、IPv4、CIDR 或主路由。保留同一个 /data 持久化目录即可保留管理员、订阅、规则、设备策略和运行配置。')}</p>
    </section>
  </>
}
