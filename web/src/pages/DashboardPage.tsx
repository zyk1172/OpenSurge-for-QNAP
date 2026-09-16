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

export function DashboardPage({ overview, onOpenNetwork }: { overview: Overview | null; onOpenNetwork: (action: 'start' | 'stop' | 'cleanup') => void }) {
  const [restarting, setRestarting] = useState(false)
  const [restartError, setRestartError] = useState('')
  const running = overview?.status.gateway === 'running' || overview?.status.gateway === 'degraded'
  const stopped = overview?.status.gateway === 'stopped'
  const interrupted = overview?.status.runtime_state === 'interrupted'
  const warnings = overview?.warnings.filter(item => !(interrupted && item.includes('interrupted by a system reboot'))) ?? []
  const { traffic, history, error } = useDeviceTraffic(overview?.status.gateway)
  const rates = traffic?.gateway_rates ?? { upload: 0, download: 0 }

  const restartGateway = async () => {
    if (!running || interrupted || restarting) return
    setRestarting(true)
    setRestartError('')
    try {
      const operation = await api.gateway('reload')
      await waitForOperation(operation.id)
    } catch (cause) {
      setRestartError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setRestarting(false)
    }
  }

  return <>
    <PageHeader
      eyebrow={qnapBuild ? 'QNAP GATEWAY' : 'CONTROL CENTER'}
      title={qnapBuild ? 'QNAP 网关，一眼可见' : '全屋网关，一眼可见'}
      description="OpenSurge 负责网关生命周期；mihomo 是当前代理引擎。"
      action={<div className="dashboard-header-actions">
        <button className={interrupted ? 'primary' : running ? 'danger' : 'primary'} disabled={!overview || restarting || (!running && !stopped)} onClick={() => onOpenNetwork(interrupted ? 'cleanup' : running ? 'stop' : 'start')}>{t(interrupted ? '安全清理旧状态' : running ? '停止网关' : '启动网关')}</button>
        <button className="gateway-reload-button" type="button" disabled={!overview || !running || interrupted || restarting} onClick={() => void restartGateway()}>{t(restarting ? '正在重启…' : '重启网关')}</button>
      </div>}
    />
    {interrupted && <div className="notice warn" role="status"><strong>{t(qnapBuild ? '上一次网关运行被容器或 NAS 重启中断。' : '上一次网关运行被 Mac 重启中断。')}</strong> {t('请安全清理旧状态，再重新启动完整网关。')}</div>}
    {restartError && <div className="notice warn" role="alert"><strong>{t('网关重启失败')}</strong><p>{restartError}</p></div>}
    {warnings.length ? <div className="dashboard-warning-stack" role="status">{warnings.map(item => <div className="notice warn" key={item}>{item}</div>)}</div> : null}
    <section className="dashboard-live-grid">
      <GatewayHealthCard overview={overview} />
      <LiveRateCard direction="upload" value={rates.upload} history={history} />
      <LiveRateCard direction="download" value={rates.download} history={history} />
    </section>
    <section className="dashboard-monitor-grid">
      <ActivityCard traffic={traffic} />
      <TrafficTrendCard title="流量趋势" subtitle="网关全部 mihomo 活跃连接 · 近 60 秒内存采样" history={history} className="gateway-trend-card" />
    </section>
    <DeviceTrafficPanel gateway={overview?.status.gateway} traffic={traffic} history={history} error={error} />
  </>
}
