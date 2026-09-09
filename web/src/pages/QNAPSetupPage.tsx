import { useEffect, useMemo, useState } from 'react'
import { PageHeader, SectionTitle } from '../components/Common'
import './QNAPSetupPage.css'

type HostInterface = {
  name: string
  mtu: number
  flags: string[]
  addresses: string[]
}

type QNAPNetwork = {
  parent_interface: string
  ipv4: string
  subnet: string
  gateway: string
}

type QNAPSetupStatus = {
  schema_version: number
  host: {
    schema_version: number
    interfaces: HostInterface[]
    network?: QNAPNetwork
    connected: boolean
  }
  container_interface?: string
  gateway?: {
    ipv4: string
    subnet: string
    gateway: string
    interface: string
  }
}

type Draft = QNAPNetwork

export function QNAPSetupPage({ onContinue }: { onContinue: () => void }) {
  const [status, setStatus] = useState<QNAPSetupStatus | null>(null)
  const [draft, setDraft] = useState<Draft>({ parent_interface: '', ipv4: '', subnet: '', gateway: '' })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const response = await fetch('/api/qnap/setup', { credentials: 'same-origin' })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload?.error?.message || `HTTP ${response.status}`)
      const next = payload as QNAPSetupStatus
      setStatus(next)
      const source = next.host.network ?? (next.gateway ? {
        parent_interface: '',
        ipv4: next.gateway.ipv4,
        subnet: next.gateway.subnet,
        gateway: next.gateway.gateway,
      } : null)
      if (source) setDraft(current => ({
        parent_interface: source.parent_interface || current.parent_interface,
        ipv4: source.ipv4 || current.ipv4,
        subnet: source.subnet || current.subnet,
        gateway: source.gateway || current.gateway,
      }))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [])

  const selected = useMemo(
    () => status?.host.interfaces.find(candidate => candidate.name === draft.parent_interface),
    [draft.parent_interface, status],
  )

  const apply = async () => {
    setSaving(true)
    setError('')
    setMessage('')
    try {
      const response = await fetch('/api/qnap/setup', {
        method: 'PUT',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(draft),
      })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload?.error?.message || `HTTP ${response.status}`)
      const next = payload as QNAPSetupStatus
      setStatus(next)
      setMessage(`QNET 已连接。容器数据接口：${next.container_interface || '已自动识别'}。网络配置已写入 OpenSurge。`)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setSaving(false)
    }
  }

  return <>
    <PageHeader
      eyebrow="QNAP SETUP"
      title="QNAP 首次设置"
      description="先通过 NAS 管理地址打开本页面，再在这里选择 QNAP 网卡和 OpenSurge 的 LAN 地址。无需 .env，也无需手工编辑 YAML。"
    />

    {error && <div className="notice warn" role="alert">{error}</div>}
    {message && <div className="notice ok-notice" role="status">{message}</div>}

    <section className="section qnap-setup-section">
      <SectionTitle title="1. 选择 QNAP 宿主网卡" subtitle="这是 QNET 绑定的 NAS 物理网卡 / bridge，不是容器内部的 eth0/eth1。" />
      {loading && <div className="notice">正在读取 QNAP 网卡…</div>}
      {!loading && status && <div className="qnap-interface-grid">
        {status.host.interfaces.map(item => <button
          key={item.name}
          type="button"
          className={`qnap-interface-card ${draft.parent_interface === item.name ? 'active' : ''}`}
          onClick={() => setDraft(current => ({ ...current, parent_interface: item.name }))}
        >
          <span className="qnap-interface-name">{item.name}</span>
          <span className="qnap-interface-addresses">{item.addresses.length ? item.addresses.join(' · ') : '无地址'}</span>
          <span className="qnap-interface-meta">MTU {item.mtu} · {item.flags.join(', ') || 'down'}</span>
        </button>)}
      </div>}
      {selected && <div className="notice">已选择 <strong>{selected.name}</strong>。请和 QTS「网络与虚拟交换机」中的物理端口对应关系核对后再应用。</div>}
    </section>

    <section className="section qnap-setup-section">
      <SectionTitle title="2. 设置 OpenSurge LAN 地址" subtitle="这些值由 Web 保存，并同时用于创建 OpenSurge 专属 QNET 和生成网关配置。" />
      <div className="qnap-setup-form">
        <label>
          <span>OpenSurge 独立 IPv4</span>
          <input value={draft.ipv4} placeholder="192.168.2.241" onChange={event => setDraft(current => ({ ...current, ipv4: event.target.value }))} />
          <small>请选择当前 LAN 内未占用的固定地址，最好位于 DHCP 动态池之外。</small>
        </label>
        <label>
          <span>LAN CIDR</span>
          <input value={draft.subnet} placeholder="192.168.2.0/24" onChange={event => setDraft(current => ({ ...current, subnet: event.target.value }))} />
          <small>例如 192.168.2.0/24。必须与所选 QNAP 网卡所在 LAN 一致。</small>
        </label>
        <label>
          <span>主路由 IPv4</span>
          <input value={draft.gateway} placeholder="192.168.2.1" onChange={event => setDraft(current => ({ ...current, gateway: event.target.value }))} />
          <small>OpenSurge 的 DIRECT 流量和默认上游将交给这个路由器。</small>
        </label>
      </div>
      <div className="qnap-setup-actions">
        <button type="button" onClick={() => void load()} disabled={loading || saving}>重新检测</button>
        <button
          type="button"
          className="primary"
          disabled={saving || !draft.parent_interface || !draft.ipv4 || !draft.subnet || !draft.gateway}
          onClick={() => void apply()}
        >{saving ? '正在创建 QNET…' : status?.host.connected ? '应用网络修改' : '创建并连接 QNET'}</button>
      </div>
    </section>

    <section className="section qnap-setup-section">
      <SectionTitle title="3. 持久化与后续配置" subtitle="Docker 持久化不再需要用户选择路径。" />
      <div className="qnap-storage-card">
        <strong>opensurge-data</strong>
        <p>配置、管理员账户、订阅/Profile、Provider、状态、日志与故障恢复 journal 都保存在 Docker named volume 中。删除或重建容器不会清空该卷。</p>
      </div>
      {status?.host.connected && <div className="qnap-setup-actions">
        <button type="button" className="primary" onClick={onContinue}>继续配置代理、DNS 与网关</button>
      </div>}
    </section>
  </>
}
