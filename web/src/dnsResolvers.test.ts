import { describe, expect, it } from 'vitest'
import { resolverPresets, validateResolverDraft } from './dnsResolvers'
import type { ControlConfig } from './types'

const base = { gateway: { lan_ip: '192.168.2.241' }, dns: { listen: '192.168.2.241', upstream: '127.0.0.1#1053', ipv6: false, resolver_mode: 'custom' } } as ControlConfig

describe('resolver validation before gateway stop', () => {
  it('accepts all encrypted presets', () => {
    for (const preset of resolverPresets) expect(validateResolverDraft({ ...base, dns: { ...base.dns, resolver_upstreams: [preset] } })).toBeNull()
  })
  it('requires explicit plaintext authorization and a fixed IPv4 for encryption', () => {
    for (const upstream of [
      { address: '8.8.8.8' },
      { address: 'https://dns.google/dns-query' },
      { address: 'tls://dns.google', bootstrap_ipv4: '127.0.0.1' },
      { address: 'https://192.168.2.241/dns-query', bootstrap_ipv4: '8.8.8.8' },
      { address: 'https://dns.google/dns-query -no-check-certificate', bootstrap_ipv4: '8.8.8.8' },
    ]) expect(validateResolverDraft({ ...base, dns: { ...base.dns, resolver_upstreams: [upstream] } })).not.toBeNull()
    expect(validateResolverDraft({ ...base, dns: { ...base.dns, resolver_upstreams: [{ address: 'tcp://8.8.8.8:53' }], resolver_allow_plaintext: true } })).toBeNull()
  })
})
