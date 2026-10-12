import { describe, expect, it } from 'vitest'
import { resolverPresets, validateResolverDraft } from './dnsResolvers'
import type { ControlConfig } from './types'

const base = { gateway: { lan_ip: '192.168.2.241' }, dns: { listen: '192.168.2.241', upstream: '127.0.0.1#1053', ipv6: false, resolver_mode: 'custom' } } as ControlConfig

describe('resolver validation before gateway stop', () => {
  it('accepts all encrypted presets', () => {
    for (const preset of resolverPresets) expect(validateResolverDraft({ ...base, dns: { ...base.dns, resolver_upstreams: [preset] } })).toBeNull()
  })
  it('requires a proxy target, encryption for bootstrap, and prevents UDP proxy bypass', () => {
    const valid = { ...base, dns: { ...base.dns, resolver_proxy_enabled: true, resolver_proxy_target: '节点选择', mihomo_use_smartdns: true, resolver_upstreams: [resolverPresets[0]] } }
    expect(validateResolverDraft(valid)).toBeNull()
    for (const dns of [
      { ...valid.dns, resolver_proxy_target: '' },
      { ...valid.dns, resolver_proxy_target: 'DIRECT' },
      { ...valid.dns, resolver_mode: 'system' as const },
      { ...valid.dns, resolver_upstreams: [{ address: 'tcp://223.5.5.5' }], resolver_allow_plaintext: true },
      { ...valid.dns, resolver_upstreams: [...valid.dns.resolver_upstreams, { address: 'udp://223.5.5.5' }], resolver_allow_plaintext: true },
    ]) expect(validateResolverDraft({ ...base, dns })).not.toBeNull()
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
