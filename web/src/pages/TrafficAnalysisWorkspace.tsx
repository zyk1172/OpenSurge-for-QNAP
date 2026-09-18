import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'
import { PageHeader, SectionTitle } from '../components/Common'
import type { Diagnostics, ProfileOverlay, ProxyGroup, ProxyHealthEntry } from '../types'
import { t } from '../i18n'
import {
  aggregateRuleHits,
  aggregateTargets,
  aggregateTrafficRoutes,
  buildOverrideRule,
  buildRouteExplanation,
  classifyTrafficRoute,
  connectionSearchText,
  detectTrafficIssues,
  mergeTrafficHistory,
  normalizeTrafficConnection,
  recordMatchesFilter,
  ruleLabel,
  targetLabel,
  type RuleMatchMode,
  type TrafficIssue,
  type TrafficRecord,
  type TrafficRouteKind,
  type TrafficViewFilter,
} from './trafficAnalysis'
import './TrafficAnalysisPage.css'

const trafficHistoryStorageKey = 'opensurge-traffic-analysis-history-v1'
const trafficPollIntervalMs = 1200

export function TrafficAnalysisPage() {
  const [details, setDetails] = useState<Diagnostics | null>(null)
  const [history, setHistory] = useState<TrafficRecord[]>(loadTrafficHistory)
  const [overlay, setOverlay] = useState<ProfileOverlay | null>(null)
  const [groups, setGroups] = useState<ProxyGroup[]>([])
  const [health, setHealth] = useState<ProxyHealthEntry[]>([])
  const [selected, setSelected] = useState<TrafficRecord | null>(null)
  const [filter, setFilter] = useState<TrafficViewFilter>('focus')
  const [search, setSearch] = useState('')
  const [paused, setPaused] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const lastPersistedAt = useRef(0)
  const requestRunning = useRef(false)

  const loadManagementState = async () => {
    const [nextOverlay, policyResponse, healthResponse] = await Promise.all([
      api.profileOverlay(),
      api.policies().catch(() => ({ groups: [] as ProxyGroup[] })),
      api.proxyHealth().catch(() => ({ schema_version: 1, test_url: '', proxies: [] as ProxyHealthEntry[] })),
    ])
    setOverlay(nextOverlay)
    setGroups(policyResponse.groups ?? [])
    setHealth(healthResponse.proxies ?? [])
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

  const liveConnections = useMemo(() => {
    const now = new Date().toISOString()
    return (details?.connections.connections ?? []).map(connection => normalizeTrafficConnection(connection, now))
  }, [details])

  const issues = useMemo(() => detectTrafficIssues(history, liveConnections, health), [history, liveConnections, health])
  const issueKeys = useMemo(() => new Set(issues.flatMap(issue => issue.related_keys)), [issues])

  const visibleConnections = useMemo(() => {
    const needle = search.trim().toLowerCase()
    return liveConnections
      .filter(record => recordMatchesFilter(record, filter, issues))
      .filter(record => !needle || connectionSearchText(record).includes(needle))
  }, [liveConnections, filter, issues, search])

  const filterCounts = useMemo(() => ({
    focus: liveConnections.filter(record => recordMatchesFilter(record, 'focus', issues)).length,
    proxy: liveConnections.filter(record => classifyTrafficRoute(record) === 'proxy').length,
    direct: liveConnections.filter(record => classifyTrafficRoute(record) === 'direct').length,
    reject: liveConnections.filter(record => classifyTrafficRoute(record) === 'reject').length,
    all: liveConnections.length,
  }), [liveConnections, issues])

  const routeSummary = useMemo(() => aggregateTrafficRoutes(history), [history])
  const ruleHits = useMemo(() => aggregateRuleHits(history), [history])
  const targets = useMemo(() => aggregateTargets(history), [history])
  const logHints = useMemo(() => {
    const pattern = /(error|warn|timeout|timed out|refused|reset|failed|failure|unreachable)/i
    return Object.entries(details?.logs ?? {})
      .flatMap(([source, lines]) => lines.map(line => ({ source, line })))
      .filter(item => pattern.test(item.line))
      .slice(-16)
      .reverse()
  }, [details])
  const overlayRules = overlay?.document.rules.prepend ?? []
  const policyOptions = useMemo(() => uniqueStrings(['DIRECT', 'REJECT', ...groups.map(group => group.name)]), [groups])
  const proxyConnections = filterCounts.proxy
  const directConnections = filterCounts.direct

  const clearHistory = () => {
    if (!window.confirm(t('清空本浏览器保存的流量观察记录？当前活动连接不会被关闭。'))) return
    setHistory([])
    window.localStorage.removeItem(trafficHistoryStorageKey)
    setMessage(t('本地观察记录已清空。'))
  }

  const openIssue = (issue: TrafficIssue) => {
    const record = liveConnections.find(item => issue.related_keys.includes(item.key))
      ?? history.find(item => issue.related_keys.includes(item.key))
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
    const active = response.sources?.find(source => source.applied) ?? response.sources?.find(source => source.desired)
    if (!active || !response.revision) return false
    await api.applySource(active.id, response.revision)
    return true
  }

  return <>
    <PageHeader
      eyebrow={t('流量分析')}
      title={t('连接与分流分析')}
      description={t('观察实时连接，解释“规则 → 策略组 → 实际出口”，并只把有明确证据的问题放进需要处理。正常 DIRECT、DNS 与局域网流量默认降噪。')}
      action={<button type="button" onClick={() => setPaused(value => !value)}>{t(paused ? '继续实时刷新' : '暂停实时刷新')}</button>}
    />

    {error && <div className="notice warn" role="alert"><strong>{t('流量分析暂不可用')}</strong><p>{error}</p></div>}
    {message && <div className="ok-notice" role="status"><strong>{t('操作完成')}</strong><p>{message}</p></div>}
    {details?.connection_error && <div className="notice warn"><strong>{t('连接数据暂不可用')}</strong><p>{details.connection_error}</p></div>}

    <section className="traffic-v3-summary" aria-label={t('流量分析概览')}>
      <SummaryCard label={t('当前连接')} value={liveConnections.length} note={paused ? t('已暂停刷新') : t('约 1.2 秒刷新')} tone="neutral" />
      <SummaryCard label={t('代理连接')} value={proxyConnections} note={t('实际链进入代理出口')} tone="proxy" />
      <SummaryCard label={t('直连连接')} value={directConnections} note={t('正常 DIRECT 不进入问题列表')} tone="direct" />
      <SummaryCard label={t('需要处理')} value={issues.length} note={issues.length ? t('仅显示有明确证据的问题') : t('当前未发现需要处理的分流问题')} tone={issues.length ? 'issue' : 'ok'} />
    </section>

    <section className="section traffic-v3-live-section">
      <div className="traffic-v3-section-head">
        <SectionTitle title={t('实时连接')} subtitle={t('默认“关注”隐藏普通 DIRECT、DNS 与 LAN；需要排查时仍可切换到直连或全部查看完整证据。')} />
        <span className="traffic-v3-live-state"><i className={paused ? 'paused' : ''} />{t(paused ? '已暂停' : '实时')}</span>
      </div>

      <div className="traffic-v3-filterbar" role="toolbar" aria-label={t('连接筛选')}>
        <div className="traffic-v3-tabs">
          {(['focus', 'proxy', 'direct', 'reject', 'all'] as TrafficViewFilter[]).map(value => <button
            key={value}
            type="button"
            className={filter === value ? 'active' : ''}
            aria-pressed={filter === value}
            onClick={() => setFilter(value)}
          >{t(filterLabel(value))}<span>{filterCounts[value]}</span></button>)}
        </div>
        <div className="traffic-v3-search-actions">
          <input value={search} onChange={event => setSearch(event.target.value)} placeholder={t('搜索域名、IP、进程、规则或出口…')} />
          <button type="button" onClick={() => void refreshTraffic()} disabled={loading}>{t('刷新')}</button>
          <button type="button" onClick={clearHistory}>{t('清空历史')}</button>
        </div>
      </div>

      <div className="traffic-v3-connection-list">
        <div className="traffic-v3-connection-head"><span>{t('目标')}</span><span>{t('来源')}</span><span>{t('分流解释')}</span><span>{t('流量')}</span></div>
        {visibleConnections.map(connection => {
          const route = classifyTrafficRoute(connection)
          const hasIssue = issueKeys.has(connection.key)
          return <button className={`traffic-v3-connection-row route-${route} ${hasIssue ? 'has-issue' : ''}`} type="button" key={connection.key} onClick={() => setSelected(connection)}>
            <span className="traffic-v3-target">
              <span className={`traffic-v3-route-badge route-${route}`}>{t(routeLabel(route))}</span>
              <strong title={targetLabel(connection)}>{targetLabel(connection)}</strong>
              <small>{connection.destination_port ? `:${connection.destination_port}` : ''}{connection.network ? ` · ${connection.network.toUpperCase()}` : ''}</small>
            </span>
            <span className="traffic-v3-source" title={connection.source_ip}><strong>{connection.source_ip || '—'}</strong><small>{connection.process || (connection.source_port ? `:${connection.source_port}` : t('未知进程'))}</small></span>
            <span className="traffic-v3-path"><strong>{ruleLabel(connection)}</strong><small title={connection.chains.join(' → ')}>{connection.chains.join(' → ') || t('未报告出口链')}</small>{hasIssue && <em>{t('需要处理')}</em>}</span>
            <span className="traffic-v3-bytes">↓ {formatBytes(connection.download)}<small>↑ {formatBytes(connection.upload)}</small></span>
          </button>
        })}
        {!visibleConnections.length && <div className="traffic-v3-empty">{loading ? t('正在读取当前连接…') : filter === 'focus' ? t('当前没有需要关注的活动连接；普通直连、DNS 与局域网流量已自动降噪。') : t('当前没有符合筛选条件的活动连接。')}</div>}
      </div>
    </section>

    <section className="section traffic-v3-issues-section">
      <SectionTitle title={t('需要处理')} subtitle={t('不再根据“同一域名出现多个规则/链路”猜异常；这里只有当前证据能够支持的问题。')} />
      {issues.length ? <div className="traffic-v3-issue-list">
        {issues.map(issue => <article className={`traffic-v3-issue ${issue.severity}`} key={issue.id}>
          <div className="traffic-v3-issue-icon" aria-hidden="true">{issue.severity === 'error' ? '!' : '↕'}</div>
          <div className="traffic-v3-issue-copy"><small>{issue.source_ip || t('未知来源')} · {issue.target}</small><h3>{t(issue.title)}</h3><p>{t(issue.detail)}</p><div className="traffic-v3-evidence">{issue.evidence.map(item => <span key={item}>{item}</span>)}</div></div>
          <button type="button" onClick={() => openIssue(issue)}>{t('查看证据')}</button>
        </article>)}
      </div> : <div className="traffic-v3-clean-state"><span aria-hidden="true">✓</span><div><strong>{t('当前未发现需要处理的分流问题')}</strong><p>{t('正常 DIRECT、DNS、LAN、不同规则命中以及策略组正常换节点都不会被误报。')}</p></div></div>}
    </section>

    <section className="section">
      <SectionTitle title={t('分流统计')} subtitle={t('把历史记录用于解释流量结构，而不是给路径变化贴异常标签。')} />
      <div className="traffic-v3-insights-grid">
        <RouteDistribution routes={routeSummary} />
        <div className="traffic-v3-insight-panel">
          <header><div><small>{t('规则命中')}</small><h3>{t('最近观察到的主要规则')}</h3></div><span>{t('按连接数')}</span></header>
          <div className="traffic-v3-rule-list">
            {ruleHits.map(rule => <div className="traffic-v3-rule-row" key={rule.key}><span className={`traffic-v3-route-dot route-${rule.route}`} /><div><strong>{rule.label}</strong><small>{formatBytes(rule.upload + rule.download)}</small></div><b>{rule.connections}</b></div>)}
            {!ruleHits.length && <div className="traffic-v3-mini-empty">{t('还没有历史规则数据')}</div>}
          </div>
        </div>
      </div>
      <div className="traffic-v3-targets">
        <header><strong>{t('主要目标')}</strong><span>{t('按历史流量排序 · 仅用于观察')}</span></header>
        {targets.slice(0, 8).map(target => <button type="button" key={target.key} onClick={() => {
          const record = history.find(item => targetLabel(item).toLowerCase() === target.key)
          if (record) setSelected(record)
        }}><span><strong>{target.host}</strong><small>{target.rules.slice(0, 2).join(' · ') || 'MATCH'}</small></span><span className="traffic-v3-target-routes">{target.routes.map(route => <i className={`route-${route}`} key={route}>{t(routeLabel(route))}</i>)}</span><span><strong>{formatBytes(target.upload + target.download)}</strong><small>{target.connections} {t('连接')}</small></span></button>)}
        {!targets.length && <div className="traffic-v3-mini-empty">{t('保持页面打开一段时间后，这里会形成流量结构。')}</div>}
      </div>
    </section>

    <section className="section">
      <SectionTitle title={t('高优先级覆盖规则')} subtitle={t('仅在确认需要修正时写入“高级：全局附加配置”的 rules.prepend；订阅原规则保持不变。')} />
      <div className="traffic-v3-override-list">
        {overlayRules.map(rule => <div className="traffic-v3-override-row" key={rule}><code>{rule}</code><button type="button" disabled={busy} onClick={() => void removeOverride(rule)}>{t('删除')}</button></div>)}
        {!overlayRules.length && <div className="traffic-v3-empty compact">{t('暂无高优先级覆盖规则。先从连接详情理解当前分流，再决定是否修正。')}</div>}
      </div>
      <p className="muted">{overlay?.document.enabled ? t(overlay.applied ? '附加配置当前已应用。' : overlay.desired ? '附加配置将在下次启动生效。' : '附加配置已启用，但当前是待应用草稿。') : t('创建第一条规则时会自动启用全局附加配置。')}</p>
    </section>

    <section className="section">
      <SectionTitle title={t('异常信号')} subtitle={t('诊断日志中的 error、warn、timeout、reset 等关键词单独保留为证据，不直接等同于分流错误。')} />
      <div className="traffic-v3-log-list">
        {logHints.map((item, index) => <div className="traffic-v3-log-row" key={`${item.source}-${index}`}><small>{item.source}</small><span>{item.line}</span></div>)}
        {!logHints.length && <div className="traffic-v3-empty compact">{t('近期日志中没有明显异常关键词。')}</div>}
      </div>
    </section>



    {selected && <ConnectionInspector
      connection={selected}
      groups={groups}
      issues={issues}
      policyOptions={policyOptions}
      existingRules={overlayRules}
      busy={busy}
      onClose={() => setSelected(null)}
      onSave={saveOverride}
    />}
  </>
}

function SummaryCard({ label, value, note, tone }: { label: string; value: number; note: string; tone: 'neutral' | 'proxy' | 'direct' | 'issue' | 'ok' }) {
  return <article className={`traffic-v3-summary-card tone-${tone}`}><small>{label}</small><strong>{value}</strong><span>{note}</span></article>
}

function RouteDistribution({ routes }: { routes: ReturnType<typeof aggregateTrafficRoutes> }) {
  const visible = routes.filter(route => route.connections > 0)
  const total = visible.reduce((sum, route) => sum + route.connections, 0)
  return <div className="traffic-v3-insight-panel">
    <header><div><small>{t('出口分布')}</small><h3>{t('流量最终去了哪里')}</h3></div><span>{total} {t('连接')}</span></header>
    <div className="traffic-v3-route-list">
      {visible.map(route => {
        const percent = total ? Math.max(2, Math.round((route.connections / total) * 100)) : 0
        return <div className="traffic-v3-route-stat" key={route.route}><div><span className={`traffic-v3-route-dot route-${route.route}`} /><strong>{t(routeLabel(route.route))}</strong><small>{formatBytes(route.upload + route.download)}</small><b>{route.connections}</b></div><span><i className={`route-${route.route}`} style={{ width: `${percent}%` }} /></span></div>
      })}
      {!visible.length && <div className="traffic-v3-mini-empty">{t('还没有历史流量数据')}</div>}
    </div>
  </div>
}

function ConnectionInspector({
  connection,
  groups,
  issues,
  policyOptions,
  existingRules,
  busy,
  onClose,
  onSave,
}: {
  connection: TrafficRecord
  groups: ProxyGroup[]
  issues: TrafficIssue[]
  policyOptions: string[]
  existingRules: string[]
  busy: boolean
  onClose: () => void
  onSave: (rule: string) => Promise<void>
}) {
  const explanation = useMemo(() => buildRouteExplanation(connection, groups), [connection, groups])
  const relatedIssues = issues.filter(issue => issue.related_keys.includes(connection.key))
  const availableModes: RuleMatchMode[] = connection.host ? ['domain', 'domain-suffix', ...(connection.destination_ip && !connection.destination_ip.includes(':') ? ['ip' as const] : [])] : ['ip']
  const [editing, setEditing] = useState(false)
  const [mode, setMode] = useState<RuleMatchMode>(availableModes[0])
  const [policy, setPolicy] = useState(() => suggestedPolicy(connection, policyOptions))
  const [rule, setRule] = useState(() => buildOverrideRule(connection, availableModes[0], suggestedPolicy(connection, policyOptions)))

  useEffect(() => {
    setRule(buildOverrideRule(connection, mode, policy))
  }, [connection.key, mode, policy])

  const duplicate = existingRules.some(item => item.trim() === rule.trim())
  const route = explanation.route

  return <div className="traffic-v3-inspector-backdrop" role="presentation" onMouseDown={event => { if (event.target === event.currentTarget) onClose() }}>
    <aside className="traffic-v3-inspector" role="dialog" aria-modal="true" aria-label={t('连接详情')}>
      <header className="traffic-v3-inspector-head"><div><span className={`traffic-v3-route-badge route-${route}`}>{t(routeLabel(route))}</span><h2>{explanation.target}</h2><p>{connection.destination_ip || '—'}{connection.destination_port ? `:${connection.destination_port}` : ''}</p></div><button type="button" onClick={onClose} aria-label={t('关闭')}>×</button></header>

      <div className={`traffic-v3-verdict ${relatedIssues.length ? 'needs-action' : 'normal'}`}>
        <span aria-hidden="true">{relatedIssues.length ? '!' : '✓'}</span>
        <div><strong>{relatedIssues.length ? t('这条连接关联到需要处理的问题') : t('当前证据没有显示这条连接存在分流异常')}</strong><p>{relatedIssues.length ? t('下面列出的判断来自近期连接或代理健康状态，不是因为 DIRECT 或规则变化本身。') : t('规则、策略组选择和实际出口链彼此可解释；正常 DIRECT、DNS、LAN 或策略组换节点不会被判错。')}</p></div>
      </div>

      {relatedIssues.length > 0 && <div className="traffic-v3-inspector-issues">{relatedIssues.map(issue => <div key={issue.id}><strong>{t(issue.title)}</strong><span>{issue.evidence.join(' · ')}</span></div>)}</div>}

      <div className="traffic-v3-explain-flow">
        <ExplainStep index="1" label={t('请求')} title={`${explanation.target}${connection.destination_port ? `:${connection.destination_port}` : ''}`} detail={[connection.source_ip, connection.process, connection.network.toUpperCase()].filter(Boolean).join(' · ') || '—'} />
        <ExplainStep index="2" label={t('命中规则')} title={explanation.matched_rule} detail={t('这是 Mihomo 对当前连接报告的实际规则命中。')} />
        <ExplainStep index="3" label={t('策略组选择')} title={explanation.group_selections.length ? explanation.group_selections.map(item => `${item.group} → ${item.selected}`).join(' · ') : t('没有可解释的策略组选择')} detail={explanation.group_selections.length ? t('来自当前 policies 状态；策略组正常切换节点不视为异常。') : t('DIRECT、REJECT 或没有经过已知策略组时这里可以为空。')} />
        <ExplainStep index="4" label={t('实际出口链')} title={explanation.actual_chain.join(' → ') || t('未报告出口链')} detail={t('原样展示 Mihomo connections API 的 chains，不根据链路数量推断异常。')} />
        <ExplainStep index="5" label={t('观察到的出口')} title={explanation.observed_egress} detail={`${t('最终分类')}：${t(routeLabel(route))}`} last />
      </div>

      <div className="traffic-v3-inspector-actions">
        <button type="button" onClick={onClose}>{t('关闭')}</button>
        <button className="primary" type="button" onClick={() => setEditing(value => !value)}>{t(editing ? '收起规则修正' : '创建覆盖规则')}</button>
      </div>

      {editing && <div className="traffic-v3-rule-editor">
        <div className="traffic-v3-rule-editor-head"><strong>{t('高优先级规则修正')}</strong><p>{t('只有确认当前路径不符合预期时再添加。它写入 rules.prepend，不修改订阅原规则。')}</p></div>
        <div className="traffic-v3-rule-form">
          <label>{t('匹配方式')}<select value={mode} onChange={event => setMode(event.target.value as RuleMatchMode)}>{availableModes.map(value => <option key={value} value={value}>{t(matchModeLabel(value))}</option>)}</select></label>
          <label>{t('目标策略')}<select value={policy} onChange={event => setPolicy(event.target.value)}>{policyOptions.map(value => <option key={value} value={value}>{value}</option>)}</select></label>
          <label className="wide">{t('高优先级规则')}<input value={rule} onChange={event => setRule(event.target.value)} spellCheck={false} /></label>
        </div>
        {duplicate && <div className="notice warn"><strong>{t('规则已存在')}</strong><p>{t('保存会把相同规则移动到高优先级列表最前面，不会重复创建。')}</p></div>}
        <div className="traffic-v3-rule-save"><button className="primary" type="button" disabled={busy || !validOverrideRule(rule)} onClick={() => void onSave(rule)}>{busy ? t('正在保存并应用…') : t('保存并应用覆盖规则')}</button></div>
      </div>}
    </aside>
  </div>
}

function ExplainStep({ index, label, title, detail, last = false }: { index: string; label: string; title: string; detail: string; last?: boolean }) {
  return <div className={`traffic-v3-explain-step ${last ? 'last' : ''}`}><span>{index}</span><div><small>{label}</small><strong>{title}</strong><p>{detail}</p></div></div>
}

function suggestedPolicy(connection: TrafficRecord, options: string[]): string {
  const route = classifyTrafficRoute(connection)
  if (route === 'direct' || route === 'lan') return options.find(option => option !== 'DIRECT' && option !== 'REJECT') ?? 'DIRECT'
  return options.includes('DIRECT') ? 'DIRECT' : options[0] ?? 'DIRECT'
}

function filterLabel(filter: TrafficViewFilter): string {
  if (filter === 'focus') return '关注'
  if (filter === 'proxy') return '代理'
  if (filter === 'direct') return '直连'
  if (filter === 'reject') return '拒绝'
  return '全部'
}

function routeLabel(route: TrafficRouteKind): string {
  if (route === 'proxy') return '代理'
  if (route === 'direct') return '直连'
  if (route === 'reject') return '拒绝'
  if (route === 'dns') return 'DNS'
  if (route === 'lan') return '局域网'
  return '未知'
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
