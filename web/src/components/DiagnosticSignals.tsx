import type { Diagnostics } from '../types'
import { t } from '../i18n'
import { SectionTitle } from './Common'

export function DiagnosticSignals({ signals }: { signals: Diagnostics['signals'] }) {
  if (!signals) return null
  return <section className="section">
    <SectionTitle title={t('告警归因与汇总')} subtitle={t('仅统计近期日志样本；来源 IP 不能确定应用，连接失败不等于网关故障。')} />
    <p>{t('样本 {{lines}} 行 · 连接失败 {{connections}} 次 · 其他告警 {{others}} 次', { lines: String(signals.sampled_lines), connections: String(signals.connection_failures), others: String(signals.other_warnings) })}</p>
    <div className="diagnostic-log-list">
      {signals.groups.map(group => <div className="row" key={JSON.stringify([group.emitter, group.kind, group.protocol, group.source_ip, group.target, group.policy, group.rule, group.reason])}>
        <strong>{t('{{count}} 次', { count: String(group.count) })}</strong>
        <div className="grow">
          <strong>{signalKind(group.kind)} · {signalReason(group.reason)}</strong>
          <small>{group.emitter}{group.protocol ? ` / ${group.protocol.toUpperCase()}` : ''} · {group.source_ip || t('来源未知')} → {group.target || t('目标未知')}</small>
          {group.policy && <small>{t('命中策略：{{policy}}；规则：{{rule}}', { policy: group.policy, rule: group.rule || '—' })}</small>}
        </div>
      </div>)}
      {!signals.groups.length && <div className="empty">{t('近期日志样本没有告警；完整运行状态请结合网关和环境检查。')}</div>}
    </div>
    {signals.omitted_groups > 0 && <p>{t('另有 {{count}} 组未展示，计数已包含在样本统计中。', { count: String(signals.omitted_groups) })}</p>}
  </section>
}

function signalKind(kind: string) {
  switch (kind) {
    case 'connection': return t('连接失败')
    case 'service': return t('服务告警')
    case 'dns': return t('DNS 告警')
    default: return t('未分类告警')
  }
}

function signalReason(reason: string) {
  switch (reason) {
    case 'timeout': return t('超时')
    case 'connection_refused': return t('连接被拒绝')
    case 'connection_reset': return t('连接被重置')
    case 'dns_resolution': return t('DNS 解析失败')
    case 'unreachable': return t('网络不可达')
    case 'tls': return t('TLS 错误')
    default: return t('其他错误')
  }
}
