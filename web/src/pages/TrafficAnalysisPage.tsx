import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'
import { PageHeader, SectionTitle } from '../components/Common'
import type { Diagnostics, ProfileOverlay, ProxyGroup, Source } from '../types'
import { t } from '../i18n'
import {
  aggregateDomains,
  buildOverrideRule,
  connectionSearchText,
  mergeTrafficHistory,
  normalizeTrafficConnection,
  type RuleMatchMode,
  type TrafficRecord,
} from './trafficAnalysis'
import './TrafficAnalysisPage.css'

const trafficHistoryStorageKey = 'opensurge-traffic-analysis-history-v1'
const trafficPollIntervalMs = 1200
const qnapBuild = import.meta.env.VITE_OPENSURGE_TARGET === 'qnap'

export function TrafficAnalysisPage() {
  const [details, setDetails] = useState<Diagnostics | null>(null)
  const [history, setHistory] = useState<TrafficRecord[]>(loadTrafficHistory)
  const [overlay, setOverlay] = useState<ProfileOverlay | null>(null)
  const [groups, setGroups] = useState<ProxyGroup[]>([])
  const [sources, setSources] = useState<Source[]>([])
  const [sourceRevision, setSourceRevision] = useState('')
  const [selected, setSelected] = useState<TrafficRecord | null>(null)
  const [search, setSearch] = useState('')
  const [paused, setPaused] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const lastPersistedAt = useRef(0)
  const requestRunning = useRef(false)

  const loadManagementState = async () => {
    const [nextOverlay, policyResponse, sourceResponse] = await Promise.all([
      api.profileOverlay(),
      api.policies().catch(() => ({ groups: [] as ProxyGroup[] })),
      api.sources().catch(() => ({ revision: '', sources: [] as Source[] })),
    ])
    setOverlay(nextOverlay)
    setGroups(policyResponse.groups ?? [])
    setSources(sourceResponse.sources ?? [])
    setSourceRevision(sourceResponse.revision ?? '')
  }

  const refreshTraffic = async () => {
    if (requestRunning.current) return
    requestRunning.current = true
    try {
      const next = await api.diagnostics()
      const now = new Date().toISOString()
      const current = (next.connections.connections ?? []).map(connection => normalizeTrafficConnection(connection, now))
      setDetails(next)
      setHistory(previous => {
        const merged = mergeTrafficHistory(previous, current)
        if (Date.now() - lastPersistedAt.current > 10_000) {
          persistTrafficHistory(merged)
          lastPersistedAt.current = Date.now()
        }
        return merged
      })
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setLoading(false)
      requestRunning.current = false
    }
  }

  useEffect(() => {
    void Promise.all([
      refreshTraffic(),
      loadManagementState().catch(cause => setError(cause instanceof Error ? cause.message : String(cause))),
    ])
  }, [])

  useEffect(() => {
    if (paused) return
    const timer = window.setInterval(() => void refreshTraffic(), trafficPollIntervalMs)
    return () => window.clearInterval(timer)
  }, [paused])

  useEffect(() => () => persistTrafficHistory(history), [history])

  const currentConnections = useMemo(() => {
    const now = new Date().toISOString()
    const records = (details?.connections.connections ?? []).map(connection => normalizeTrafficConnection(connection, now))
    const needle = search.trim().toLowerCase()
    return needle ? records.filter(record => connectionSearchText(record).includes(needle)) : records
  }, [details, search])

  const domains = useMemo(() => aggregateDomains(history), [history])
  const suspiciousDomains = useMemo(() => domains.filter(domain => domain.needs_review), [domains])
  const logHints = useMemo(() => {
    const pattern = /(error|warn|timeout|timed out|refused|reset|failed|failure|unreachable)/i
    return Object.entries(details?.logs ?? {})
      .flatMap(([source, lines]) => lines.map(line => ({ source, line })))
      .filter(item => pattern.test(item.line))
      .slice(-24)
      .reverse()
  }, [details])
  const overlayRules = overlay?.document.rules.prepend ?? []
  const policyOptions = useMemo(() => uniqueStrings(['DIRECT', 'REJECT', ...groups.map(group => group.name)]), [groups])
  const totalRateBytes = (details?.connections.upload_total ?? 0) + (details?.connections.download_total ?? 0)

  const clearHistory = () => {
    if (!window.confirm(t('清空本浏览器保存的流量观察记录？当前活动连接不会被关闭。'))) return
    setHistory([])
    window.localStorage.removeItem(trafficHistoryStorageKey)
    setMessage(t('本地观察记录已清空。'))
  }

  const openDomain = (key: string) => {
    const record = history.find(item => (item.host || item.destination_ip).toLowerCase() === key)
    if (record) setSelected(record)
  }

  const saveOverride = async (rule: string) => {
    if (busy) return
    const normalized = rule.trim()
    if (!validOverrideRule(normalized)) {
      setError(t('规则格式无效；请输入一条不含换行的 Mihomo 规则。'))
      return
    }
    setBusy(true)
    setError('')
    setMessage('')
    let saved = false
    try {
      const fresh = await api.profileOverlay()
      const document = structuredClone(fresh.document)
      document.enabled = true
      document.rules.prepend = [normalized, ...document.rules.prepend.filter(item => item.trim() !== normalized)]
      const nextOverlay = await api.saveProfileOverlayDocument(document, fresh.revision)
      saved = true
      setOverlay(nextOverlay)
      const application = await applyOverlayToActiveSource()
      setMessage(application
        ? t('高优先级规则已保存，并已应用到当前运行配置。')
        : t('高优先级规则已保存到高级网络设置；当前没有已选择的订阅来源，将在下一次应用或启动时生效。'))
      setSelected(null)
      await loadManagementState()
      await refreshTraffic()
    } catch (cause) {
      const detail = cause instanceof Error ? cause.message : String(cause)
      setError(saved ? t('规则已经保存，但自动应用失败：{{error}}', { error: detail }) : detail)
    } finally {
      setBusy(false)
    }
  }

  const removeOverride = async (rule: string) => {
    if (busy || !window.confirm(t('删除这条高优先级覆盖规则？'))) return
    setBusy(true)
    setError('')
    setMessage('')
    let saved = false
    try {
      const fresh = await api.profileOverlay()
      const document = structuredClone(fresh.document)
      document.rules.prepend = document.rules.prepend.filter(item => item !== rule)
      const nextOverlay = await api.saveProfileOverlayDocument(document, fresh.revision)
      saved = true
      setOverlay(nextOverlay)
      const application = await applyOverlayToActiveSource()
      setMessage(application ? t('覆盖规则已删除并重新应用当前配置。') : t('覆盖规则已删除；下次应用或启动时生效。'))
      await loadManagementState()
    } catch (cause) {
      const detail = cause instanceof Error ? cause.message : String(cause)
      setError(saved ? t('规则已经删除，但自动应用失败：{{error}}', { error: detail }) : detail)
    } finally {
      setBusy(false)
    }
  }

  const applyOverlayToActiveSource = async () => {
    const response = await api.sources()
    setSources(response.sources ?? [])
    setSourceRevision(response.revision ?? '')
    const active = response.sources?.find(source => source.applied) ?? response.sources?.find(source => source.desired)
    if (!active || !response.revision) return false
    await api.applySource(active.id, response.revision)
    return true
  }

  const copyAIInstructions = async () => {
    const text = [
      `OpenSurge · ${t('AI 规则分析')}`,
      `1. GET /api/remote/v1/diagnostics — ${t('当前连接、规则命中、流量元数据和脱敏日志')}`,
      `2. GET /api/remote/v1/profile-overlay — ${t('读取或写入 rules.prepend 高优先级规则')}`,
      `3. ${t('不会修改订阅原规则')}；先给出 evidence → current match → suggested override.`,
      `4. ${t('高优先级覆盖规则')} → profile-overlay.document.rules.prepend; save with If-Match after user confirmation.`,
      `5. ${t('读取来源；规则确认后可重新应用当前来源')}`,
    ].join('\n')
    try {
      await copyText(text)
      setMessage(t('AI 分析说明已复制。'))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  return <>
    <PageHeader
      eyebrow={t('流量分析')}
      title={t('流量与规则分析')}
      description={t('滚动观察当前连接、域名、规则命中和近期异常日志；从真实流量直接建立高优先级覆盖规则。')}
      action={<button type="button" onClick={() => setPaused(value => !value)}>{t(paused ? '继续实时刷新' : '暂停实时刷新')}</button>}
    />

    {error && <div className="notice warn" role="alert"><strong>{t('流量分析暂不可用')}</strong><p>{error}</p></div>}
    {message && <div className="ok-notice" role="status"><strong>{t('操作完成')}</strong><p>{message}</p></div>}
    {details?.connection_error && <div className="notice warn"><strong>{t('连接数据暂不可用')}</strong><p>{details.connection_error}</p></div>}

    <section className="traffic-analysis-summary" aria-label={t('流量分析概览')}>
      <article><small>{t('当前连接')}</small><strong>{details?.connections.connections?.length ?? 0}</strong><span>{paused ? t('已暂停刷新') : t('约 1.2 秒刷新')}</span></article>
      <article><small>{t('观察到的域名')}</small><strong>{domains.length}</strong><span>{t('本浏览器最多保留 7 天 / 3000 条连接')}</span></article>
      <article><small>{t('疑似规则异常')}</small><strong>{suspiciousDomains.length}</strong><span>{t('仅提示复核，不自动判定规则错误')}</span></article>
      <article><small>{t('累计流量')}</small><strong>{formatBytes(totalRateBytes)}</strong><span>↑ {formatBytes(details?.connections.upload_total ?? 0)} · ↓ {formatBytes(details?.connections.download_total ?? 0)}</span></article>
    </section>

    <section className="section">
      <SectionTitle title={t('实时网络活动')} subtitle={t('像网络活动面板一样持续滚动；点击任意连接可直接创建修正规则')} />
      <div className="traffic-analysis-toolbar">
        <input value={search} onChange={event => setSearch(event.target.value)} placeholder={t('搜索域名、IP、端口、规则或出口…')} />
        <button type="button" onClick={() => void refreshTraffic()} disabled={loading}>{t('立即刷新')}</button>
        <button type="button" onClick={clearHistory}>{t('清空观察历史')}</button>
      </div>
      <div className="traffic-live-list">
        <div className="traffic-live-head"><span>{t('目标')}</span><span>{t('来源')}</span><span>{t('协议')}</span><span>{t('命中规则 / 出口')}</span><span>{t('流量')}</span></div>
        {currentConnections.map(connection => <button className="traffic-live-row" type="button" key={connection.key} onClick={() => setSelected(connection)}>
          <strong title={connection.host || connection.destination_ip}>{connection.host || connection.destination_ip || '—'}<small>{connection.destination_port ? `:${connection.destination_port}` : ''}</small></strong>
          <span title={connection.source_ip}>{connection.source_ip || '—'}{connection.source_port ? `:${connection.source_port}` : ''}</span>
          <span>{[connection.network, connection.connection_type].filter(Boolean).join(' / ') || '—'}</span>
          <span className="traffic-route-chain"><code>{[connection.rule, connection.rule_payload].filter(Boolean).join(':') || 'MATCH'}</code><small title={connection.chains.join(' → ')}>{connection.chains.join(' → ') || '—'}</small></span>
          <span>↑{formatBytes(connection.upload)}<br />↓{formatBytes(connection.download)}</span>
        </button>)}
        {!currentConnections.length && <div className="empty">{loading ? t('正在读取当前连接…') : t('当前没有符合条件的活动连接')}</div>}
      </div>
    </section>

    <section className="section">
      <SectionTitle title={t('域名与规则复核')} subtitle={t('按最近观察到的连接聚合；同一目标命中多个规则或出口时标为疑似异常')} />
      <div className="traffic-domain-list">
        {domains.slice(0, 80).map(domain => <div className={`traffic-domain-row ${domain.needs_review ? 'review' : ''}`} key={domain.key}>
          <button className="linklike" type="button" onClick={() => openDomain(domain.key)}><strong>{domain.host}</strong><small>{domain.needs_review ? `⚠ ${t('建议复核')}` : t('规则路径稳定')}</small></button>
          <span><strong>{domain.connections}</strong><small>{t('连接')} · {formatBytes(domain.upload + domain.download)}</small></span>
          <span><small>{domain.rules.slice(0, 2).join(' · ') || 'MATCH'}</small><br /><small>{domain.chains.slice(0, 2).join(' / ') || '—'}</small></span>
          <button type="button" onClick={() => openDomain(domain.key)}>{t('修改规则')}</button>
        </div>)}
        {!domains.length && <div className="empty">{t('还没有可分析的域名记录。保持此页面打开一段时间即可形成观察历史。')}</div>}
      </div>
    </section>

    <section className="section">
      <SectionTitle title={t('高优先级覆盖规则')} subtitle={t('写入“高级：全局附加配置”的 rules.prepend，固定排在订阅规则之前')} />
      <div className="traffic-override-list">
        {overlayRules.map(rule => <div className="traffic-override-row" key={rule}><code>{rule}</code><button type="button" disabled={busy} onClick={() => void removeOverride(rule)}>{t('删除')}</button></div>)}
        {!overlayRules.length && <div className="empty">{t('暂无高优先级覆盖规则；可以直接点击上方连接创建。')}</div>}
      </div>
      <p className="muted">{overlay?.document.enabled ? t(overlay.applied ? '附加配置当前已应用。' : overlay.desired ? '附加配置将在下次启动生效。' : '附加配置已启用，但当前是待应用草稿。') : t('创建第一条规则时会自动启用全局附加配置。')}</p>
    </section>

    <section className="section">
      <SectionTitle title={t('近期异常日志')} subtitle={t('从诊断日志中筛选 error、warn、timeout、reset 等信号；仅作为规则分析证据')} />
      <div className="traffic-log-hints">
        {logHints.map((item, index) => <div className="traffic-log-row" key={`${item.source}-${index}`}><small>{item.source}</small><span>{item.line}</span></div>)}
        {!logHints.length && <div className="empty">{t('近期日志中没有明显异常关键词。')}</div>}
      </div>
    </section>

    {qnapBuild && <section className="section">
      <SectionTitle title={t('AI 规则分析')} subtitle={t('复用现有局域网管理 Token；AI 读取证据后把确认的修正规则写入同一个高优先级附加层')} />
      <div className="traffic-ai-flow">
        <span><strong>GET</strong> <code>/api/remote/v1/diagnostics</code> — {t('当前连接、规则命中、流量元数据和脱敏日志')}</span>
        <span><strong>GET / PUT</strong> <code>/api/remote/v1/profile-overlay</code> — {t('读取或写入 rules.prepend 高优先级规则')}</span>
        <span><strong>GET</strong> <code>/api/remote/v1/policies</code> — {t('读取当前可用策略组')}</span>
        <span><strong>GET / POST</strong> <code>/api/remote/v1/sources</code> — {t('读取来源；规则确认后可重新应用当前来源')}</span>
        <div className="source-actions"><button type="button" onClick={() => void copyAIInstructions()}>{t('复制 AI 分析说明')}</button></div>
      </div>
    </section>}

    {selected && <RuleEditorDialog
      connection={selected}
      policyOptions={policyOptions}
      existingRules={overlayRules}
      busy={busy}
      onClose={() => setSelected(null)}
      onSave={saveOverride}
    />}
  </>
}

function RuleEditorDialog({
  connection,
  policyOptions,
  existingRules,
  busy,
  onClose,
  onSave,
}: {
  connection: TrafficRecord
  policyOptions: string[]
  existingRules: string[]
  busy: boolean
  onClose: () => void
  onSave: (rule: string) => Promise<void>
}) {
  const availableModes: RuleMatchMode[] = connection.host ? ['domain', 'domain-suffix', ...(connection.destination_ip && !connection.destination_ip.includes(':') ? ['ip' as const] : [])] : ['ip']
  const [mode, setMode] = useState<RuleMatchMode>(availableModes[0])
  const [policy, setPolicy] = useState(() => suggestedPolicy(connection, policyOptions))
  const [rule, setRule] = useState(() => buildOverrideRule(connection, availableModes[0], suggestedPolicy(connection, policyOptions)))

  useEffect(() => {
    setRule(buildOverrideRule(connection, mode, policy))
  }, [connection.key, mode, policy])

  const duplicate = existingRules.some(item => item.trim() === rule.trim())

  return <div className="traffic-rule-dialog-backdrop" role="presentation" onMouseDown={event => { if (event.target === event.currentTarget) onClose() }}>
    <aside className="traffic-rule-dialog" role="dialog" aria-modal="true" aria-label={t('修改连接规则')}>
      <header><div><small>{t('来自实时网络活动')}</small><h2>{t('修改连接规则')}</h2><p>{connection.host || connection.destination_ip}</p></div><button type="button" onClick={onClose} aria-label={t('关闭')}>×</button></header>

      <div className="traffic-rule-current">
        <div><small>{t('当前命中')}</small><strong>{[connection.rule, connection.rule_payload].filter(Boolean).join(':') || 'MATCH'}</strong></div>
        <div><small>{t('当前出口链')}</small><strong>{connection.chains.join(' → ') || '—'}</strong></div>
        <div><small>{t('来源')}</small><strong>{connection.source_ip || '—'}{connection.source_port ? `:${connection.source_port}` : ''}</strong></div>
        <div><small>{t('目标')}</small><strong>{connection.destination_ip || '—'}{connection.destination_port ? `:${connection.destination_port}` : ''}</strong></div>
      </div>

      <div className="notice">
        <strong>{t('不会修改订阅原规则')}</strong>
        <p>{t('新规则写入全局附加配置的 rules.prepend，并固定放在订阅规则之前。删除该覆盖规则即可恢复原始行为。')}</p>
      </div>

      <div className="traffic-rule-form">
        <label>{t('匹配方式')}<select value={mode} onChange={event => setMode(event.target.value as RuleMatchMode)}>{availableModes.map(value => <option key={value} value={value}>{t(matchModeLabel(value))}</option>)}</select></label>
        <label>{t('目标策略')}<select value={policy} onChange={event => setPolicy(event.target.value)}>{policyOptions.map(value => <option key={value} value={value}>{value}</option>)}</select></label>
        <label>{t('高优先级规则')}<input value={rule} onChange={event => setRule(event.target.value)} spellCheck={false} /></label>
        {duplicate && <div className="notice warn"><strong>{t('规则已存在')}</strong><p>{t('保存会把相同规则移动到高优先级列表最前面，不会重复创建。')}</p></div>}
        <div className="source-actions"><button type="button" onClick={onClose} disabled={busy}>{t('取消')}</button><button className="primary" type="button" disabled={busy || !validOverrideRule(rule)} onClick={() => void onSave(rule)}>{busy ? t('正在保存并应用…') : t('添加高优先级覆盖规则')}</button></div>
      </div>
    </aside>
  </div>
}

function suggestedPolicy(connection: TrafficRecord, options: string[]): string {
  const route = `${connection.rule} ${connection.rule_payload} ${connection.chains.join(' ')}`.toUpperCase()
  if (route.includes('DIRECT')) {
    return options.find(option => option !== 'DIRECT' && option !== 'REJECT') ?? 'DIRECT'
  }
  return options.includes('DIRECT') ? 'DIRECT' : options[0] ?? 'DIRECT'
}

function matchModeLabel(mode: RuleMatchMode): string {
  if (mode === 'domain') return '精确域名'
  if (mode === 'domain-suffix') return '域名后缀'
  return '目标 IPv4'
}

function validOverrideRule(rule: string): boolean {
  return Boolean(rule && !rule.includes('\n') && rule.split(',').length >= 3)
}

function uniqueStrings(values: string[]): string[] {
  return [...new Set(values.map(value => value.trim()).filter(Boolean))]
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  const scaled = value / 1024 ** index
  return `${scaled >= 100 || index === 0 ? scaled.toFixed(0) : scaled.toFixed(1)} ${units[index]}`
}

function loadTrafficHistory(): TrafficRecord[] {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(trafficHistoryStorageKey) ?? '[]') as TrafficRecord[]
    return Array.isArray(parsed) ? mergeTrafficHistory(parsed, []) : []
  } catch {
    return []
  }
}

function persistTrafficHistory(history: TrafficRecord[]) {
  try {
    window.localStorage.setItem(trafficHistoryStorageKey, JSON.stringify(history))
  } catch { /* browser storage can be unavailable or full */ }
}

async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = value
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  document.execCommand('copy')
  textarea.remove()
}
