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

const optimizerRequest = (path: string, init?: RequestInit) => request<OptimizerResponse>(path, init)
const defaultTarget = (): Target => ({ domain: '', enabled: true, test_path: '/' })

export function CloudflareOptimizerPage() {
  const [data, setData] = useState<OptimizerResponse | null>(null)
  const [draft, setDraft] = useState<OptimizerConfig | null>(null)
  const [saving, setSaving] = useState(false)
  const [scanning, setScanning] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const next = await optimizerRequest('/api/v1/cloudflare-opt')
      setData(next)
      setDraft(next.config)
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const dirty = useMemo(() => data && draft ? JSON.stringify(data.config) !== JSON.stringify(draft) : false, [data, draft])

  const updateSchedule = (patch: Partial<Schedule>) => setDraft(current => current ? { ...current, schedule: { ...current.schedule, ...patch } } : current)
  const updateScan = (patch: Partial<ScanSettings>) => setDraft(current => current ? { ...current, scan: { ...current.scan, ...patch } } : current)
  const updateTarget = (index: number, patch: Partial<Target>) => setDraft(current => current ? {
    ...current,
    targets: current.targets.map((target, targetIndex) => targetIndex === index ? { ...target, ...patch } : target),
  } : current)

  const save = async (): Promise<boolean> => {
    if (!draft || saving) return false
    setSaving(true)
    setError('')
    try {
      const next = await optimizerRequest('/api/v1/cloudflare-opt', { method: 'PUT', body: JSON.stringify(draft) })
      setData(next)
      setDraft(next.config)
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
      setDraft(next.config)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setScanning(false)
    }
  }

  if (!draft || !data) return <div className="cloudflare-page">
    <PageHeader eyebrow="CLOUDFLARE" title="Cloudflare 优选" description="正在读取优选配置…" />
    <Panel busy>
      <SectionHeader eyebrow="INITIALIZING" title="正在连接 Cloudflare 优选服务" subtitle="读取目标域名、测速预算和最近一次优选结果。" />
      {error
        ? <div className="error-banner" role="alert"><span>!</span><p>{error}</p><button className="ui-button" type="button" onClick={() => void load()}>{t('重试')}</button></div>
        : <Empty text="正在加载…" />}
    </Panel>
  </div>

  const canScan = draft.targets.some(target => target.enabled && target.domain.trim())

  return <div className="cloudflare-page">
    <PageHeader
      eyebrow="CLOUDFLARE HOSTS OPTIMIZER"
      title="Cloudflare 优选"
      description="为指定域名筛选更合适的 Cloudflare IPv4，并写入最终 Mihomo Hosts 与真实 IP 规则。测速强制绑定 QNAP 容器物理接口，整轮受时间预算限制。"
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
      <article className="ui-stat"><small>{t('时间预算')}</small><strong className="cloudflare-number">{draft.scan.budget_seconds}s</strong></article>
    </section>

    <Panel>
      <SectionHeader eyebrow="AUTOMATION" title="自动优选" subtitle="默认每 7 天执行一次；也可以切换到标准 5 段 Cron 表达式。" />
      <div className="ui-form-grid">
        <div className="ui-field"><span>{t('启用自动优选')}</span><label className="ui-switch"><input type="checkbox" checked={draft.enabled} onChange={event => setDraft(current => current ? { ...current, enabled: event.target.checked } : current)} /><span>{t('启用')}</span></label></div>
        <FormField label="周期模式"><select value={draft.schedule.mode} onChange={event => updateSchedule({ mode: event.target.value as Schedule['mode'] })}><option value="interval">{t('每 N 天')}</option><option value="cron">Cron</option></select></FormField>
        {draft.schedule.mode === 'interval' ? <>
          <FormField label="每隔"><select value={draft.schedule.every_days ?? 7} onChange={event => updateSchedule({ every_days: Number(event.target.value) })}><option value={1}>{t('1 天')}</option><option value={3}>{t('3 天')}</option><option value={7}>{t('7 天')}</option><option value={14}>{t('14 天')}</option><option value={30}>{t('30 天')}</option></select></FormField>
          <FormField label="执行时间"><input type="time" value={draft.schedule.at ?? '04:00'} onChange={event => updateSchedule({ at: event.target.value })} /></FormField>
        </> : <FormField className="span-2" label="Cron 表达式" hint="标准 5 段：分钟 小时 日 月 星期，例如每周日 04:00 为 0 4 * * 0。"><input value={draft.schedule.cron ?? '0 4 * * 0'} placeholder="0 4 * * 0" onChange={event => updateSchedule({ cron: event.target.value })} /></FormField>}
      </div>
    </Panel>

    <Panel>
      <SectionHeader
        eyebrow="TARGETS"
        title="优选域名"
        subtitle="域名会自动规范化并去重。测试使用目标域名的 TLS SNI / HTTP Host，最终地址写入运行态 Hosts，并加入真实 IP 处理。"
        action={<button className="ui-button" type="button" onClick={() => setDraft(current => current ? { ...current, targets: [...current.targets, defaultTarget()] } : current)}>+ {t('添加域名')}</button>}
      />
      <div className="cloudflare-target-list">
        {draft.targets.length === 0 && <Empty text="尚未添加域名。" />}
        {draft.targets.map((target, index) => <div className="ui-card cloudflare-target-card" key={`${index}-${target.domain}`}>
          <div className="cloudflare-target-fields">
            <FormField label="域名"><input value={target.domain} placeholder="api.example.com" onChange={event => updateTarget(index, { domain: event.target.value })} /></FormField>
            <FormField label="测试路径"><input value={target.test_path ?? '/'} placeholder="/" onChange={event => updateTarget(index, { test_path: event.target.value })} /></FormField>
          </div>
          <div className="cloudflare-target-actions">
            <label className="ui-switch"><input type="checkbox" checked={target.enabled} onChange={event => updateTarget(index, { enabled: event.target.checked })} /><span>{t('启用')}</span></label>
            <button className="ui-button ui-button--danger" type="button" onClick={() => setDraft(current => current ? { ...current, targets: current.targets.filter((_, targetIndex) => targetIndex !== index) } : current)}>{t('删除')}</button>
          </div>
        </div>)}
      </div>
    </Panel>

    <Panel>
      <SectionHeader eyebrow="SCAN BUDGET" title="测速限制" subtitle="共享 TCP 粗筛只执行一次，各域名复用候选池。达到总时间预算后停止扩展测试，避免长时间占用网络。" />
      <div className="ui-form-grid">
        <FormField label="总时间预算"><select value={draft.scan.budget_seconds} onChange={event => updateScan({ budget_seconds: Number(event.target.value) })}><option value={30}>{t('快速 · 30 秒')}</option><option value={60}>{t('标准 · 60 秒')}</option><option value={90}>{t('完整 · 90 秒')}</option><option value={120}>{t('深入 · 120 秒')}</option></select></FormField>
        <FormField label="候选 IP"><input className="cloudflare-number" type="number" min={32} max={2048} value={draft.scan.candidate_limit} onChange={event => updateScan({ candidate_limit: Number(event.target.value) })} /></FormField>
        <FormField label="TCP 并发"><input className="cloudflare-number" type="number" min={1} max={256} value={draft.scan.tcp_concurrency} onChange={event => updateScan({ tcp_concurrency: Number(event.target.value) })} /></FormField>
        <FormField label="HTTPS 候选"><input className="cloudflare-number" type="number" min={1} max={64} value={draft.scan.https_candidate_count} onChange={event => updateScan({ https_candidate_count: Number(event.target.value) })} /></FormField>
        <FormField label="下载候选"><input className="cloudflare-number" type="number" min={0} max={10} value={draft.scan.download_candidate_count} onChange={event => updateScan({ download_candidate_count: Number(event.target.value) })} /></FormField>
        <FormField label="单 IP 下载秒数"><input className="cloudflare-number" type="number" min={1} max={10} value={draft.scan.download_seconds} onChange={event => updateScan({ download_seconds: Number(event.target.value) })} /></FormField>
      </div>
    </Panel>

    <Panel>
      <SectionHeader eyebrow="RESULTS" title="当前结果" subtitle="只有最佳 IP 发生变化时才会重新生成有效配置并执行一次 Mihomo 重载。" />
      {data.state.results.length === 0
        ? <Empty text="尚无优选结果。" />
        : <TableSurface><table className="ui-table"><thead><tr><th>{t('域名')}</th><th>{t('当前 IP')}</th><th>{t('延迟')}</th><th>{t('丢包')}</th><th>TTFB</th><th>{t('下载')}</th><th>Colo</th></tr></thead><tbody>{data.state.results.map(result => <tr key={result.domain}><td><strong>{result.domain}</strong></td><td><code>{result.selected.ip}</code></td><td className="cloudflare-number">{result.selected.latency_ms} ms</td><td className="cloudflare-number">{(result.selected.loss_rate * 100).toFixed(0)}%</td><td className="cloudflare-number">{result.selected.ttfb_ms ? `${result.selected.ttfb_ms} ms` : '—'}</td><td className="cloudflare-number">{result.selected.download_mbps ? `${result.selected.download_mbps.toFixed(1)} Mbps` : '—'}</td><td>{result.selected.colo || '—'}</td></tr>)}</tbody></table></TableSurface>}
    </Panel>

    <ActionBar status={t(dirty ? '有未保存修改' : '配置已保存')}>
      <button className="ui-button" type="button" disabled={!dirty || saving || scanning} onClick={() => setDraft(data.config)}>{t('撤销')}</button>
      <button className="ui-button ui-button--primary" type="button" disabled={!dirty || saving || scanning} onClick={() => void save()}>{t(saving ? '正在保存…' : '保存设置')}</button>
    </ActionBar>
  </div>
}

function formatTime(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString(localeIdentifier())
}
