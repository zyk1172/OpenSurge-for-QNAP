import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'
import { PageHeader, SectionTitle, StatusDot } from '../components/Common'
import type { Diagnostics, DoctorRunStatus, Overview } from '../types'
import { t } from '../i18n'

const doctorPollIntervalMs = 500
const qnapBuild = import.meta.env.VITE_OPENSURGE_TARGET === 'qnap'

export function DiagnosticsPage({ overview }: { overview: Overview | null }) {
  const [details, setDetails] = useState<Diagnostics | null>(null)
  const [doctorStatus, setDoctorStatus] = useState<DoctorRunStatus | null>(null)
  const [doctorError, setDoctorError] = useState('')

  useEffect(() => {
    let active = true
    void api.diagnostics().then(value => { if (active) setDetails(value) }).catch(() => { if (active) setDetails(null) })
    return () => { active = false }
  }, [overview?.revision, overview?.status.gateway])

  useEffect(() => {
    let active = true
    setDoctorError('')
    void api.doctorStatus()
      .then(value => { if (active) setDoctorStatus(value) })
      .catch(cause => { if (active) setDoctorError(cause instanceof Error ? cause.message : String(cause)) })
    return () => { active = false }
  }, [overview?.revision])

  useEffect(() => {
    if (doctorStatus?.state !== 'running') return
    let active = true
    let timer = 0
    const poll = () => {
      timer = window.setTimeout(() => {
        void api.doctorStatus()
          .then(value => {
            if (!active) return
            setDoctorStatus(value)
            setDoctorError('')
            if (value.state === 'running') poll()
          })
          .catch(cause => {
            if (!active) return
            setDoctorError(cause instanceof Error ? cause.message : String(cause))
            poll()
          })
      }, doctorPollIntervalMs)
    }
    poll()
    return () => { active = false; window.clearTimeout(timer) }
  }, [doctorStatus?.state, doctorStatus?.started_at])

  const runDoctor = async () => {
    setDoctorError('')
    try {
      setDoctorStatus(await api.runDoctor())
    } catch (cause) {
      setDoctorError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  const running = doctorStatus?.state === 'running'
  const checks = doctorStatus?.checks ?? []
  const orderedChecks = useMemo(() => [...checks].sort((left, right) => Number(left.ok) - Number(right.ok)), [checks])
  const failedChecks = checks.filter(check => !check.ok).length
  const connections = details?.connections.connections ?? []
  const providers = overview?.providers.proxy_providers ?? []

  return <>
    <PageHeader
      eyebrow="DIAGNOSTICS"
      title="诊断、连接与 Provider"
      description={qnapBuild
        ? '查看 QNAP 容器的数据面、Provider、连接和日志；不会执行任何 QTS 宿主网络修改。'
        : '错误保持结构化；日志经过已知凭据脱敏，菜单栏只复制压缩摘要。'}
    />

    {qnapBuild && <section className="diagnostic-summary-grid" aria-label="Diagnostics summary">
      <article className="diagnostic-summary-card">
        <small>GATEWAY</small>
        <strong>{overview?.status.gateway ?? 'unknown'}</strong>
        <span>{overview?.status.runtime_state ?? 'loading'}</span>
      </article>
      <article className={`diagnostic-summary-card ${failedChecks ? 'warn' : doctorStatus?.healthy ? 'ok' : ''}`}>
        <small>DOCTOR</small>
        <strong>{running ? t('后台检查中…') : failedChecks ? `${failedChecks} issues` : doctorStatus?.healthy ? 'Healthy' : 'Not run'}</strong>
        <span>{doctorSubtitle(doctorStatus)}</span>
      </article>
      <article className="diagnostic-summary-card">
        <small>CONNECTIONS</small>
        <strong>{connections.length}</strong>
        <span>↑ {formatBytes(details?.connections.upload_total ?? 0)} · ↓ {formatBytes(details?.connections.download_total ?? 0)}</span>
      </article>
      <article className="diagnostic-summary-card">
        <small>PROVIDERS</small>
        <strong>{providers.length}</strong>
        <span>{providers.filter(provider => provider.proxies.some(proxy => proxy.alive)).length} healthy</span>
      </article>
    </section>}

    <section className="split qnap-diagnostic-main">
      <div>
        <div className="diagnostic-doctor-head">
          <SectionTitle title={qnapBuild ? 'QNAP Environment Doctor' : 'Doctor'} subtitle={doctorSubtitle(doctorStatus)} />
          <button className="primary" type="button" disabled={running} onClick={() => void runDoctor()}>{running ? <><span className="button-spinner" aria-hidden="true" />{t('后台检查中…')}</> : t(doctorStatus?.state === 'idle' ? '运行 Doctor' : '重新运行 Doctor')}</button>
        </div>
        {qnapBuild && <div className="notice qnap-doctor-note">
          <strong>QNAP persistent-storage verification</strong>
          <p>Doctor verifies create, fsync, rename and directory fsync under /data/runtime. Deployment preflight additionally verifies /data/web-auth with the configured Web UID/GID.</p>
        </div>}
        {doctorError && <div className="notice warn" role="alert">{t('Doctor 状态暂不可用：{{error}}', { error: doctorError })}</div>}
        {doctorStatus?.state === 'failed' && <div className="notice warn" role="alert">{t('Doctor 后台任务失败：{{error}}', { error: doctorStatus.error || t('未知错误') })}</div>}
        <div className="doctor-check-list">
          {orderedChecks.map(check => <div className={`check ${check.ok ? '' : 'doctor-check-failed'}`} key={check.name}><span className={check.ok ? 'ok-mark' : 'bad-mark'}>{check.ok ? '✓' : '!'}</span><div><strong>{check.name}</strong><small>{check.message}</small></div></div>)}
        </div>
        {!checks.length && !doctorError && doctorStatus?.state !== 'failed' && <div className="empty">{t(running ? '检查在 Control Service 后台执行；离开本页不会启动第二份任务。' : qnapBuild ? '点击“运行 Doctor”后才会执行完整检查；普通 Web 刷新不会触发。' : '点击“运行 Doctor”后才会执行完整检查；总览与菜单栏刷新不会触发。')}</div>}
      </div>
      <div>
        <SectionTitle title="Proxy Providers" subtitle={qnapBuild ? '从这里观察和刷新 Provider' : '可从这里观察和刷新，不在菜单栏中执行'} />
        <div className="provider-status-list">
          {providers.map(provider => <div className="row" key={provider.name}><StatusDot status={provider.proxies.some(proxy => proxy.alive) ? 'running' : 'degraded'} /><div className="grow"><strong>{provider.name}</strong><small>{provider.proxy_count} proxies · {provider.vehicle_type}</small></div><button onClick={() => void api.refreshProvider(provider.name)}>{t('刷新')}</button></div>)}
          {!providers.length && <div className="empty">No Proxy Providers</div>}
        </div>
      </div>
    </section>

    <section className="section">
      <SectionTitle title="Live Connections" subtitle={details?.connection_error || `${connections.length} active connections`} />
      <div className="connection-summary-line"><span>↑ {formatBytes(details?.connections.upload_total ?? 0)}</span><span>↓ {formatBytes(details?.connections.download_total ?? 0)}</span></div>
      {connections.length ? <div className="diagnostic-connection-list">{connections.slice(0, 12).map(connection => <div className="diagnostic-connection-row" key={connection.id}><strong>{connection.rule || 'MATCH'}</strong><span>{(connection.chains ?? []).join(' → ') || connection.id.slice(0, 8)}</span></div>)}</div> : <div className="empty">No active connections</div>}
    </section>

    <section className="section">
      <SectionTitle title="Recent logs" subtitle="每个进程最多 80 行；API 会遮蔽 mihomo secret 与 upstream credentials" />
      <div className="diagnostic-log-list">
        {Object.entries(details?.logs ?? {}).map(([name, lines]) => <details key={name} className="diagnostic-log-panel"><summary><strong>{name}</strong><span>{lines.length} lines</span></summary><pre>{lines.join('\n') || 'No log output'}</pre></details>)}
        {!Object.keys(details?.logs ?? {}).length && <div className="empty">No logs</div>}
      </div>
    </section>

    <section className="section">
      <SectionTitle title="Operations 与恢复记录" subtitle={`Recovery: ${details?.recovery.stage ?? overview?.recovery.stage ?? 'idle'}`} />
      {details?.operations.length ? details.operations.map(operation => <div className="row" key={operation.id}><StatusDot status={operation.state === 'failed' ? 'degraded' : operation.state === 'succeeded' ? 'running' : 'stopped'} /><div className="grow"><strong>{operation.kind} · {operation.state}</strong><small>{operation.id} · {operation.updated_at}{operation.error ? ` · ${operation.error}` : ''}</small></div></div>) : <div className="empty">{t('尚无生命周期操作记录')}</div>}
    </section>
  </>
}

function doctorSubtitle(status: DoctorRunStatus | null): string {
  if (!status) return t('正在读取最近一次结果')
  if (status.state === 'idle') return t('完整检查仅在你显式运行时执行，最长可能需要 90 秒')
  if (status.state === 'running') return t('正在后台执行完整检查，不阻塞总览刷新')
  if (!status.current) return t('配置已变化；以下是旧版本结果，请重新运行')
  if (status.state === 'failed') return t('后台任务没有完成')
  return t(status.healthy ? '当前配置的基础检查通过' : '当前配置存在需要处理的问题')
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  const scaled = value / 1024 ** index
  return `${scaled >= 100 || index === 0 ? scaled.toFixed(0) : scaled.toFixed(1)} ${units[index]}`
}
