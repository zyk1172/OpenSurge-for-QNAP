// @vitest-environment jsdom
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Overview } from '../types'
import { GatewayHealthCard } from './GatewayHealthCard'

function overviewWithDNS(dns: string, dhcp = 'disabled', dhcpEnabled = false): Overview {
  return {
    schema_version: 1,
    revision: 'test',
    topology: 'same_lan',
    drift: false,
    warnings: [],
    status: {
      gateway: 'running',
      runtime_state: 'active',
      interface: 'eth0',
      lan_ip: '192.168.2.241',
      data_plane: 'tun-iif-route',
      routing: 'applied',
      dhcp,
      dhcp_enabled: dhcpEnabled,
      dns,
      mihomo: 'running',
      tun: 'ready',
      tun_interface: 'tun0',
      forwarding: 'enabled',
      dns_ipv6: false,
      tun_ipv6_requested: 'off',
      ipv6_packet: 'disabled',
      native_ipv6_available: false,
      client_count: 0,
    },
    doctor: [],
    doctor_healthy: true,
    leases: [],
    policies: [],
    providers: { proxy_providers: [], rule_providers: [] },
    recovery: { stage: 'idle', required: false },
  }
}

describe('GatewayHealthCard DNS status', () => {
  it('uses the DNS frontend status when DHCP is disabled', () => {
    render(<GatewayHealthCard overview={overviewWithDNS('running')} />)

    const strip = screen.getByLabelText('核心服务状态')
    const dns = within(strip).getByText('DNS').closest('.gateway-service-state')
    expect(dns).toBeTruthy()
    expect(within(dns as HTMLElement).getByText('running')).toBeTruthy()
    expect(within(dns as HTMLElement).queryByText('disabled')).toBeNull()
  })

  it('treats legacy dnsmasq DNS as a healthy DNS frontend', () => {
    render(<GatewayHealthCard overview={overviewWithDNS('legacy-dnsmasq')} />)

    const strip = screen.getByLabelText('核心服务状态')
    const dns = within(strip).getByText('DNS').closest('.gateway-service-state')
    expect(dns).toBeTruthy()
    expect(within(dns as HTMLElement).getByText('legacy-dnsmasq')).toBeTruthy()
  })
})
