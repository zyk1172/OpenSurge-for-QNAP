import { useCallback, useEffect, useMemo, useState } from 'react'
import { PageHeader } from '../components/Common'

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

async function optimizerRequest(path: string, init?: RequestInit): Promise<OptimizerResponse> {
  const response = await fetch(path, {
    credentials: 'same-origin',
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })
  if (!response.ok) {
    let message = response.statusText
    try {
      const payload = await response.json() as { error?: { message?: string } }
      message = payload.error?.message || message
    } catch { /* non-JSON response */ }
    throw new Error(message)
  }
  return response.json() as Promise<OptimizerResponse>
}

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

  const save = async () => {
    if (!draft || saving) return
    setSaving(true); setError('')
    try {
      const next = await optimizerRequest('/api/v1/cloudflare-opt', { method: 'PUT', body: JSON.stringify(draft) })
      setData(next); setDraft(next.config)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setSaving(false)
    }
  }

  const scan = async () => {
    if (scanning || saving) return
    if (dirty) await save()
    setScanning(true); setError('')
    try {
      const next = await optimizerRequest('/api/v1/cloudflare-opt/scan', { method: 'POST', body: '{}' })
      setData(next); setDraft(next.config)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setScanning(false)
    }
  }

  if (!draft || !data) return <>
    <PageHeader eyebrow="CLOUDFLARE" title="Cloudflare 优选" description="正在加载优选配置…" />
    {error && <div className="error-banner" role="alert"><span>!</span><p>{error}</p><button type="button" onClick={() => void load()}>重试</button></div>}
  </>

  return <>
    <PageHeader
      eyebrow="CLOUDFLARE HOSTS OPTIMIZER"
      title="Cloudflare 优选"
      description="为指定域名筛选更合适的 Cloudflare IPv4，并写入最终 Mihomo Hosts 与真实 IP 规则。测速强制绑定 QNAP 容器物理接口，整轮受时间预算限制。"
      action={<button className="primary" type="button" disabled={scanning || !draft.targets.some(target => target.enabled && target.domain.trim())} onClick={() => void scan()}>{scanning ? '正在优选…' : '立即优选'}</button>}
    />

    {error && <div className="error-banner" role="alert"><span>!</span><p>{error}</p><button type="button" onClick={() => void load()}>重新加载</button></div>}
    {data.state.last_error && <div className="notice warn" role="status"><strong>上次任务未完成</strong><p>{data.state.last_error}</p></div>}

    <section className="connectivity-overview">
      <div className="connectivity-score"><span className={`score-orb ${data.state.last_error ? 'mixed' : data.state.results.length ? 'healthy' : ''}`}><strong>{data.state.results.length || '—'}</strong><small>域名</small></span><div><small>MANAGED HOSTS</small><h2>{data.state.running || scanning ? '正在执行优选' : data.state.results.length ? '优选结果已应用' : '等待首次优选'}</h2><p>仅结果变化时重载 Mihomo；多个域名整批只应用一次。</p></div></div>
      <div className="connectivity-metrics"><span><small>上次执行</small><strong>{formatTime(data.state.last_run_at)}</strong></span><span><small>下次执行</small><strong>{formatTime(data.state.next_run_at)}</strong></span><span><small>时间预算</small><strong>{draft.scan.budget_seconds}s</strong></span></div>
    </section>

    <section className="panel">
      <div className="section-heading"><div><small>AUTOMATION</small><h2>自动优选</h2><p>默认每 7 天执行一次；也可以切换到标准 5 段 Cron 表达式。</p></div></div>
      <div className="form-grid">
        <label><span>启用自动优选</span><input type="checkbox" checked={draft.enabled} onChange={event => setDraft(current => current ? { ...current, enabled: event.target.checked } : current)} /></label>
        <label><span>周期模式</span><select value={draft.schedule.mode} onChange={event => updateSchedule({ mode: event.target.value as Schedule['mode'] })}><option value="interval">每 N 天</option><option value="cron">Cron</option></select></label>
        {draft.schedule.mode === 'interval' ? <>
          <label><span>每隔</span><select value={draft.schedule.every_days ?? 7} onChange={event => updateSchedule({ every_days: Number(event.target.value) })}><option value={1}>1 天</option><option value={3}>3 天</option><option value={7}>7 天</option><option value={14}>14 天</option><option value={30}>30 天</option></select></label>
          <label><span>执行时间</span><input type="time" value={draft.schedule.at ?? '04:00'} onChange={event => updateSchedule({ at: event.target.value })} /></label>
        </> : <label className="span-2"><span>Cron 表达式</span><input value={draft.schedule.cron ?? '0 4 * * 0'} placeholder="0 4 * * 0" onChange={event => updateSchedule({ cron: event.target.value })} /><small>标准 5 段：分钟 小时 日 月 星期，例如每周日 04:00 为 0 4 * * 0。</small></label>}
      </div>
    </section>

    <section className="panel">
      <div className="section-heading"><div><small>TARGETS</small><h2>优选域名</h2><p>域名会自动规范化并去重。测试使用目标域名的 TLS SNI / HTTP Host，最终地址写入运行态 Hosts，并加入真实 IP 处理。</p></div><button type="button" onClick={() => setDraft(current => current ? { ...current, targets: [...current.targets, defaultTarget()] } : current)}>+ 添加域名</button></div>
      <div className="source-list">
        {draft.targets.length === 0 && <div className="empty-state"><p>尚未添加域名。</p></div>}
        {draft.targets.map((target, index) => <div className="source-card" key={`${index}-${target.domain}`}>
          <div className="source-main"><label><span>域名</span><input value={target.domain} placeholder="api.example.com" onChange={event => updateTarget(index, { domain: event.target.value })} /></label><label><span>测试路径</span><input value={target.test_path ?? '/'} placeholder="/" onChange={event => updateTarget(index, { test_path: event.target.value })} /></label></div>
          <div className="source-actions"><label className="toggle-row"><input type="checkbox" checked={target.enabled} onChange={event => updateTarget(index, { enabled: event.target.checked })} /><span>启用</span></label><button type="button" onClick={() => setDraft(current => current ? { ...current, targets: current.targets.filter((_, targetIndex) => targetIndex !== index) } : current)}>删除</button></div>
        </div>)}
      </div>
    </section>

    <section className="panel">
      <div className="section-heading"><div><small>SCAN BUDGET</small><h2>测速限制</h2><p>共享 TCP 粗筛只执行一次，各域名复用候选池。达到总时间预算后停止扩展测试，避免长时间占用网络。</p></div></div>
      <div className="form-grid">
        <label><span>总时间预算</span><select value={draft.scan.budget_seconds} onChange={event => updateScan({ budget_seconds: Number(event.target.value) })}><option value={30}>快速 · 30 秒</option><option value={60}>标准 · 60 秒</option><option value={90}>完整 · 90 秒</option><option value={120}>深入 · 120 秒</option></select></label>
        <label><span>候选 IP</span><input type="number" min={32} max={2048} value={draft.scan.candidate_limit} onChange={event => updateScan({ candidate_limit: Number(event.target.value) })} /></label>
        <label><span>TCP 并发</span><input type="number" min={1} max={256} value={draft.scan.tcp_concurrency} onChange={event => updateScan({ tcp_concurrency: Number(event.target.value) })} /></label>
        <label><span>HTTPS 候选</span><input type="number" min={1} max={64} value={draft.scan.https_candidate_count} onChange={event => updateScan({ https_candidate_count: Number(event.target.value) })} /></label>
        <label><span>下载候选</span><input type="number" min={0} max={10} value={draft.scan.download_candidate_count} onChange={event => updateScan({ download_candidate_count: Number(event.target.value) })} /></label>
        <label><span>单 IP 下载秒数</span><input type="number" min={1} max={10} value={draft.scan.download_seconds} onChange={event => updateScan({ download_seconds: Number(event.target.value) })} /></label>
      </div>
    </section>

    <section className="panel">
      <div className="section-heading"><div><small>RESULTS</small><h2>当前结果</h2><p>只有最佳 IP 发生变化时才会重新生成有效配置并执行一次 Mihomo 重载。</p></div></div>
      {data.state.results.length === 0 ? <div className="empty-state"><p>尚无优选结果。</p></div> : <div className="table-wrap"><table><thead><tr><th>域名</th><th>当前 IP</th><th>延迟</th><th>丢包</th><th>TTFB</th><th>下载</th><th>Colo</th></tr></thead><tbody>{data.state.results.map(result => <tr key={result.domain}><td><strong>{result.domain}</strong></td><td><code>{result.selected.ip}</code></td><td>{result.selected.latency_ms} ms</td><td>{(result.selected.loss_rate * 100).toFixed(0)}%</td><td>{result.selected.ttfb_ms ? `${result.selected.ttfb_ms} ms` : '—'}</td><td>{result.selected.download_mbps ? `${result.selected.download_mbps.toFixed(1)} Mbps` : '—'}</td><td>{result.selected.colo || '—'}</td></tr>)}</tbody></table></div>}
    </section>

    <div className="sticky-actions"><span>{dirty ? '有未保存修改' : '配置已保存'}</span><button type="button" disabled={!dirty || saving || scanning} onClick={() => setDraft(data.config)}>撤销</button><button className="primary" type="button" disabled={!dirty || saving || scanning} onClick={() => void save()}>{saving ? '正在保存…' : '保存设置'}</button></div>
  </>
}

function formatTime(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString()
}
