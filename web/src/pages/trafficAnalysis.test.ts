import { describe, expect, it } from 'vitest'
import { aggregateDomains, buildOverrideRule, mergeTrafficHistory, normalizeTrafficConnection } from './trafficAnalysis'

const now = '2026-09-12T04:40:00.000Z'

function connection(id: string, host: string, rule: string, payload: string, chains: string[]) {
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
    },
    rule_payload: payload,
  } as never, now)
}

describe('traffic analysis helpers', () => {
  it('normalizes live Mihomo metadata for the activity list', () => {
    const record = connection('a', 'api.example.com', 'DomainSuffix', 'example.com', ['Proxy', 'HK'])
    expect(record.host).toBe('api.example.com')
    expect(record.source_ip).toBe('192.168.2.240')
    expect(record.destination_port).toBe('443')
    expect(record.rule_payload).toBe('example.com')
    expect(record.chains).toEqual(['Proxy', 'HK'])
  })

  it('updates an existing live connection instead of duplicating samples', () => {
    const first = connection('a', 'api.example.com', 'DomainSuffix', 'example.com', ['DIRECT'])
    const updated = { ...first, download: 120, last_seen: '2026-09-12T04:40:05.000Z' }
    const history = mergeTrafficHistory([first], [updated], Date.parse('2026-09-12T04:40:05.000Z'))
    expect(history).toHaveLength(1)
    expect(history[0].download).toBe(120)
  })

  it('flags a domain for review when observed routes disagree', () => {
    const direct = connection('a', 'api.example.com', 'DomainSuffix', 'example.com', ['DIRECT'])
    const proxy = { ...connection('b', 'api.example.com', 'Domain', 'api.example.com', ['Proxy', 'HK']), last_seen: '2026-09-12T04:41:00.000Z' }
    const [summary] = aggregateDomains([direct, proxy])
    expect(summary.host).toBe('api.example.com')
    expect(summary.connections).toBe(2)
    expect(summary.needs_review).toBe(true)
    expect(summary.rules).toHaveLength(2)
  })

  it('builds high-priority rules without touching the subscription source', () => {
    const record = connection('a', 'api.example.com', 'DomainSuffix', 'example.com', ['DIRECT'])
    expect(buildOverrideRule(record, 'domain', 'Proxy')).toBe('DOMAIN,api.example.com,Proxy')
    expect(buildOverrideRule(record, 'domain-suffix', 'Proxy')).toBe('DOMAIN-SUFFIX,api.example.com,Proxy')
    expect(buildOverrideRule(record, 'ip', 'DIRECT')).toBe('IP-CIDR,93.184.216.34/32,DIRECT,no-resolve')
  })
})
