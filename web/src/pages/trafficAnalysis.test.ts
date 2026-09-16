import { describe, expect, it } from 'vitest'
import {
  aggregateRuleHits,
  aggregateTargets,
  buildOverrideRule,
  buildRouteExplanation,
  classifyTrafficRoute,
  detectTrafficIssues,
  mergeTrafficHistory,
  normalizeTrafficConnection,
  recordMatchesFilter,
} from './trafficAnalysis'

const now = '2026-09-12T04:40:00.000Z'

function connection(id: string, host: string, rule: string, payload: string, chains: string[], overrides: Record<string, unknown> = {}) {
  return normalizeTrafficConnection({
    id,
    upload: 10,
    download: 20,
    rule,
    chains,
    metadata: {
      host,
      sourceIP: '192.168.2.240',
      sourcePort: '50000',
      destinationIP: '93.184.216.34',
      destinationPort: '443',
      network: 'tcp',
      ...overrides,
    },
    rule_payload: payload,
  } as never, now)
}

describe('traffic analysis helpers', () => {
  it('normalizes live Mihomo metadata for the activity list', () => {
    const record = connection('a', 'api.example.com', 'DomainSuffix', 'example.com', ['HK', 'Proxy'], { process: 'curl' })
    expect(record.host).toBe('api.example.com')
    expect(record.source_ip).toBe('192.168.2.240')
    expect(record.destination_port).toBe('443')
    expect(record.rule_payload).toBe('example.com')
    expect(record.chains).toEqual(['HK', 'Proxy'])
    expect(record.process).toBe('curl')
  })

  it('updates an existing live connection instead of duplicating samples', () => {
    const first = connection('a', 'api.example.com', 'DomainSuffix', 'example.com', ['DIRECT'])
    const updated = { ...first, download: 120, last_seen: '2026-09-12T04:40:05.000Z' }
    const history = mergeTrafficHistory([first], [updated], Date.parse('2026-09-12T04:40:05.000Z'))
    expect(history).toHaveLength(1)
    expect(history[0].download).toBe(120)
  })

  it('classifies ordinary direct, proxy, reject, DNS and LAN traffic without treating them as anomalies', () => {
    expect(classifyTrafficRoute(connection('direct', 'cn.example', 'GeoSite', 'cn', ['DIRECT']))).toBe('direct')
    expect(classifyTrafficRoute(connection('proxy', 'global.example', 'RuleSet', 'global', ['HK-01', 'Proxy']))).toBe('proxy')
    expect(classifyTrafficRoute(connection('reject', 'ads.example', 'RuleSet', 'ads', ['REJECT']))).toBe('reject')
    expect(classifyTrafficRoute(connection('dns', 'resolver.example', 'Match', '', ['dns-out'], { destinationPort: '53' }))).toBe('dns')
    expect(classifyTrafficRoute(connection('lan', '', 'Match', '', ['DIRECT'], { destinationIP: '192.168.2.1' }))).toBe('lan')
  })

  it('does not flag a target merely because normal DIRECT traffic matched different rules', () => {
    const first = connection('a', 'www.qq.com', 'GeoSite', 'cn', ['DIRECT'])
    const second = { ...connection('b', 'www.qq.com', 'GeoIP', 'CN', ['DIRECT']), last_seen: '2026-09-12T04:41:00.000Z' }
    const [summary] = aggregateTargets([first, second])
    const issues = detectTrafficIssues([first, second], [second], [], Date.parse('2026-09-12T04:42:00.000Z'))
    expect(summary.routes).toEqual(['direct'])
    expect(summary.rules).toHaveLength(2)
    expect(issues).toHaveLength(0)
  })

  it('does not flag normal selector or url-test node changes when the route remains proxied', () => {
    const first = connection('a', 'api.example.com', 'RuleSet', 'global', ['HK-01', 'Proxy'])
    const second = { ...connection('b', 'api.example.com', 'RuleSet', 'global', ['HK-02', 'Proxy']), last_seen: '2026-09-12T04:41:00.000Z' }
    const issues = detectTrafficIssues([first, second], [second], [], Date.parse('2026-09-12T04:42:00.000Z'))
    expect(issues).toHaveLength(0)
  })

  it('requires repeated recent DIRECT and Proxy observations before reporting route flapping', () => {
    const records = [
      connection('d1', 'api.example.com', 'Domain', 'api.example.com', ['DIRECT']),
      { ...connection('p1', 'api.example.com', 'Domain', 'api.example.com', ['HK-01', 'Proxy']), last_seen: '2026-09-12T04:41:00.000Z' },
      { ...connection('d2', 'api.example.com', 'Domain', 'api.example.com', ['DIRECT']), last_seen: '2026-09-12T04:42:00.000Z' },
      { ...connection('p2', 'api.example.com', 'Domain', 'api.example.com', ['HK-02', 'Proxy']), last_seen: '2026-09-12T04:43:00.000Z' },
    ]
    const issues = detectTrafficIssues(records, [records[3]], [], Date.parse('2026-09-12T04:44:00.000Z'))
    expect(issues).toHaveLength(1)
    expect(issues[0].kind).toBe('route_flap')
    expect(issues[0].evidence).toContain('DIRECT × 2')
    expect(issues[0].evidence).toContain('Proxy × 2')
  })

  it('reports an active proxy connection only when its observed chain contains an unhealthy proxy', () => {
    const record = connection('p1', 'api.example.com', 'RuleSet', 'global', ['HK-01', 'Proxy'])
    const issues = detectTrafficIssues([record], [record], [
      { name: 'HK-01', type: 'ss', udp: true, status: 'timeout', probeable: true },
      { name: 'Proxy', type: 'select', udp: true, status: 'not_applicable', probeable: false },
    ], Date.parse(now))
    expect(issues).toHaveLength(1)
    expect(issues[0].kind).toBe('unhealthy_proxy')
    expect(issues[0].evidence).toEqual(['HK-01'])
  })

  it('hides quiet DIRECT, DNS and LAN traffic from the focus filter while retaining access through explicit filters', () => {
    const direct = connection('direct', 'cn.example', 'GeoSite', 'cn', ['DIRECT'])
    const proxy = connection('proxy', 'global.example', 'RuleSet', 'global', ['HK-01', 'Proxy'])
    const dns = connection('dns', 'resolver.example', 'Match', '', ['dns-out'], { destinationPort: '53' })
    expect(recordMatchesFilter(direct, 'focus')).toBe(false)
    expect(recordMatchesFilter(dns, 'focus')).toBe(false)
    expect(recordMatchesFilter(proxy, 'focus')).toBe(true)
    expect(recordMatchesFilter(direct, 'direct')).toBe(true)
    expect(recordMatchesFilter(direct, 'all')).toBe(true)
  })

  it('keeps route explanation factual and resolves current policy-group selections', () => {
    const record = connection('proxy', 'global.example', 'RuleSet', 'global', ['HK-01', 'Proxy'])
    const explanation = buildRouteExplanation(record, [{ name: 'Proxy', type: 'select', selected: 'HK-01', options: ['HK-01', 'HK-02'] }])
    expect(explanation.route).toBe('proxy')
    expect(explanation.matched_rule).toBe('RuleSet:global')
    expect(explanation.actual_chain).toEqual(['HK-01', 'Proxy'])
    expect(explanation.group_selections).toEqual([{ group: 'Proxy', selected: 'HK-01' }])
    expect(explanation.observed_egress).toBe('HK-01')
  })

  it('aggregates rule hits by matched rule and route instead of assigning a verdict', () => {
    const direct = connection('a', 'www.qq.com', 'GeoSite', 'cn', ['DIRECT'])
    const proxy = connection('b', 'api.example.com', 'RuleSet', 'global', ['HK-01', 'Proxy'])
    const hits = aggregateRuleHits([direct, proxy])
    expect(hits.map(hit => hit.label)).toEqual(expect.arrayContaining(['GeoSite:cn', 'RuleSet:global']))
    expect(hits.find(hit => hit.label === 'GeoSite:cn')?.route).toBe('direct')
  })

  it('builds high-priority rules without touching the subscription source', () => {
    const record = connection('a', 'api.example.com', 'DomainSuffix', 'example.com', ['DIRECT'])
    expect(buildOverrideRule(record, 'domain', 'Proxy')).toBe('DOMAIN,api.example.com,Proxy')
    expect(buildOverrideRule(record, 'domain-suffix', 'Proxy')).toBe('DOMAIN-SUFFIX,api.example.com,Proxy')
    expect(buildOverrideRule(record, 'ip', 'DIRECT')).toBe('IP-CIDR,93.184.216.34/32,DIRECT,no-resolve')
  })
})
