import type { Diagnostics } from '../types'

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
  rule: string
  rule_payload: string
  chains: string[]
  upload: number
  download: number
}

export type DomainSummary = {
  key: string
  host: string
  connections: number
  upload: number
  download: number
  rules: string[]
  chains: string[]
  last_seen: string
  needs_review: boolean
  review_reason: string
}

const historyRetentionMs = 7 * 24 * 60 * 60 * 1000

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
  const start = typeof (connection as DiagnosticConnection & { start?: string }).start === 'string'
    ? (connection as DiagnosticConnection & { start?: string }).start ?? ''
    : ''
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
    rule: connection.rule ?? '',
    rule_payload: (connection as DiagnosticConnection & { rule_payload?: string }).rule_payload ?? '',
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

export function aggregateDomains(records: TrafficRecord[]): DomainSummary[] {
  const groups = new Map<string, {
    host: string
    ids: Set<string>
    upload: number
    download: number
    rules: Set<string>
    chains: Set<string>
    lastSeen: string
  }>()
  for (const record of records) {
    const host = record.host || record.destination_ip
    if (!host) continue
    const key = host.toLowerCase()
    const group = groups.get(key) ?? {
      host,
      ids: new Set<string>(),
      upload: 0,
      download: 0,
      rules: new Set<string>(),
      chains: new Set<string>(),
      lastSeen: record.last_seen,
    }
    if (!group.ids.has(record.key)) {
      group.ids.add(record.key)
      group.upload += record.upload
      group.download += record.download
    }
    if (record.rule || record.rule_payload) group.rules.add([record.rule, record.rule_payload].filter(Boolean).join(':'))
    if (record.chains.length) group.chains.add(record.chains.join(' → '))
    if (Date.parse(record.last_seen) > Date.parse(group.lastSeen)) group.lastSeen = record.last_seen
    groups.set(key, group)
  }
  return [...groups.entries()].map(([key, group]) => {
    const rules = [...group.rules]
    const chains = [...group.chains]
    const needsReview = rules.length > 1 || chains.length > 1
    return {
      key,
      host: group.host,
      connections: group.ids.size,
      upload: group.upload,
      download: group.download,
      rules,
      chains,
      last_seen: group.lastSeen,
      needs_review: needsReview,
      review_reason: needsReview ? '同一目标在观察窗口内命中过多个规则或出口，建议检查是否符合预期。' : '',
    }
  }).sort((left, right) => (right.upload + right.download) - (left.upload + left.download) || right.connections - left.connections)
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
  return [record.host, record.source_ip, record.destination_ip, record.destination_port, record.rule, record.rule_payload, ...record.chains].join(' ').toLowerCase()
}
