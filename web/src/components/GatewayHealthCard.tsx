import { statusLabel } from '../status'
import type { Overview } from '../types'
import { StatusDot } from './Common'
import { t } from '../i18n'

const qnapBuild = import.meta.env.VITE_OPENSURGE_TARGET === 'qnap'

export function GatewayHealthCard({ overview }: { overview: Overview | null }) {
  const status = overview?.status
  const running = status?.gateway === 'running'
  const configState = t(overview?.drift ? running ? '待重载' : '下次启动应用' : '已同步')
  return <article className="gateway-health-card" aria-label={t('网关状态')}>
    <div className="gateway-health-main">
      <div className="gateway-health-identity"><div className="orb"><span /></div><div><small>GATEWAY</small><h2>{statusLabel(status?.gateway)}</h2><p>{status?.interface ?? '—'} · {status?.lan_ip ?? t('等待状态')}</p></div></div>
      <div className="gateway-health-meta">
        <GatewayMeta label={t('接管模式')} value={topologyLabel(overview?.topology)} />
        {qnapBuild && <GatewayMeta label={t('数据面')} value={status?.data_plane ?? '—'} tone={status?.routing === 'applied' ? 'ok' : status?.routing === 'missing' || status?.routing === 'unknown' ? 'warn' : ''} />}
        <GatewayMeta label={t('配置状态')} value={configState} tone={overview?.drift ? 'warn' : 'ok'} />
      </div>
    </div>
    <div className="gateway-service-strip" aria-label={t('核心服务状态')}>
      <ServiceState label={status?.dhcp_enabled === false ? 'DNS' : 'DHCP / DNS'} state={status?.dhcp} />
      <ServiceState label="mihomo" state={status?.mihomo} />
      <ServiceState label={status?.tun_interface ? `TUN · ${status.tun_interface}` : 'TUN'} state={status?.tun} />
      {qnapBuild
        ? <ServiceState label={t('策略路由')} state={status?.routing} displayState={routingStatusLabel(status?.routing)} detail={status?.routing_error} />
        : <ServiceState label="PF Anchor" state={status?.pf_anchor} />}
      <ServiceState label={t('IPv4 转发')} state={status?.forwarding} />
    </div>
  </article>
}

function GatewayMeta({ label, value, tone = '' }: { label: string; value: string; tone?: 'ok' | 'warn' | '' }) {
  return <span className={`gateway-meta ${tone}`.trim()}><small>{t(label)}</small><strong>{t(value)}</strong></span>
}

function ServiceState({ label, state = '—', displayState, detail }: { label: string; state?: string; displayState?: string; detail?: string }) {
  return <span className="gateway-service-state" title={detail || undefined}><StatusDot status={state} /><span><strong>{t(label)}</strong><small>{t(displayState ?? state)}</small>{detail && <small>{detail}</small>}</span></span>
}

function routingStatusLabel(state?: string) {
  if (state === 'applied') return '已应用'
  if (state === 'missing') return '缺失'
  if (state === 'unknown') return '无法确认'
  if (state === 'not_applied') return '未应用'
  return '—'
}

function topologyLabel(topology?: string) {
  if (topology === 'same_wifi_dhcp') return t('局域网 DHCP 接管')
  if (topology === 'same_lan') return t('旁路由模式')
  if (topology === 'isolated_lan') return t('独立下游 LAN')
  return t('IPv4 网关')
}
