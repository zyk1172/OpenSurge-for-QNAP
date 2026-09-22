import { useCallback, useEffect, useMemo, useState } from 'react'
import { request } from '../api'
import { ActionBar, Empty, FormField, PageHeader, Panel, SectionHeader, TableSurface } from '../components/Common'
import { localeIdentifier, t } from '../i18n'

type Schedule = { mode: 'interval' | 'cron'; every_days?: number; at?: string; cron?: string }
type HealthSettings = {
  enabled: boolean
  check_interval_minutes: number
  latency_threshold_ms: number
  loss_rate_threshold: number
}
type ScanSettings = {
  budget_seconds: number
  candidate_limit: number
  tcp_concurrency: number
  tcp_attempts: number
  tcp_timeout_ms: number
  max_latency_ms: number
  max_loss_rate: number
  https_candidate_count: number
  http_timeout_ms: number
  download_candidate_count: number
  download_seconds: number
  download_max_bytes: number
  min_download_mbps: number
}
type Target = { domain: string; enabled: boolean; test_path?: string }
type Candidate = { ip: string; latency_ms: number; loss_rate: number; ttfb_ms?: number; download_mbps?: number; colo?: string }
type Result = { domain: string; selected: Candidate; alternatives: Candidate[]; updated_at: string }
type HealthResult = { domain: string; ip: string; healthy: boolean; latency_ms?: number; loss_rate?: number; ttfb_ms?: number; checked_at: string; error?: string }
type OptimizerConfig = { schema_version: number; enabled: boolean; schedule: Schedule; health: HealthSettings; scan: ScanSettings; targets: Target[] }
type OptimizerState = {
  running: boolean
  checking: boolean
  started_at?: string
  last_run_at?: string
  next_run_at?: string
  last_health_check_at?: string
  next_health_check_at?: string
  last_error?: string
  health: HealthResult[]
  results: Result[]
}
type OptimizerResponse = { config: OptimizerConfig; state: OptimizerState }
type ScanMode = 'cfst' | 'fast' | 'full' | 'deep' | 'custom'
type ScanPresetMode = Exclude<ScanMode, 'custom'>
type ScheduleChoice = 'off' | '1' | '3' | '7' | '14' | '30' | 'custom'

const optimizerRequest = (path: string, init?: RequestInit) => request<OptimizerResponse>(path, init)
const intervalChoices = new Set([1, 3, 7, 14, 30])

const scanPresets: Record<ScanPresetMode, ScanSettings> = {
  cfst: {
    budget_seconds: 180,
    candidate_limit: 0,
    tcp_concurrency: 200,
    tcp_attempts: 4,
    tcp_timeout_ms: 1000,
    max_latency_ms: 9999,
    max_loss_rate: 1,
    https_candidate_count: 10,
    http_timeout_ms: 2500,
    download_candidate_count: 10,
    download_seconds: 10,
    download_max_bytes: 200_000_000,
    min_download_mbps: 0,
  },
  fast: {
    budget_seconds: 30,
    candidate_limit: 256,
    tcp_concurrency: 64,
    tcp_attempts: 2,
    tcp_timeout_ms: 700,
    max_latency_ms: 100,
    max_loss_rate: 0,
    https_candidate_count: 12,
    http_timeout_ms: 1800,
    download_candidate_count: 3,
    download_seconds: 2,
    download_max_bytes: 200_000_000,
    min_download_mbps: 0,
  },
  full: {
    budget_seconds: 120,
    candidate_limit: 1536,
    tcp_concurrency: 200,
    tcp_attempts: 4,
    tcp_timeout_ms: 900,
    max_latency_ms: 100,
    max_loss_rate: 0,
    https_candidate_count: 40,
    http_timeout_ms: 3000,
    download_candidate_count: 12,
    download_seconds: 5,
    download_max_bytes: 200_000_000,
    min_download_mbps: 0,
  },
  deep: {
    budget_seconds: 180,
    candidate_limit: 2048,
    tcp_concurrency: 256,
    tcp_attempts: 4,
    tcp_timeout_ms: 1000,
    max_latency_ms: 100,
    max_loss_rate: 0,
    https_candidate_count: 64,
    http_timeout_ms: 4000,
    download_candidate_count: 20,
    download_seconds: 6,
    download_max_bytes: 200_000_000,
    min_download_mbps: 0,
  },
}

export function CloudflareOptimizerPage() {
  const [data, setData] = useState<OptimizerResponse | null>(null)
  const [draft, setDraft] = useState<OptimizerConfig | null>(null)
  const [targetsText, setTargetsText] = useState('')
  const [saving, setSaving] = useState(false)
  const [scanning, setScanning] = useState(false)
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const next = await optimizerRequest('/api/v1/cloudflare-opt')
      setData(next)
      setDraft(next.config)
      setTargetsText(targetsToText(next.config.targets))
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const simpleDraft = useMemo(() => draft ? simplifyTargets(draft) : null, [draft])
  const dirty = useMemo(() => data && simpleDraft ? JSON.stringify(data.config) !== JSON.stringify(simpleDraft) : false, [data, simpleDraft])
  const scanMode = useMemo<ScanMode>(() => draft ? detectScanMode(draft.scan) : 'cfst', [draft])
  const scheduleChoice = useMemo<ScheduleChoice>(() => {
    if (!draft?.enabled) return 'off'
    if (draft.schedule.mode === 'interval' && intervalChoices.has(draft.schedule.every_days ?? 0)) return String(draft.schedule.every_days) as ScheduleChoice
    return 'custom'
  }, [draft])

  const updateTargetsText = (value: string) => {
    setTargetsText(value)
    setDraft(current => current ? { ...current, targets: targetsFromText(value) } : current)
  }

  const restoreDraft = (config: OptimizerConfig) => {
    setDraft(config)
    setTargetsText(targetsToText(config.targets))
  }

  const updateScheduleChoice = (choice: ScheduleChoice) => setDraft(current => {
    if (!current || choice === 'custom') return current
    if (choice === 'off') return { ...current, enabled: false }
    return {
      ...current,
      enabled: true,
      schedule: { mode: 'interval', every_days: Number(choice), at: current.schedule.at || '04:00' },
    }
  })

  const updateHealth = (patch: Partial<HealthSettings>) => setDraft(current => current ? {
    ...current,
    health: { ...current.health, ...patch },
  } : current)

  const updateScan = (patch: Partial<ScanSettings>) => setDraft(current => current ? {
    ...current,
    scan: { ...current.scan, ...patch },
  } : current)

  const updateScanMode = (mode: ScanMode) => setDraft(current => {
    if (!current || mode === 'custom') return current
    return { ...current, scan: { ...scanPresets[mode] } }
  })

  const save = async (): Promise<boolean> => {
    if (!simpleDraft || saving) return false
    setSaving(true)
    setError('')
    try {
      const next = await optimizerRequest('/api/v1/cloudflare-opt', { method: 'PUT', body: JSON.stringify(simpleDraft) })
      setData(next)
      restoreDraft(next.config)
      return true
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
      return false
    } finally {
      setSaving(false)
    }
  }

  const scan = async () => {
    if (scanning || checking || saving) return
    if (dirty && !(await save())) return
    setScanning(true)
    setError('')
    try {
      const next = await optimizerRequest('/api/v1/cloudflare-opt/scan', { method: 'POST', body: '{}' })
      setData(next)
      restoreDraft(next.config)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setScanning(false)
    }
  }

  const checkHealth = async () => {
    if (checking || scanning || saving || data?.state.results.length === 0) return
    if (dirty && !(await save())) return
    setChecking(true)
    setError('')
    try {
      const next = await optimizerRequest('/api/v1/cloudflare-opt/check', { method: 'POST', body: '{}' })
      setData(next)
      restoreDraft(next.config)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setChecking(false)
    }
  }

  if (!draft || !data) return <div className="cloudflare-page">
    <PageHeader eyebrow="CLOUDFLARE" title="Cloudflare 优选" description="正在读取优选配置…" />
    <Panel busy>
      <SectionHeader eyebrow="INITIALIZING" title="正在连接 Cloudflare 优选服务" subtitle="读取目标域名、持续监控设置和最近一次结果。" />
      {error
        ? <div className="error-banner" role="alert"><span>!</span><p>{error}</p><button className="ui-button" type="button" onClick={() => void load()}>{t('重试')}</button></div>
        : <Empty text="正在加载…" />}
    </Panel>
  </div>

  const canScan = draft.targets.some(target => target.domain.trim())
  const healthByDomain = new Map(data.state.health.map(item => [item.domain, item]))
  const unhealthyCount = data.state.health.filter(item => !item.healthy).length
  const busy = scanning || checking || data.state.running || data.state.checking
  const overallStatus = statusLabel(data.state, scanning, checking)

  return <div className="cloudflare-page">
    <PageHeader
      eyebrow="CLOUDFLARE CONTINUOUS OPTIMIZER"
      title="Cloudflare 优选"
      description="持续检查当前优选 IP；质量下降时自动重新优选，并按周期强制刷新。所有测速都绑定物理出口，避免代理 TUN 干扰结果。"
      action={<button className="ui-button ui-button--primary" type="button" disabled={busy || !canScan} onClick={() => void scan()}>{t(scanning || data.state.running ? '正在优选…' : '立即优选')}</button>}
    />

    {error && <div className="error-banner" role="alert"><span>!</span><p>{error}</p><button className="ui-button" type="button" onClick={() => void load()}>{t('重新加载')}</button></div>}
    {data.state.last_error && <div className="notice warn" role="status"><strong>{t('上次任务未完成')}</strong><p>{data.state.last_error}</p></div>}

    <section className="ui-summary-grid" aria-label={t('当前结果')}>
      <article className="ui-summary-main">
        <span className="ui-status-orb"><strong>{data.state.results.length || '—'}</strong><small>{t('域名')}</small></span>
        <div className="ui-summary-copy"><small>CONTINUOUS HEALTH</small><h2>{t(overallStatus)}</h2><p>{t(unhealthyCount ? '检测到质量下降，将触发重新优选。' : '当前 IP 通过物理出口健康检查；只有最佳 IP 变化时才重载 Mihomo。')}</p></div>
      </article>
      <article className="ui-stat"><small>{t('上次健康检查')}</small><strong>{formatTime(data.state.last_health_check_at)}</strong></article>
      <article className="ui-stat"><small>{t('下次健康检查')}</small><strong>{formatTime(data.state.next_health_check_at)}</strong></article>
      <article className="ui-stat"><small>{t('下次强制优选')}</small><strong>{formatTime(data.state.next_run_at)}</strong></article>
    </section>

    <Panel>
      <SectionHeader
        eyebrow="TARGETS"
        title="优选域名"
        subtitle="一行一个域名，可直接粘贴整批列表；OpenSurge 会使用目标域名进行 TLS SNI / HTTP Host 验证。"
      />
      <div className="ui-card cloudflare-target-editor">
        <FormField label="域名列表" hint="空行会忽略，重复域名只保留一份；结果仅注入最终 Mihomo Hosts，不修改原始订阅。">
          <textarea
            className="cloudflare-target-textarea"
            aria-label={t('域名列表')}
            rows={8}
            spellCheck={false}
            placeholder={'api.example.com\ncdn.example.com\nassets.example.com'}
            value={targetsText}
            onChange={event => updateTargetsText(event.target.value)}
          />
        </FormField>
      </div>
    </Panel>

    <Panel>
      <SectionHeader eyebrow="CONTINUOUS MONITORING" title="持续监控" subtitle="轻量检查当前 IP；超过延迟或丢包阈值才执行完整优选，同时保留周期性强制刷新。" />
      <div className="ui-form-grid cloudflare-monitor-grid">
        <FormField label="持续监控">
          <select value={draft.health.enabled ? 'on' : 'off'} onChange={event => updateHealth({ enabled: event.target.value === 'on' })}>
            <option value="on">{t('开启')}</option>
            <option value="off">{t('关闭')}</option>
          </select>
        </FormField>
        <FormField label="检查间隔">
          <select value={draft.health.check_interval_minutes} onChange={event => updateHealth({ check_interval_minutes: Number(event.target.value) })}>
            <option value={15}>{t('15 分钟')}</option>
            <option value={30}>{t('30 分钟（推荐）')}</option>
            <option value={60}>{t('1 小时')}</option>
            <option value={180}>{t('3 小时')}</option>
          </select>
        </FormField>
        <FormField label="延迟阈值">
          <select value={draft.health.latency_threshold_ms} onChange={event => updateHealth({ latency_threshold_ms: Number(event.target.value) })}>
            <option value={80}>80 ms</option>
            <option value={100}>100 ms</option>
            <option value={150}>150 ms</option>
            <option value={200}>200 ms</option>
          </select>
        </FormField>
        <FormField label="最大丢包">
          <select value={draft.health.loss_rate_threshold} onChange={event => updateHealth({ loss_rate_threshold: Number(event.target.value) })}>
            <option value={0}>0%</option>
            <option value={0.25}>25%</option>
            <option value={0.5}>50%</option>
          </select>
        </FormField>
        <FormField label="强制重新优选">
          <select value={scheduleChoice} onChange={event => updateScheduleChoice(event.target.value as ScheduleChoice)}>
            <option value="off">{t('关闭周期强制优选')}</option>
            <option value="1">{t('每 1 天（推荐）')}</option>
            <option value="3">{t('每 3 天')}</option>
            <option value="7">{t('每 7 天')}</option>
            <option value="14">{t('每 14 天')}</option>
            <option value="30">{t('每 30 天')}</option>
            {scheduleChoice === 'custom' && <option value="custom">{t('现有自定义周期（保留）')}</option>}
          </select>
        </FormField>
      </div>
      <div className="cloudflare-monitor-actions">
        <button className="primary cloudflare-check-button" type="button" disabled={busy || data.state.results.length === 0} onClick={() => void checkHealth()}>{t(checking || data.state.checking ? '正在检查…' : '立即检查')}</button>
      </div>
    </Panel>

    <Panel>
      <SectionHeader eyebrow="SCAN STRATEGY" title="完整优选策略" subtitle="默认按 CFST 流程：每个 /24 随机取一个 IP，200 并发执行 4 次 TCP 443 测试，默认不以延迟或丢包硬过滤，再对延迟靠前的 10 个候选执行下载测速并按速度排序。OpenSurge 额外保留目标域名 HTTPS/SNI 验证、物理出口绑定和 Cloudflare 官方测速流。" />
      <div className="ui-form-grid">
        <FormField label="测速模式">
          <select value={scanMode} onChange={event => updateScanMode(event.target.value as ScanMode)}>
            <option value="cfst">{t('CFST 默认 · 全部 /24（推荐）')}</option>
            <option value="fast">{t('快速 · 约 30 秒')}</option>
            <option value="full">{t('完整 · 约 120 秒')}</option>
            <option value="deep">{t('深入 · 最多 180 秒')}</option>
            {scanMode === 'custom' && <option value="custom">{t('自定义（保留当前参数）')}</option>}
          </select>
        </FormField>
        <FormField label="优选延迟上限" hint="超过这个 TCP 延迟的候选会在 HTTPS 和下载测速前直接剔除。">
          <select aria-label={t('优选延迟上限')} value={draft.scan.max_latency_ms} onChange={event => updateScan({ max_latency_ms: Number(event.target.value) })}>
            <option value={100}>100 ms</option>
            <option value={150}>150 ms</option>
            <option value={200}>200 ms</option>
            <option value={300}>300 ms</option>
            <option value={500}>500 ms</option>
            <option value={800}>800 ms</option>
            <option value={1000}>1000 ms</option>
            <option value={9999}>{t('不限制（CFST 默认）')}</option>
          </select>
        </FormField>
        <FormField label="优选丢包上限" hint="CFST 默认 100%，即只淘汰完全不可达的 IP；需要更严格时可手动降低。">
          <select aria-label={t('优选丢包上限')} value={draft.scan.max_loss_rate} onChange={event => updateScan({ max_loss_rate: Number(event.target.value) })}>
            <option value={0}>0%</option>
            <option value={0.25}>25%</option>
            <option value={0.5}>50%</option>
            <option value={1}>{t('100%（CFST 默认）')}</option>
          </select>
        </FormField>
        <FormField label="最低下载速度（Mbps）" hint="0 表示关闭速度硬筛选；大于 0 时，未测出速度或低于该值的候选都会剔除。">
          <input
            aria-label={t('最低下载速度（Mbps）')}
            type="number"
            min={0}
            max={10000}
            step={1}
            inputMode="decimal"
            value={draft.scan.min_download_mbps}
            onChange={event => updateScan({ min_download_mbps: Math.max(0, Number(event.target.value) || 0) })}
          />
        </FormField>
      </div>
      <TableSurface>
        <table className="ui-table">
          <thead><tr><th>{t('候选 IP')}</th><th>{t('TCP 并发')}</th><th>{t('TCP 采样')}</th><th>{t('延迟上限')}</th><th>{t('丢包上限')}</th><th>{t('HTTPS 候选')}</th><th>{t('下载候选')}</th></tr></thead>
          <tbody><tr><td>{draft.scan.candidate_limit === 0 ? t('全部 /24') : draft.scan.candidate_limit}</td><td>{draft.scan.tcp_concurrency}</td><td>{draft.scan.tcp_attempts}</td><td>{draft.scan.max_latency_ms} ms</td><td>{Math.round(draft.scan.max_loss_rate * 100)}%</td><td>{draft.scan.https_candidate_count}</td><td>{draft.scan.download_candidate_count}</td></tr></tbody>
        </table>
      </TableSurface>
    </Panel>

    <Panel>
      <SectionHeader eyebrow="RESULTS" title="当前结果" subtitle="健康检查只验证当前 IP；完整优选才会更新候选与 Hosts。备用地址保留在状态中，最佳 IP 变化时整批只重载 Mihomo 一次。" />
      {data.state.results.length === 0
        ? <Empty text="尚无优选结果。" />
        : <TableSurface><table className="ui-table"><thead><tr><th>{t('域名')}</th><th>{t('健康')}</th><th>{t('当前 IP')}</th><th>{t('延迟')}</th><th>{t('丢包')}</th><th>TTFB</th><th>{t('下载')}</th><th>Colo</th></tr></thead><tbody>{data.state.results.map(result => {
          const health = healthByDomain.get(result.domain)
          return <tr key={result.domain}>
            <td><strong>{result.domain}</strong></td>
            <td>{t(health ? (health.healthy ? '正常' : '需重选') : '待检查')}</td>
            <td><code>{result.selected.ip}</code></td>
            <td className="cloudflare-number">{health?.latency_ms ?? result.selected.latency_ms} ms</td>
            <td className="cloudflare-number">{((health?.loss_rate ?? result.selected.loss_rate) * 100).toFixed(0)}%</td>
            <td className="cloudflare-number">{health?.ttfb_ms ? `${health.ttfb_ms} ms` : result.selected.ttfb_ms ? `${result.selected.ttfb_ms} ms` : '—'}</td>
            <td className="cloudflare-number">{result.selected.download_mbps ? `${result.selected.download_mbps.toFixed(1)} Mbps` : '—'}</td>
            <td>{result.selected.colo || '—'}</td>
          </tr>
        })}</tbody></table></TableSurface>}
    </Panel>

    <ActionBar status={t(dirty ? '有未保存修改' : '配置已保存')}>
      <button className="ui-button" type="button" disabled={!dirty || saving || busy} onClick={() => restoreDraft(data.config)}>{t('撤销')}</button>
      <button className="ui-button ui-button--primary" type="button" disabled={!dirty || saving || busy} onClick={() => void save()}>{t(saving ? '正在保存…' : '保存设置')}</button>
    </ActionBar>
  </div>
}

function targetsToText(targets: Target[]) {
  return targets.map(target => target.domain.trim()).filter(Boolean).join('\n')
}

function targetsFromText(value: string): Target[] {
  const domains = [...new Set(value.split(/\r?\n/).map(line => line.trim()).filter(Boolean))]
  return domains.map(domain => ({ domain, enabled: true, test_path: '/' }))
}

function simplifyTargets(config: OptimizerConfig): OptimizerConfig {
  return {
    ...config,
    targets: config.targets.map(target => ({ ...target, enabled: true, test_path: '/' })),
  }
}

function detectScanMode(scan: ScanSettings): ScanMode {
  const exact = (Object.entries(scanPresets) as [ScanPresetMode, ScanSettings][]).find(([, preset]) => JSON.stringify(scan) === JSON.stringify(preset))
  return exact?.[0] ?? 'custom'
}


function statusLabel(state: OptimizerState, localScanning: boolean, localChecking: boolean) {
  if (state.running || localScanning) return '正在执行完整优选'
  if (state.checking || localChecking) return '正在检查当前 IP'
  if (state.results.length === 0) return '等待首次优选'
  if (state.health.some(item => !item.healthy)) return '当前 IP 需要重新优选'
  if (state.health.length > 0) return '当前 IP 健康'
  return '等待健康检查'
}

function formatTime(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString(localeIdentifier())
}
