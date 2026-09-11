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
  const [ipv6ClientIPv4, setIPv6ClientIPv4] = useState('')

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
      // Older schema-v1 config GET responses do not include ipv6_ra_enabled.
      // Preserve the local draft during a save/reload and otherwise use the
      // authoritative runtime status until the field is present in the GET.
      setDraft(current => ({
        ...config,
        transparent: {
          ...config.transparent,
          ipv6_ra_enabled: config.transparent.ipv6_ra_enabled
            ?? current?.transparent.ipv6_ra_enabled
            ?? overview?.status.ipv6_ra_enabled
            ?? false,
        },
      }))
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
    const ipv6Enabled = draft.transparent.tun_ipv6 !== 'off'
    if (ipv6Enabled && !draft.transparent.ipv6_shared_l2_ready) {
      setError(t(draft.transparent.ipv6_ra_enabled
        ? '开启自动 IPv6 分配前，请确认主路由不会继续向该 LAN 发布竞争 IPv6 默认路由。'
        : '开启手动 IPv6 接管前，请确认目标客户端已使用 OpenSurge IPv6，且没有另一条可用的 IPv6 默认路由。'))
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
      setDraft({
        ...reread,
        transparent: {
          ...reread.transparent,
          ipv6_ra_enabled: reread.transparent.ipv6_ra_enabled ?? draft.transparent.ipv6_ra_enabled ?? false,
        },
      })

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
  const ipv6Enabled = draft?.transparent.tun_ipv6 !== 'off'
  const ipv6Automatic = ipv6Enabled && (draft?.transparent.ipv6_ra_enabled ?? false)
  const clientIPv6 = qnapClientIPv6(ipv6ClientIPv4)

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
          <label className="sidebar-switch">
            <input type="checkbox" checked={draft.dns.ipv6} onChange={event => patch({ dns: { ...draft.dns, ipv6: event.target.checked } })} />
            <span><strong>{t('DNS IPv6')}</strong><small>{t('开启 AAAA / IPv6 fake-IP；IPv6 接管时建议同时开启。')}</small></span>
          </label>
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
        <SectionTitle title={t('IPv6 接管')} subtitle={t('接管方式与客户端 IPv6 分配分开控制')} />
        <div className="source-import-grid">
          <article className="source-import-card">
            <label>
              <span>{t('IPv6 接管模式')}</span>
              <select value={draft.transparent.tun_ipv6} onChange={event => {
                const mode = event.target.value as ControlConfig['transparent']['tun_ipv6']
                patch({
                  transparent: {
                    ...draft.transparent,
                    tun_ipv6: mode,
                    ipv6_ra_enabled: mode === 'off' ? false : draft.transparent.ipv6_ra_enabled,
                    ipv6_shared_l2_ready: mode === 'off' ? false : draft.transparent.ipv6_shared_l2_ready,
                  },
                })
              }}>
                <option value="off">{t('关闭')}</option>
                <option value="auto">{t('自动')}</option>
                <option value="always">{t('强制')}</option>
              </select>
            </label>
            <p className="muted">{t('“自动”遵循 Mihomo 的系统 IPv6 检测；“强制”用于只有 ULA 下游、没有原生 IPv6 出口的 QNAP 环境。')}</p>
          </article>

          <article className="source-import-card">
            <label>
              <span>{t('客户端 IPv6 分配')}</span>
              <select disabled={!ipv6Enabled} value={ipv6Automatic ? 'automatic' : 'manual'} onChange={event => {
                const automatic = event.target.value === 'automatic'
                patch({
                  dns: automatic ? { ...draft.dns, ipv6: true } : draft.dns,
                  transparent: {
                    ...draft.transparent,
                    ipv6_ra_enabled: automatic,
                    ipv6_shared_l2_ready: false,
                  },
                })
              }}>
                <option value="manual">{t('手动（固定 ULA）')}</option>
                <option value="automatic">{t('自动（RA / SLAAC）')}</option>
              </select>
            </label>
            <p className="muted">{t(ipv6Automatic
              ? 'OpenSurge 会向同一 LAN 发布 RA/SLAAC/RDNSS，手机、电视等设备无需手动填写 IPv6。'
              : '仅手动配置 OpenSurge ULA 的设备进入 IPv6 接管，不向整个局域网广播 RA。')}</p>
          </article>
        </div>

        {ipv6Enabled && <>
          {ipv6Automatic ? <div className="source-import-grid">
            <article className="source-import-card qnap-ipv6-values">
              <span><strong>{t('自动下发前缀')}</strong><code>fdfe:dcba:9878::/64</code></span>
              <span><strong>{t('地址配置')}</strong><code>RA / SLAAC</code></span>
              <span><strong>{t('DNS 下发')}</strong><code>RDNSS</code></span>
              <span><strong>{t('终端设置')}</strong><code>{t('无需手动配置')}</code></span>
            </article>
            <article className="source-import-card">
              <strong>{t('自动模式会影响整个同层局域网')}</strong>
              <p>{t('连接到该 LAN 且接受 IPv6 RA 的设备都会获得 OpenSurge ULA，并把 OpenSurge 作为 IPv6 默认路由。')}</p>
              <p className="muted">{t('开启前必须关闭主路由的 IPv6 RA / DHCPv6，或用 RA Guard 确保不存在竞争默认路由；否则设备可能绕过 OpenSurge。')}</p>
            </article>
          </div> : <div className="source-import-grid">
            <article className="source-import-card qnap-ipv6-values">
              <span><strong>{t('客户端前缀')}</strong><code>fdfe:dcba:9878::/64</code></span>
              <span><strong>{t('IPv6 网关 / DNS')}</strong><code>fdfe:dcba:9878::1</code></span>
              <label>
                <span>{t('按设备 IPv4 生成固定 IPv6')}</span>
                <input value={ipv6ClientIPv4} onChange={event => setIPv6ClientIPv4(event.target.value)} placeholder="192.168.2.101" inputMode="decimal" />
              </label>
              <span><strong>{t('设备 IPv6')}</strong><code>{clientIPv6 || '—'}</code></span>
            </article>
            <article className="source-import-card">
              <strong>{t('手动模式只接管指定设备')}</strong>
              <p>{t('不会发送 RA。只有手动配置固定 ULA、OpenSurge IPv6 网关和 DNS 的客户端会进入 IPv6 接管。')}</p>
              <p className="muted">{t('固定 ULA 由设备 IPv4 推导，可继续用于 IPv6 按设备策略匹配。')}</p>
            </article>
          </div>}

          <label className="sidebar-switch">
            <input type="checkbox" checked={draft.transparent.ipv6_shared_l2_ready ?? false} onChange={event => patch({ transparent: { ...draft.transparent, ipv6_shared_l2_ready: event.target.checked } })} />
            <span>
              <strong>{t(ipv6Automatic ? '我已消除主路由的竞争 IPv6 默认路由' : '我已完成目标客户端 IPv6 配置')}</strong>
              <small>{t(ipv6Automatic
                ? '确认主路由不再向这个 LAN 发布可用的 IPv6 默认路由后再保存。'
                : '确认目标客户端只使用 OpenSurge 的 IPv6 默认路由和 DNS 后再保存。')}</small>
            </span>
          </label>

          {ipv6Automatic && <div className="notice warn">
            <strong>{t('自动分配模式的设备策略限制')}</strong>
            <p>{t('QNAP 原生 Linux TUN 在三层接管后无法保留客户端源 MAC，SLAAC 地址也不由固定 IPv4 推导。因此自动模式可可靠应用全局 IPv6 规则，但目前不保证命中每台设备的独立 IPv6 策略；需要精确设备策略时请选择手动固定 ULA。')}</p>
          </div>}
        </>}

        <div className="notice">
          <strong>{t('只修改 OpenSurge 容器内 IPv6')}</strong>
          <p>{t('QNET、QTS 宿主默认路由和主路由配置不会被 OpenSurge 自动修改。切换到自动分配前，主路由 RA / DHCPv6 需要由你在路由器侧关闭。')}</p>
        </div>
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

function qnapClientIPv6(ipv4: string): string {
  const parts = ipv4.trim().split('.').map(value => Number(value))
  if (parts.length !== 4 || parts.some(value => !Number.isInteger(value) || value < 0 || value > 255)) return ''
  const upper = ((parts[0] << 8) | parts[1]).toString(16)
  const lower = ((parts[2] << 8) | parts[3]).toString(16)
  return `fdfe:dcba:9878:0:1:0:${upper}:${lower}`
}
