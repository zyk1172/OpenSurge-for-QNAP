import type { ControlConfig, ResolverUpstream } from './types'

export const resolverPresets: Array<ResolverUpstream & { name: string }> = [
  { name: 'Cloudflare', address: 'https://cloudflare-dns.com/dns-query', bootstrap_ipv4: '1.1.1.1' },
  { name: 'Google', address: 'https://dns.google/dns-query', bootstrap_ipv4: '8.8.8.8' },
  { name: 'Quad9', address: 'https://dns.quad9.net/dns-query', bootstrap_ipv4: '9.9.9.9' },
]

// Validate before stopping a running gateway. The server validates the same
// configuration again before persisting it.
export function validateResolverDraft(config: ControlConfig): string | null {
  const upstreams = config.dns.resolver_upstreams || []
  if (config.dns.resolver_mode === 'custom' && !upstreams.length) return '自定义上游至少需要填写一个 DNS。'
  const seen = new Set<string>()
  const ipv4 = (value: string) => {
    if (!/^\d{1,3}(\.\d{1,3}){3}$/.test(value)) return false
    const octets = value.split('.').map(Number)
    if (octets.some((n, i) => n > 255 || String(n) !== value.split('.')[i])) return false
    return octets[0] !== 0 && octets[0] !== 127 && octets[0] < 224 && !(octets[0] === 169 && octets[1] === 254)
      && value !== '100.100.100.100' && value !== config.dns.listen && value !== config.gateway.lan_ip
  }
  for (const upstream of upstreams) {
    const invalid = '请填写有效的上游地址；加密 DNS 需填写证书域名和固定连接 IPv4，明文 DNS 需明确允许。'
    const address = upstream.address.trim()
    if (!address || address.length > 2048 || /[\s"'#,|\\]/.test(address)) return invalid
    try {
      const endpoint = new URL(address.includes('://') ? address : `udp://${address}`)
      if (endpoint.username || endpoint.password || endpoint.hash || (endpoint.port && (+endpoint.port < 1 || +endpoint.port > 65535))) return invalid
      const encrypted = endpoint.protocol === 'https:' || endpoint.protocol === 'tls:'
      if (encrypted) {
        if (endpoint.hostname.length > 253 || !endpoint.hostname.includes('.') || !endpoint.hostname.split('.').every(label => /^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/.test(label)) || /^\d+(\.\d+){3}$/.test(endpoint.hostname)) return invalid
        if (!ipv4(upstream.bootstrap_ipv4?.trim() || '')) return invalid
        if (endpoint.protocol === 'https:' ? endpoint.pathname === '/' : endpoint.pathname !== '' || endpoint.search !== '') return invalid
      } else {
        if (!['udp:', 'tcp:'].includes(endpoint.protocol) || !config.dns.resolver_allow_plaintext || !ipv4(endpoint.hostname) || endpoint.pathname || endpoint.search || upstream.bootstrap_ipv4?.trim()) return invalid
      }
      const key = `${endpoint.protocol}|${endpoint.host}|${endpoint.pathname}|${endpoint.search}|${upstream.bootstrap_ipv4?.trim() || ''}`
      if (seen.has(key)) return '上游 DNS 列表中存在重复地址。'
      seen.add(key)
    } catch { return invalid }
  }
  return upstreams.length > 16 ? '最多可配置 16 个上游 DNS。' : null
}
