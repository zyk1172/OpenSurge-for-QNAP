import { useCallback, useEffect, useMemo, useState } from 'react'
import { request } from '../api'
import { ActionBar, Empty, FormField, PageHeader, Panel, SectionHeader, TableSurface } from '../components/Common'
import { localeIdentifier, t } from '../i18n'

type Schedule = { mode: 'interval' | 'cron'; every_days?: number; at?: string; cron?: string }
type ScanSettings = {
  budget_seconds: number
  candidate_limit: number
  tcp_concurrency: number
  tcp_attempts: number
  tcp_timeout_ms: number
  https_candidate_count: number
  http_timeout_ms: number
  download_candidate_count: number
  download_seconds: number
  download_max_bytes: number
}
type Target = { domain: string; enabled: boolean; test_path?: string }
type Candidate = { ip: string; latency_ms: number; loss_rate: number; ttfb_ms?: number; download_mbps?: number; colo?: string }
type Result = { domain: string; selected: Candidate; alternatives: Candidate[]; updated_at: string }
type OptimizerConfig = { schema_version: number; enabled: boolean; schedule: Schedule; scan: ScanSettings; targets: Target[] }
type OptimizerState = { running: boolean; started_at?: string; last_run_at?: string; next_run_at?: string; last_error?: string; results: Result[] }
type OptimizerResponse = { config: OptimizerConfig; state: OptimizerState }
type ScanMode = 'fast' | 'standard' | 'full' | 'deep'
type ScheduleChoice = 'off' | '1' | '3' | '7' | '14' | '30' | 'custom'

const optimizerRequest = (path: string, init?: RequestInit) => request<OptimizerResponse>(path, init)
const intervalChoices = new Set([1, 3, 7, 14, 30])

const scanPresets: Record<ScanMode, ScanSettings> = {
  fast: {
    budget_seconds: 30,
    candidate_limit: 128,
    tcp_concurrency: 48,
    tcp_attempts: 2,
    tcp_timeout_ms: 700,
    https_candidate_count: 10,
    http_timeout_ms: 1600,
    download_candidate_count: 2,
    download_seconds: 1,
    download_max_bytes: 2 << 20,
  },
  standard: {
    budget_seconds: 60,
    candidate_limit: 256,
    tcp_concurrency: 64,
    tcp_attempts: 2,
    tcp_timeout_ms: 800,
    https_candidate_count: 15,
    http_timeout_ms: 2000,
    download_candidate_count: 3,
    download_seconds: 2,
    download_max_bytes: 4 << 20,
  },
  full: {
    budget_seconds: 90,
    candidate_limit: 512,
    tcp_concurrency: 96,
    tcp_attempts: 3,
    tcp_timeout_ms: 900,
    https_candidate_count: 24,
    http_timeout_ms: 3000,
    download_candidate_count: 4,
    download_seconds: 3,
    download_max_bytes: 8 << 20,
  },
  deep: {
    budget_seconds: 120,
    candidate_limit: 768,
    tcp_concurrency: 128,
    tcp_attempts: 3,
    tcp_timeout_ms: 1000,
    https_candidate_count: 32,
    http_timeout_ms: 4000,
    download_candidate_count: 5,
    download_seconds: 4,
    download_max_bytes: 16 << 20,
  },
}

export function CloudflareOptimizerPage() {
  const [data, setData] = useState<OptimizerResponse | null>(null)
  const [draft, setDraft] = useState<OptimizerConfig | null>(null)
  const [targetsText, setTargetsText] = useState('')
  const [saving, setSaving] = useState(false)
  const [scanning, setScanning] = useState(false)
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
  const scanMode = useMemo(() => draft ? detectScanMode(draft.scan) : 'standard', [draft])
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

  const updateScanMode = (mode: ScanMode) => setDraft(current => current ? { ...current, scan: { ...scanPresets[mode] } } : current)

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
    if (scanning || saving) return
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

  if (!draft || !data) return <div className="cloudflare-page">
    <PageHeader eyebrow="CLOUDFLARE" title="Cloudflare 优选" description="正在读取优选配置…" />
    <Panel busy>
      <SectionHeader eyebrow="INITIALIZING" title="正在连接 Cloudflare 优选服务" subtitle="读取目标域名、优选周期和最近一次结果。" />
      {error
        ? <div className="error-banner" role="alert"><span>!</span><p>{error}</p><button className="ui-button" type="button" onClick={() => void load()}>{t('重试')}</button></div>
        : <Empty text="正在加载…" />}
    </Panel>
  </div>

  const canScan = draft.targets.some(target => target.domain.trim())

  return <div className="cloudflare-page">
    <PageHeader
      eyebrow="CLOUDFLARE HOSTS OPTIMIZER"
      title="Cloudflare 优选"
      description="填写需要优选的域名，选择自动执行周期和测速模式即可。优选结果会写入 Mihomo Hosts；只有最佳 IP 变化时才重载运行配置。"
      action={<button className="ui-button ui-button--primary" type="button" disabled={scanning || !canScan} onClick={() => void scan()}>{t(scanning ? '正在优选…' : '立即优选')}</button>}
    />

    {error && <div className="error-banner" role="alert"><span>!</span><p>{error}</p><button className="ui-button" type="button" onClick={() => void load()}>{t('重新加载')}</button></div>}
    {data.state.last_error && <div className="notice warn" role="status"><strong>{t('上次任务未完成')}</strong><p>{data.state.last_error}</p></div>}

    <section className="ui-summary-grid" aria-label={t('当前结果')}>
      <article className="ui-summary-main">
        <span className="ui-status-orb"><strong>{data.state.results.length || '—'}</strong><small>{t('域名')}</small></span>
        <div className="ui-summary-copy"><small>MANAGED HOSTS</small><h2>{t(data.state.running || scanning ? '正在执行优选' : data.state.results.length ? '优选结果已应用' : '等待首次优选')}</h2><p>{t('仅结果变化时重载 Mihomo；多个域名整批只应用一次。')}</p></div>
      </article>
      <article className="ui-stat"><small>{t('上次执行')}</small><strong>{formatTime(data.state.last_run_at)}</strong></article>
      <article className="ui-stat"><small>{t('下次执行')}</small><strong>{formatTime(data.state.next_run_at)}</strong></article>
      <article className="ui-stat"><small>{t('测速模式')}</small><strong>{t(scanModeLabel(scanMode))}</strong></article>
    </section>

    <Panel>
      <SectionHeader
        eyebrow="TARGETS"
        title="优选域名"
        subtitle="一行一个域名，可直接粘贴整批列表；空行会忽略，重复域名只保留一份。"
      />
      <div className="ui-card cloudflare-target-editor">
        <FormField label="域名列表" hint="支持直接粘贴多行内容；保存时会忽略空行并自动去重。">
          <textarea
            className="cloudflare-target-textarea"
            aria-label={t('域名列表')}
            rows={8}
            spellCheck={false}
            placeholder={`api.example.com
cdn.example.com
assets.example.com`}
            value={targetsText}
            onChange={event => updateTargetsText(event.target.value)}
          />
        </FormField>
      </div>
    </Panel>

    <Panel>
      <SectionHeader eyebrow="SCHEDULE" title="自动优选周期" subtitle="选择多久重新优选一次；固定使用现有执行时间。关闭自动优选后仍可随时点击“立即优选”。" />
      <div className="cloudflare-simple-settings">
        <FormField label="周期">
          <select value={scheduleChoice} onChange={event => updateScheduleChoice(event.target.value as ScheduleChoice)}>
            <option value="off">{t('关闭自动优选')}</option>
            <option value="1">{t('1 天')}</option>
            <option value="3">{t('3 天')}</option>
            <option value="7">{t('7 天')}</option>
            <option value="14">{t('14 天')}</option>
            <option value="30">{t('30 天')}</option>
            {scheduleChoice === 'custom' && <option value="custom">{t('现有自定义周期（保留）')}</option>}
          </select>
        </FormField>
      </div>
    </Panel>

    <Panel>
      <SectionHeader eyebrow="SCAN MODE" title="测速模式" subtitle="只选择测试强度即可；候选数量、并发、超时和下载测试参数由 OpenSurge 统一管理。" />
      <div className="cloudflare-simple-settings">
        <FormField label="测速模式">
          <select value={scanMode} onChange={event => updateScanMode(event.target.value as ScanMode)}>
            <option value="fast">{t('快速 · 约 30 秒')}</option>
            <option value="standard">{t('标准 · 约 60 秒（推荐）')}</option>
            <option value="full">{t('完整 · 约 90 秒')}</option>
            <option value="deep">{t('深入 · 约 120 秒')}</option>
          </select>
        </FormField>
      </div>
    </Panel>

    <Panel>
      <SectionHeader eyebrow="RESULTS" title="当前结果" subtitle="只有最佳 IP 发生变化时才会重新生成有效配置并执行一次 Mihomo 重载。" />
      {data.state.results.length === 0
        ? <Empty text="尚无优选结果。" />
        : <TableSurface><table className="ui-table"><thead><tr><th>{t('域名')}</th><th>{t('当前 IP')}</th><th>{t('延迟')}</th><th>{t('丢包')}</th><th>TTFB</th><th>{t('下载')}</th><th>Colo</th></tr></thead><tbody>{data.state.results.map(result => <tr key={result.domain}><td><strong>{result.domain}</strong></td><td><code>{result.selected.ip}</code></td><td className="cloudflare-number">{result.selected.latency_ms} ms</td><td className="cloudflare-number">{(result.selected.loss_rate * 100).toFixed(0)}%</td><td className="cloudflare-number">{result.selected.ttfb_ms ? `${result.selected.ttfb_ms} ms` : '—'}</td><td className="cloudflare-number">{result.selected.download_mbps ? `${result.selected.download_mbps.toFixed(1)} Mbps` : '—'}</td><td>{result.selected.colo || '—'}</td></tr>)}</tbody></table></TableSurface>}
    </Panel>

    <ActionBar status={t(dirty ? '有未保存修改' : '配置已保存')}>
      <button className="ui-button" type="button" disabled={!dirty || saving || scanning} onClick={() => restoreDraft(data.config)}>{t('撤销')}</button>
      <button className="ui-button ui-button--primary" type="button" disabled={!dirty || saving || scanning} onClick={() => void save()}>{t(saving ? '正在保存…' : '保存设置')}</button>
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
  const exact = (Object.entries(scanPresets) as [ScanMode, ScanSettings][]).find(([, preset]) => JSON.stringify(scan) === JSON.stringify(preset))
  if (exact) return exact[0]
  if (scan.budget_seconds <= 30) return 'fast'
  if (scan.budget_seconds <= 60) return 'standard'
  if (scan.budget_seconds <= 90) return 'full'
  return 'deep'
}

function scanModeLabel(mode: ScanMode) {
  if (mode === 'fast') return '快速'
  if (mode === 'full') return '完整'
  if (mode === 'deep') return '深入'
  return '标准'
}

function formatTime(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString(localeIdentifier())
}
