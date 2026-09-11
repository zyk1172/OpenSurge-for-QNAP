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
  const availableProviders = providers.filter(provider => provider.proxies.some(proxy => proxy.alive)).length
  const recoveryState = details?.recovery.stage ?? overview?.recovery.stage ?? 'idle'

  const doctorPanel = <>
    <div className="diagnostic-doctor-head">
      <SectionTitle title={t('环境检查')} subtitle={doctorSubtitle(doctorStatus)} />
      <button className="primary" type="button" disabled={running} onClick={() => void runDoctor()}>{running ? <><span className="button-spinner" aria-hidden="true" />{t('后台检查中')}</> : t(!doctorStatus || doctorStatus.state === 'idle' ? '运行检查' : '重新检查')}</button>
    </div>
    {qnapBuild && <div className="notice qnap-doctor-note">
      <strong>{t('持久化存储检查')}</strong>
      <p>{t('检查 /data/runtime 的持久化写入；/data/web-auth 权限由部署预检验证。')}</p>
    </div>}
    {doctorError && <div className="notice warn" role="alert">{t('环境检查状态暂不可用：{{error}}', { error: doctorError })}</div>}
    {doctorStatus?.state === 'failed' && <div className="notice warn" role="alert">{t('环境检查任务失败：{{error}}', { error: doctorStatus.error || t('未知错误') })}</div>}
    <div className="doctor-check-list">
      {orderedChecks.map(check => <div className={`check ${check.ok ? '' : 'doctor-check-failed'}`} key={check.name}><span className={check.ok ? 'ok-mark' : 'bad-mark'}>{check.ok ? '✓' : '!'}</span><div><strong>{doctorCheckName(check.name)}</strong>{check.message && <small>{doctorCheckMessage(check.message)}</small>}</div></div>)}
    </div>
    {!checks.length && !doctorError && doctorStatus?.state !== 'failed' && <div className="empty">{t(running ? '检查在后台执行；离开本页不会重复启动。' : '运行检查后才会执行完整检查；刷新页面不会自动触发。')}</div>}
  </>

  const providerPanel = <>
    <SectionTitle title={t('代理集合')} subtitle={t('查看状态并手动刷新')} />
    <div className="provider-status-list">
      {providers.map(provider => <div className="row" key={provider.name}><StatusDot status={provider.proxies.some(proxy => proxy.alive) ? 'running' : 'degraded'} /><div className="grow"><strong>{provider.name}</strong><small>{t('{{count}} 个节点 · {{type}}', { count: String(provider.proxy_count), type: provider.vehicle_type })}</small></div><button onClick={() => void api.refreshProvider(provider.name)}>{t('刷新')}</button></div>)}
      {!providers.length && <div className="empty">{t('暂无代理集合')}</div>}
    </div>
  </>

  return <>
    <PageHeader
      eyebrow={t('诊断')}
      title={t('诊断与运行状态')}
      description={t('查看网关、环境检查、代理集合、连接和日志。')}
    />

    {qnapBuild && <section className="diagnostic-summary-grid" aria-label={t('诊断概览')}>
      <article className="diagnostic-summary-card">
        <small>{t('网关')}</small>
        <strong>{statusLabel(overview?.status.gateway)}</strong>
        <span>{statusLabel(overview?.status.runtime_state ?? 'loading')}</span>
      </article>
      <article className={`diagnostic-summary-card ${failedChecks ? 'warn' : doctorStatus?.healthy ? 'ok' : ''}`}>
        <small>{t('环境检查')}</small>
        <strong>{running ? t('后台检查中') : failedChecks ? t('{{count}} 项异常', { count: String(failedChecks) }) : doctorStatus?.healthy ? t('正常') : statusLabel(doctorStatus?.state)}</strong>
        <span>{doctorSubtitle(doctorStatus)}</span>
      </article>
      <article className="diagnostic-summary-card">
        <small>{t('活动连接')}</small>
        <strong>{connections.length}</strong>
        <span>↑ {formatBytes(details?.connections.upload_total ?? 0)} · ↓ {formatBytes(details?.connections.download_total ?? 0)}</span>
      </article>
      <article className="diagnostic-summary-card">
        <small>{t('代理集合')}</small>
        <strong>{providers.length}</strong>
        <span>{t('{{count}} 个可用', { count: String(availableProviders) })}</span>
      </article>
    </section>}

    {qnapBuild ? <>
      <section className="section diagnostic-doctor-section">{doctorPanel}</section>
      <section className="section diagnostic-provider-section">{providerPanel}</section>
    </> : <section className="split"><div>{doctorPanel}</div><div>{providerPanel}</div></section>}

    <section className="section">
      <SectionTitle title={t('活动连接')} subtitle={details?.connection_error || t('{{count}} 条活动连接', { count: String(connections.length) })} />
      <div className="connection-summary-line"><span>↑ {formatBytes(details?.connections.upload_total ?? 0)}</span><span>↓ {formatBytes(details?.connections.download_total ?? 0)}</span></div>
      {connections.length ? <div className="diagnostic-connection-list">{connections.slice(0, 12).map(connection => <div className="diagnostic-connection-row" key={connection.id}><strong>{connection.rule || 'MATCH'}</strong><span>{(connection.chains ?? []).join(' → ') || connection.id.slice(0, 8)}</span></div>)}</div> : <div className="empty">{t('暂无活动连接')}</div>}
    </section>

    <section className="section">
      <SectionTitle title={t('近期日志')} subtitle={t('每个进程最多 80 行；敏感凭据已脱敏')} />
      <div className="diagnostic-log-list">
        {Object.entries(details?.logs ?? {}).map(([name, lines]) => <details key={name} className="diagnostic-log-panel"><summary><strong>{name}</strong><span>{t('{{count}} 行', { count: String(lines.length) })}</span></summary><pre>{lines.join('\n') || t('暂无日志输出')}</pre></details>)}
        {!Object.keys(details?.logs ?? {}).length && <div className="empty">{t('暂无日志')}</div>}
      </div>
    </section>

    <section className="section">
      <SectionTitle title={t('操作与恢复')} subtitle={t('恢复状态：{{state}}', { state: statusLabel(recoveryState) })} />
      {details?.operations.length ? details.operations.map(operation => <div className="row" key={operation.id}><StatusDot status={operation.state === 'failed' ? 'degraded' : operation.state === 'succeeded' ? 'running' : 'stopped'} /><div className="grow"><strong>{operationKindLabel(operation.kind)} · {statusLabel(operation.state)}</strong><small>{operation.id} · {operation.updated_at}{operation.error ? ` · ${operation.error}` : ''}</small></div></div>) : <div className="empty">{t('尚无生命周期操作记录')}</div>}
    </section>
  </>
}

function doctorSubtitle(status: DoctorRunStatus | null): string {
  if (!status) return t('读取检查结果')
  if (status.state === 'idle') return t('按需执行，不随页面刷新自动运行')
  if (status.state === 'running') return t('后台检查中')
  if (!status.current) return t('配置已变化，请重新检查')
  if (status.state === 'failed') return t('检查未完成')
  return t(status.healthy ? '检查通过' : '发现需要处理的问题')
}

function statusLabel(value?: string): string {
  switch (value) {
    case 'running':
    case 'active': return t('运行中')
    case 'ready': return t('就绪')
    case 'applied': return t('已应用')
    case 'succeeded':
    case 'complete': return t('已完成')
    case 'degraded': return t('异常')
    case 'failed': return t('失败')
    case 'interrupted': return t('待恢复')
    case 'stopped': return t('已停止')
    case 'idle': return t('空闲')
    case 'none': return t('无运行状态')
    case 'not_applied': return t('未应用')
    case 'missing': return t('缺失')
    case 'recovering': return t('恢复中')
    case 'observing': return t('观察中')
    case 'pending': return t('等待中')
    case 'loading': return t('读取中')
    case 'unknown':
    case undefined:
    case '': return t('未知')
    default: return value
  }
}

function operationKindLabel(kind: string): string {
  const normalized = kind.toLowerCase().replaceAll('_', '-')
  if (normalized.includes('restart') && normalized.includes('mihomo')) return t('重启 mihomo')
  if (normalized.includes('reload')) return t('重载配置')
  if (normalized.includes('start')) return t('启动网关')
  if (normalized.includes('stop')) return t('停止网关')
  return kind
}

function doctorCheckName(name: string): string {
  switch (name) {
    case 'root privileges': return t('Root 权限')
    case 'mihomo config validation': return t('mihomo 配置校验')
    case 'TUN device': return t('TUN 设备')
    case 'persistent runtime storage': return t('持久化运行目录')
    case 'gateway interface topology': return t('网关接口拓扑')
    case 'downstream IPv6 takeover': return t('下游 IPv6 接管')
    default:
      if (name.startsWith('interface ')) return t('接口 {{name}}', { name: name.slice('interface '.length) })
      if (name.startsWith('LAN IP bound to ')) return t('LAN IP 已绑定到 {{name}}', { name: name.slice('LAN IP bound to '.length) })
      return name
  }
}

function doctorCheckMessage(message: string): string {
  if (message === 'not found in PATH') return t('未在 PATH 中找到')
  if (message === 'path is empty') return t('路径为空')
  if (message === 'path is a directory') return t('路径指向目录')
  if (message === 'start/stop require elevated network privileges') return t('需要提升网络权限才能启动或停止')
  if (message === 'invalid IPv4 address') return t('IPv4 地址无效')
  if (message === 'not configured on interface') return t('接口上未配置该地址')
  if (message === 'isolated_lan requires separate downstream and upstream interfaces') return t('isolated_lan 要求下游与上游使用不同接口')
  if (message === 'requested but unsupported in OpenSurge for QNAP v1; clients with IPv6 may bypass the gateway') return t('QNAP v1 暂不支持下游 IPv6 接管；客户端 IPv6 流量可能绕过网关')
  if (message.endsWith(' requires gateway and upstream interfaces to match')) return t('当前模式要求网关接口与上游接口一致')
  if (message.endsWith(' uses one LAN interface')) return t('当前模式使用同一 LAN 接口')
  if (message.endsWith(' is not a character device')) return t('{{path}} 不是字符设备', { path: message.slice(0, -' is not a character device'.length) })
  if (message.endsWith(' supports durable create/rename/fsync')) return t('{{path}} 的持久化写入、重命名与 fsync 正常', { path: message.slice(0, -' supports durable create/rename/fsync'.length) })
  return message
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  const scaled = value / 1024 ** index
  return `${scaled >= 100 || index === 0 ? scaled.toFixed(0) : scaled.toFixed(1)} ${units[index]}`
}
