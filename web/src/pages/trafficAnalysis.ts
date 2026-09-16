import type { Diagnostics, ProxyGroup, ProxyHealthEntry } from '../types'

export type DiagnosticConnection = Diagnostics['connections']['connections'][number]

export type TrafficRecord = {
  key: string
  id: string
  first_seen: string
  last_seen: string
  start: string
  host: string
  source_ip: string
  source_port: string
  destination_ip: string
  destination_port: string
  network: string
  connection_type: string
  process: string
  rule: string
  rule_payload: string
  chains: string[]
  upload: number
  download: number
}

export type TrafficRouteKind = 'proxy' | 'direct' | 'reject' | 'dns' | 'lan' | 'unknown'
export type TrafficViewFilter = 'focus' | 'proxy' | 'direct' | 'reject' | 'all'

export type TrafficTargetSummary = {
  key: string
  host: string
  connections: number
  upload: number
  download: number
  rules: string[]
  chains: string[]
  routes: TrafficRouteKind[]
  dominant_route: TrafficRouteKind
  last_seen: string
}

export type TrafficRouteSummary = {
  route: TrafficRouteKind
  connections: number
  upload: number
  download: number
}

export type TrafficRuleSummary = {
  key: string
  label: string
  route: TrafficRouteKind
  connections: number
  upload: number
  download: number
  last_seen: string
}

export type TrafficIssue = {
  id: string
  kind: 'route_flap' | 'unhealthy_proxy'
  severity: 'warning' | 'error'
  target: string
  source_ip: string
  title: string
  detail: string
  evidence: string[]
  related_keys: string[]
  last_seen: string
}

export type RouteExplanation = {
  route: TrafficRouteKind
  target: string
  matched_rule: string
  actual_chain: string[]
  group_selections: Array<{ group: string; selected: string }>
  observed_egress: string
}

const historyRetentionMs = 7 * 24 * 60 * 60 * 1000
const routeFlapWindowMs = 15 * 60 * 1000

function metadataString(metadata: Record<string, unknown> | undefined, ...keys: string[]): string {
  if (!metadata) return ''
  for (const key of keys) {
    const value = metadata[key]
    if (typeof value === 'string' && value.trim()) return value.trim()
    if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  }
  return ''
}

export function normalizeTrafficConnection(connection: DiagnosticConnection, now = new Date().toISOString()): TrafficRecord {
  const metadata = connection.metadata ?? {}
  const enriched = connection as DiagnosticConnection & { start?: string; rule_payload?: string; rulePayload?: string }
  const start = typeof enriched.start === 'string' ? enriched.start : ''
  const host = metadataString(metadata, 'host', 'dnsModeHost', 'sniffHost')
  return {
    key: `${connection.id}|${start}`,
    id: connection.id,
    first_seen: start || now,
    last_seen: now,
    start,
    host,
    source_ip: metadataString(metadata, 'sourceIP', 'sourceIp', 'srcIP', 'srcIp'),
    source_port: metadataString(metadata, 'sourcePort', 'srcPort'),
    destination_ip: metadataString(metadata, 'destinationIP', 'destinationIp', 'dstIP', 'dstIp'),
    destination_port: metadataString(metadata, 'destinationPort', 'dstPort'),
    network: metadataString(metadata, 'network'),
    connection_type: metadataString(metadata, 'type'),
    process: metadataString(metadata, 'process', 'processName', 'processPath'),
    rule: connection.rule ?? '',
    rule_payload: enriched.rule_payload ?? enriched.rulePayload ?? '',
    chains: [...(connection.chains ?? [])],
    upload: Number.isFinite(connection.upload) ? connection.upload : 0,
    download: Number.isFinite(connection.download) ? connection.download : 0,
  }
}

export function mergeTrafficHistory(previous: TrafficRecord[], current: TrafficRecord[], now = Date.now(), limit = 3000): TrafficRecord[] {
  const byKey = new Map(previous.map(record => [record.key, record]))
  for (const incoming of current) {
    const old = byKey.get(incoming.key)
    byKey.set(incoming.key, old ? {
      ...old,
      ...incoming,
      first_seen: old.first_seen || incoming.first_seen,
      upload: Math.max(old.upload, incoming.upload),
      download: Math.max(old.download, incoming.download),
    } : incoming)
  }
  return [...byKey.values()]
    .filter(record => {
      const seen = Date.parse(record.last_seen)
      return Number.isFinite(seen) && now - seen <= historyRetentionMs
    })
    .sort((left, right) => Date.parse(right.last_seen) - Date.parse(left.last_seen))
    .slice(0, limit)
}

export function classifyTrafficRoute(record: TrafficRecord): TrafficRouteKind {
  const routeText = [record.rule, record.rule_payload, ...record.chains].join(' ').toUpperCase()
  const destination = record.destination_ip.trim()
  const connectionType = record.connection_type.toLowerCase()
  if (routeText.includes('REJECT') || routeText.includes('REJECT-DROP')) return 'reject'
  if (record.chains.some(chain => /(^|[-_])DNS([-_]|$)|DNS-OUT/i.test(chain)) || connectionType === 'dns' || record.destination_port === '53') return 'dns'
  if (isPrivateDestination(destination)) return 'lan'
  if (record.chains.some(chain => chain.trim().toUpperCase() === 'DIRECT') || routeText.includes(' DIRECT')) return 'direct'
  if (record.chains.length > 0) return 'proxy'
  return 'unknown'
}

export function routeIsQuiet(route: TrafficRouteKind): boolean {
  return route === 'direct' || route === 'dns' || route === 'lan'
}

export function aggregateTargets(records: TrafficRecord[]): TrafficTargetSummary[] {
  const groups = new Map<string, {
    host: string
    ids: Set<string>
    upload: number
    download: number
    rules: Set<string>
    chains: Set<string>
    routes: Map<TrafficRouteKind, number>
    lastSeen: string
  }>()
  for (const record of records) {
    const host = targetLabel(record)
    if (!host) continue
    const key = host.toLowerCase()
    const group = groups.get(key) ?? {
      host,
      ids: new Set<string>(),
      upload: 0,
      download: 0,
      rules: new Set<string>(),
      chains: new Set<string>(),
      routes: new Map<TrafficRouteKind, number>(),
      lastSeen: record.last_seen,
    }
    if (!group.ids.has(record.key)) {
      group.ids.add(record.key)
      group.upload += record.upload
      group.download += record.download
      const route = classifyTrafficRoute(record)
      group.routes.set(route, (group.routes.get(route) ?? 0) + 1)
    }
    if (record.rule || record.rule_payload) group.rules.add(ruleLabel(record))
    if (record.chains.length) group.chains.add(record.chains.join(' → '))
    if (Date.parse(record.last_seen) > Date.parse(group.lastSeen)) group.lastSeen = record.last_seen
    groups.set(key, group)
  }
  return [...groups.entries()].map(([key, group]) => ({
    key,
    host: group.host,
    connections: group.ids.size,
    upload: group.upload,
    download: group.download,
    rules: [...group.rules],
    chains: [...group.chains],
    routes: [...group.routes.keys()],
    dominant_route: [...group.routes.entries()].sort((left, right) => right[1] - left[1])[0]?.[0] ?? 'unknown',
    last_seen: group.lastSeen,
  })).sort((left, right) => (right.upload + right.download) - (left.upload + left.download) || right.connections - left.connections)
}

// Compatibility for older imports. This intentionally no longer attaches an
// anomaly verdict to ordinary rule/chain variation.
export const aggregateDomains = aggregateTargets

export function aggregateTrafficRoutes(records: TrafficRecord[]): TrafficRouteSummary[] {
  const order: TrafficRouteKind[] = ['proxy', 'direct', 'reject', 'dns', 'lan', 'unknown']
  const groups = new Map<TrafficRouteKind, TrafficRouteSummary>()
  for (const record of records) {
    const route = classifyTrafficRoute(record)
    const summary = groups.get(route) ?? { route, connections: 0, upload: 0, download: 0 }
    summary.connections += 1
    summary.upload += record.upload
    summary.download += record.download
    groups.set(route, summary)
  }
  return order.map(route => groups.get(route) ?? { route, connections: 0, upload: 0, download: 0 })
}

export function aggregateRuleHits(records: TrafficRecord[], limit = 8): TrafficRuleSummary[] {
  const groups = new Map<string, TrafficRuleSummary>()
  for (const record of records) {
    const label = ruleLabel(record)
    const route = classifyTrafficRoute(record)
    const key = `${label}|${route}`
    const current = groups.get(key) ?? { key, label, route, connections: 0, upload: 0, download: 0, last_seen: record.last_seen }
    current.connections += 1
    current.upload += record.upload
    current.download += record.download
    if (Date.parse(record.last_seen) > Date.parse(current.last_seen)) current.last_seen = record.last_seen
    groups.set(key, current)
  }
  return [...groups.values()]
    .sort((left, right) => right.connections - left.connections || (right.upload + right.download) - (left.upload + left.download))
    .slice(0, limit)
}

export function detectTrafficIssues(
  history: TrafficRecord[],
  active: TrafficRecord[],
  health: ProxyHealthEntry[] = [],
  now = Date.now(),
): TrafficIssue[] {
  const issues: TrafficIssue[] = []
  const recent = history.filter(record => {
    const seen = Date.parse(record.last_seen)
    return Number.isFinite(seen) && now - seen <= routeFlapWindowMs
  })

  const routeGroups = new Map<string, TrafficRecord[]>()
  for (const record of recent) {
    if (!record.source_ip) continue
    const route = classifyTrafficRoute(record)
    if (route !== 'direct' && route !== 'proxy') continue
    const key = `${record.source_ip}|${targetLabel(record).toLowerCase()}|${record.network.toLowerCase()}`
    const group = routeGroups.get(key) ?? []
    group.push(record)
    routeGroups.set(key, group)
  }
  for (const records of routeGroups.values()) {
    const direct = records.filter(record => classifyTrafficRoute(record) === 'direct')
    const proxy = records.filter(record => classifyTrafficRoute(record) === 'proxy')
    // Two observations on each side avoids flagging a one-off policy edit or a
    // stale connection that happened to overlap a route change.
    if (direct.length < 2 || proxy.length < 2) continue
    const latest = [...records].sort((left, right) => Date.parse(right.last_seen) - Date.parse(left.last_seen))[0]
    issues.push({
      id: `route-flap:${latest.source_ip}:${targetLabel(latest).toLowerCase()}:${latest.network}`,
      kind: 'route_flap',
      severity: 'warning',
      target: targetLabel(latest),
      source_ip: latest.source_ip,
      title: '同一来源的分流结果近期反复变化',
      detail: '同一来源、目标和协议在 15 分钟内多次同时出现 DIRECT 与 Proxy。普通策略组换节点不会触发此项。',
      evidence: [
        `DIRECT × ${direct.length}`,
        `Proxy × ${proxy.length}`,
        uniqueStrings(records.map(ruleLabel)).slice(0, 3).join(' · '),
      ].filter(Boolean),
      related_keys: records.map(record => record.key),
      last_seen: latest.last_seen,
    })
  }

  const healthMap = new Map(health.map(entry => [entry.name.toLowerCase(), entry]))
  const unhealthy = new Set(['unreachable', 'timeout', 'error'])
  const unhealthyGroups = new Map<string, { record: TrafficRecord; names: Set<string>; keys: Set<string> }>()
  for (const record of active) {
    if (classifyTrafficRoute(record) !== 'proxy') continue
    const badNames = record.chains.filter(chain => {
      const entry = healthMap.get(chain.toLowerCase())
      return entry ? unhealthy.has(entry.status) : false
    })
    if (!badNames.length) continue
    const groupKey = `${record.source_ip}|${targetLabel(record).toLowerCase()}`
    const group = unhealthyGroups.get(groupKey) ?? { record, names: new Set<string>(), keys: new Set<string>() }
    badNames.forEach(name => group.names.add(name))
    group.keys.add(record.key)
    if (Date.parse(record.last_seen) > Date.parse(group.record.last_seen)) group.record = record
    unhealthyGroups.set(groupKey, group)
  }
  for (const group of unhealthyGroups.values()) {
    const names = [...group.names]
    issues.push({
      id: `unhealthy-proxy:${group.record.source_ip}:${targetLabel(group.record).toLowerCase()}`,
      kind: 'unhealthy_proxy',
      severity: 'error',
      target: targetLabel(group.record),
      source_ip: group.record.source_ip,
      title: '当前连接正在经过不可用代理',
      detail: '实际连接链命中了健康检查已明确标记为不可达、超时或错误的代理节点。',
      evidence: names,
      related_keys: [...group.keys],
      last_seen: group.record.last_seen,
    })
  }

  return issues.sort((left, right) => {
    if (left.severity !== right.severity) return left.severity === 'error' ? -1 : 1
    return Date.parse(right.last_seen) - Date.parse(left.last_seen)
  })
}

export function recordMatchesFilter(record: TrafficRecord, filter: TrafficViewFilter, issues: TrafficIssue[] = []): boolean {
  const route = classifyTrafficRoute(record)
  if (filter === 'all') return true
  if (filter === 'proxy') return route === 'proxy'
  if (filter === 'direct') return route === 'direct'
  if (filter === 'reject') return route === 'reject'
  const issueKeys = new Set(issues.flatMap(issue => issue.related_keys))
  return route === 'proxy' || route === 'reject' || route === 'unknown' || issueKeys.has(record.key)
}

export function buildRouteExplanation(record: TrafficRecord, groups: ProxyGroup[]): RouteExplanation {
  const groupMap = new Map(groups.map(group => [group.name.toLowerCase(), group]))
  const groupSelections = record.chains.flatMap(chain => {
    const group = groupMap.get(chain.toLowerCase())
    return group ? [{ group: group.name, selected: group.selected }] : []
  })
  const route = classifyTrafficRoute(record)
  return {
    route,
    target: targetLabel(record),
    matched_rule: ruleLabel(record),
    actual_chain: record.chains,
    group_selections: dedupeGroupSelections(groupSelections),
    observed_egress: observedEgress(record, route, groups),
  }
}

export type RuleMatchMode = 'domain' | 'domain-suffix' | 'ip'

export function buildOverrideRule(record: TrafficRecord, mode: RuleMatchMode, policy: string): string {
  const target = policy.trim()
  if (!target) return ''
  if (mode === 'ip') {
    const ip = record.destination_ip.trim()
    if (!ip || ip.includes(':')) return ''
    return `IP-CIDR,${ip}/32,${target},no-resolve`
  }
  const host = record.host.trim().replace(/^\.+|\.+$/g, '')
  if (!host) return ''
  return `${mode === 'domain-suffix' ? 'DOMAIN-SUFFIX' : 'DOMAIN'},${host},${target}`
}

export function connectionSearchText(record: TrafficRecord): string {
  return [
    record.host,
    record.source_ip,
    record.destination_ip,
    record.destination_port,
    record.process,
    record.rule,
    record.rule_payload,
    classifyTrafficRoute(record),
    ...record.chains,
  ].join(' ').toLowerCase()
}

export function targetLabel(record: TrafficRecord): string {
  return record.host || record.destination_ip || '—'
}

export function ruleLabel(record: TrafficRecord): string {
  return [record.rule, record.rule_payload].filter(Boolean).join(':') || 'MATCH'
}

function observedEgress(record: TrafficRecord, route: TrafficRouteKind, groups: ProxyGroup[]): string {
  if (route === 'direct') return 'DIRECT'
  if (route === 'reject') return 'REJECT'
  if (route === 'dns') return 'DNS'
  if (route === 'lan') return 'LAN'
  if (route === 'unknown') return 'UNKNOWN'
  const groupNames = new Set(groups.map(group => group.name.toLowerCase()))
  return record.chains.find(chain => !groupNames.has(chain.toLowerCase())) ?? record.chains[0] ?? 'PROXY'
}

function dedupeGroupSelections(values: Array<{ group: string; selected: string }>): Array<{ group: string; selected: string }> {
  const seen = new Set<string>()
  return values.filter(value => {
    const key = value.group.toLowerCase()
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

function isPrivateDestination(value: string): boolean {
  if (!value) return false
  const lower = value.toLowerCase()
  if (lower === '::1' || lower.startsWith('fc') || lower.startsWith('fd') || lower.startsWith('fe8') || lower.startsWith('fe9') || lower.startsWith('fea') || lower.startsWith('feb')) return true
  const parts = value.split('.').map(part => Number(part))
  if (parts.length !== 4 || parts.some(part => !Number.isInteger(part) || part < 0 || part > 255)) return false
  if (parts[0] === 10 || parts[0] === 127) return true
  if (parts[0] === 169 && parts[1] === 254) return true
  if (parts[0] === 172 && parts[1] >= 16 && parts[1] <= 31) return true
  if (parts[0] === 192 && parts[1] === 168) return true
  return false
}

function uniqueStrings(values: string[]): string[] {
  return [...new Set(values.map(value => value.trim()).filter(Boolean))]
}
