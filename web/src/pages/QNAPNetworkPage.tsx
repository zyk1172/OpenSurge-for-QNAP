import { useEffect, useState } from 'react'
import { api, waitForOperation } from '../api'
import { PageHeader, SectionTitle } from '../components/Common'
import type { OperationNotification } from '../components/OperationNotifications'
import { RemoteManagementCard } from '../components/RemoteManagementCard'
import type { ControlConfig, NetworkDefaults, Overview } from '../types'
import { t } from '../i18n'

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
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [lifecycleBusy, setLifecycleBusy] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  const running = overview?.status.gateway === 'running' || overview?.status.gateway === 'degraded'
  const interrupted = overview?.status.runtime_state === 'interrupted'
  const stopped = overview?.status.gateway === 'stopped'

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const [config, network] = await Promise.all([
        api.config(),
        api.networkDefaults('same_lan').catch(() => null),
      ])
      setDraft(config)
      setActual(network)
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

  const saveRuntime = async () => {
    if (!draft || saving || lifecycleBusy) return
    const ipv6DNSFakeIPEnabled = draft.transparent.tun_ipv6 !== 'off' && draft.dns.ipv6
    if (ipv6DNSFakeIPEnabled && !draft.transparent.ipv6_shared_l2_ready) {
      setError(t('开启 IPv6 DNS 定向接管前，请先完成主路由 DNS 与 fake-IP IPv6 静态路由，并勾选确认。'))
      return
    }

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

      const saved = await api.saveConfig(draft)
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
  const ipv6DNSFakeIPEnabled = Boolean(draft && draft.transparent.tun_ipv6 !== 'off' && draft.dns.ipv6)
  const ipv6NextHop = qnapLinkLocalIPv6(networkIPv4)

  return <>
    <PageHeader
      eyebrow="QNAP NETWORK"
      title="QNAP 网关网络"
      description="物理网卡、QNET、静态 IP、CIDR 与主路由在创建容器时确定；运行中的容器不再伪装成可以修改 QNAP 宿主网络。"
      action={<button id="gateway-control" className={running ? 'danger' : 'primary'} type="button" disabled={lifecycleBusy || saving || (!running && !stopped && !interrupted)} onClick={() => void runLifecycle()}>{t(lifecycleBusy ? '正在执行…' : interrupted ? '安全清理旧状态' : running ? '停止网关' : '启动网关')}</button>}
    />

    {interrupted && <div className="notice warn" role="status"><strong>{t('检测到上一次容器或 NAS 重启留下的运行状态。')}</strong><p>{t('先执行安全清理；QNAP 版不会触碰宿主 QTS 网络设置。')}</p></div>}
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

      <div className="qnap-ipv6-panel">
        <SectionTitle title={t('IPv6 DNS 定向接管')} subtitle={t('保留主路由 IPv6、公网地址和默认路由，只把 fake-IP IPv6 送入 OpenSurge')} />

        <label className="sidebar-switch">
          <input type="checkbox" checked={ipv6DNSFakeIPEnabled} onChange={event => {
            const enabled = event.target.checked
            patch({
              dns: { ...draft.dns, ipv6: enabled },
              transparent: {
                ...draft.transparent,
                tun_ipv6: enabled ? 'always' : 'off',
                ipv6_shared_l2_ready: false,
              },
            })
          }} />
          <span>
            <strong>{t('启用 IPv6 DNS / fake-IP 定向接管')}</strong>
            <small>{t('不开 RA，不修改客户端 IPv6 网关；普通 IPv6 继续直接走主路由。')}</small>
          </span>
        </label>

        {ipv6DNSFakeIPEnabled && <>
          <div className="source-import-grid">
            <article className="source-import-card qnap-ipv6-values">
              <span><strong>{t('主路由 DNS 上游')}</strong><code>{networkIPv4}</code></span>
              <span><strong>{t('IPv6 fake-IP 网段')}</strong><code>fdfe:dcba:9876::/64</code></span>
              <span><strong>{t('IPv6 静态路由下一跳')}</strong><code>{ipv6NextHop || '—'}</code></span>
              <span><strong>{t('静态路由出口')}</strong><code>{networkInterface}</code></span>
            </article>
            <article className="source-import-card">
              <strong>{t('主路由需要两项设置')}</strong>
              <p>{t('1. 将主路由使用的 DNS 上游指向 OpenSurge IPv4。')}</p>
              <p>{t('2. 添加 IPv6 静态路由：fdfe:dcba:9876::/64 → 上方 OpenSurge link-local 下一跳，并选择 LAN/桥接口。')}</p>
              <p className="muted">{t('主路由原有 IPv6 RA、DHCPv6、IPv6 Bridge/Passthrough 和公网 IPv6 分配保持不变。')}</p>
            </article>
          </div>

          <label className="sidebar-switch">
            <input type="checkbox" checked={draft.transparent.ipv6_shared_l2_ready ?? false} onChange={event => patch({ transparent: { ...draft.transparent, ipv6_shared_l2_ready: event.target.checked } })} />
            <span>
              <strong>{t('主路由 DNS 和 IPv6 静态路由已配置')}</strong>
              <small>{t('确认 fake-IP IPv6 网段会被送到 OpenSurge，普通公网 IPv6 仍使用主路由。')}</small>
            </span>
          </label>

          <div className="notice">
            <strong>{t('客户端无需设置 IPv6')}</strong>
            <p>{t('手机、电视和电脑继续从主路由获得原来的公网 IPv6 与默认路由。只有 OpenSurge DNS 返回的 fake IPv6 会经主路由静态路由进入 TUN，并按照全局 Mihomo 规则决定 DIRECT / PROXY / REJECT。')}</p>
          </div>

          <div className="notice warn">
            <strong>{t('IPv6 不再承诺逐设备策略')}</strong>
            <p>{t('这条路径在 Linux TUN 三层入口看不到原始客户端 MAC，因此按设备 IPv6 策略不作为保证目标；IPv4 设备策略保持原有实现不变。')}</p>
          </div>

          <div className="notice warn">
            <strong>{t('注意统一 fake-IP DNS 对 IPv4 A 查询的影响')}</strong>
            <p>{t('本功能不新增或修改 IPv4 路由，但 Mihomo 的统一 fake-IP DNS 仍可能给 A 查询返回 IPv4 fake-ip。若把 OpenSurge DNS 全局提供给未走 OpenSurge IPv4 数据面的设备，请确认现有 IPv4 fake-ip 路径可达，或在主路由侧使用合适的 DNS 策略。')}</p>
          </div>
        </>}

        {!ipv6DNSFakeIPEnabled && <div className="notice">
          <strong>{t('IPv6 保持原网络行为')}</strong>
          <p>{t('关闭后 OpenSurge 不建立 IPv6 fake-IP TUN 路由。客户端继续完全使用主路由提供的 IPv6。')}</p>
        </div>}
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

function qnapLinkLocalIPv6(ipv4: string): string {
  const parts = ipv4.trim().split('.').map(value => Number(value))
  if (parts.length !== 4 || parts.some(value => !Number.isInteger(value) || value < 0 || value > 255)) return ''
  const upper = ((parts[0] << 8) | parts[1]).toString(16)
  const lower = ((parts[2] << 8) | parts[3]).toString(16)
  return `fe80::1:0:${upper}:${lower}`
}
