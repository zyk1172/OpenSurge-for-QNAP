import { useState } from 'react'
import { api, waitForOperation } from '../api'
import { ActivityCard } from '../components/ActivityCard'
import { PageHeader } from '../components/Common'
import { DeviceTrafficPanel } from '../components/DeviceTrafficPanel'
import { GatewayHealthCard } from '../components/GatewayHealthCard'
import { LiveRateCard } from '../components/LiveRateCard'
import { TrafficTrendCard } from '../components/TrafficTrendCard'
import { useDeviceTraffic } from '../hooks/useDeviceTraffic'
import type { Overview } from '../types'
import { t } from '../i18n'

const qnapBuild = import.meta.env.VITE_OPENSURGE_TARGET === 'qnap'

export function DashboardPage({ overview, onOpenNetwork, onChanged }: { overview: Overview | null; onOpenNetwork: (action: 'start' | 'stop' | 'cleanup') => void; onChanged: () => Promise<void> }) {
  const [lifecycleBusy, setLifecycleBusy] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [controlError, setControlError] = useState('')
  const running = overview?.status.gateway === 'running' || overview?.status.gateway === 'degraded'
  const stopped = overview?.status.gateway === 'stopped'
  const interrupted = overview?.status.runtime_state === 'interrupted'
  const warnings = overview?.warnings.filter(item => !(interrupted && item.includes('interrupted by a system reboot'))) ?? []
  const { traffic, history, error } = useDeviceTraffic(overview?.status.gateway)
  const rates = traffic?.gateway_rates ?? { upload: 0, download: 0 }

  const runQNAPLifecycle = async () => {
    if (!qnapBuild || lifecycleBusy || restarting || (!running && !stopped && !interrupted)) return
    const action = interrupted || running ? 'stop' : 'start'
    setLifecycleBusy(true)
    setControlError('')
    try {
      const operation = await api.gateway(action)
      await waitForOperation(operation.id)
      await onChanged()
    } catch (cause) {
      setControlError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setLifecycleBusy(false)
    }
  }

  const restartGateway = async () => {
    if (!running || interrupted || restarting || lifecycleBusy) return
    setRestarting(true)
    setControlError('')
    try {
      const operation = await api.gateway('reload')
      await waitForOperation(operation.id)
      await onChanged()
    } catch (cause) {
      setControlError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setRestarting(false)
    }
  }

  const primaryAction = interrupted ? 'cleanup' : running ? 'stop' : 'start'
  const primaryLabel = lifecycleBusy
    ? t('正在执行…')
    : t(interrupted ? '安全清理旧状态' : running ? '停止网关' : '启动网关')

  return <>
    <PageHeader
      eyebrow={qnapBuild ? 'QNAP GATEWAY' : 'CONTROL CENTER'}
      title={qnapBuild ? 'QNAP 网关总览' : '全屋网关，一眼可见'}
      description={qnapBuild ? '网关状态、实时流量与运行控制。' : 'OpenSurge 负责网关生命周期；mihomo 是当前代理引擎。'}
      action={<div className="dashboard-header-actions">
        <button className={interrupted ? 'primary' : running ? 'danger' : 'primary'} disabled={!overview || lifecycleBusy || restarting || (!running && !stopped && !interrupted)} onClick={() => qnapBuild ? void runQNAPLifecycle() : onOpenNetwork(primaryAction)}>{primaryLabel}</button>
        <button className="gateway-reload-button" type="button" disabled={!overview || !running || interrupted || restarting || lifecycleBusy} onClick={() => void restartGateway()}>{t(restarting ? '正在重启…' : '重启网关')}</button>
      </div>}
    />
    {interrupted && <div className="notice warn" role="status"><strong>{t(qnapBuild ? '上一次网关运行被容器或 NAS 重启中断。' : '上一次网关运行被 Mac 重启中断。')}</strong> {t(qnapBuild ? '请先安全清理旧状态。' : '请安全清理旧状态，再重新启动完整网关。')}</div>}
    {controlError && <div className="notice warn" role="alert"><strong>{t('网关操作失败')}</strong><p>{controlError}</p></div>}
    {warnings.length ? <div className="dashboard-warning-stack" role="status">{warnings.map(item => <div className="notice warn" key={item}>{item}</div>)}</div> : null}
    <section className="dashboard-live-grid">
      <GatewayHealthCard overview={overview} />
      <LiveRateCard direction="upload" value={rates.upload} history={history} />
      <LiveRateCard direction="download" value={rates.download} history={history} />
    </section>
    <section className="dashboard-monitor-grid">
      <ActivityCard traffic={traffic} />
      <TrafficTrendCard title="流量趋势" subtitle="近 60 秒网关流量" history={history} className="gateway-trend-card" />
    </section>
    <DeviceTrafficPanel gateway={overview?.status.gateway} traffic={traffic} history={history} error={error} />
  </>
}
