// @vitest-environment jsdom
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ControlConfig, NetworkDefaults, Overview } from '../types'

vi.mock('../api', () => ({
  api: {
    config: vi.fn(),
    networkDefaults: vi.fn(),
    gateway: vi.fn(),
    saveConfig: vi.fn(),
  },
  request: vi.fn(),
  waitForOperation: vi.fn(async () => ({ id: 'operation', kind: 'start', state: 'succeeded' })),
}))

import { api, request, waitForOperation } from '../api'
import { QNAPNetworkPage } from './QNAPNetworkPage'

const config: ControlConfig = {
  schema_version: 1,
  revision: 'config-revision',
  gateway: { mode: 'same_lan', interface: 'eth0', lan_ip: '192.168.2.241', lan_prefix_len: 24, upstream_interface: 'eth0' },
  dhcp: { enabled: false, range_start: '', range_end: '', lease_time: '12h', domain: 'lan', bypass_gateway: '', bypass_dns: [] },
  dns: { listen: '192.168.2.241', upstream: '127.0.0.1#1053', ipv6: false },
  mihomo: { store_fake_ip: true },
  transparent: { mode: 'tun', strict_route: false, tun_ipv6: 'off' },
  local_system_proxy: { enabled: false },
  lan_proxy: { enabled: false, socks_port: 7891 },
  device_policy: { enabled: true, protected_ipv4: [] },
}

const networkDefaults: NetworkDefaults = {
  schema_version: 1,
  mode: 'same_lan',
  snapshot: { network_service: 'QNET', interface: 'eth0', ipv4: '192.168.2.241', subnet_mask: '255.255.255.0', router: '192.168.2.1', dns: ['192.168.2.1'], ipv6_default: false },
  gateway_ipv4: '192.168.2.241',
  lan_prefix_len: 24,
  bypass_dns: [],
  warnings: [],
  blockers: [],
}

const overview: Overview = {
  schema_version: 1,
  revision: 'config-revision',
  topology: 'same_lan',
  drift: false,
  warnings: [],
  status: {
    gateway: 'stopped', runtime_state: 'none', interface: 'eth0', lan_ip: '192.168.2.241', data_plane: 'stopped', routing: 'not_applied',
    dhcp: 'stopped', dhcp_enabled: false, mihomo: 'stopped', tun: 'stopped', forwarding: 'disabled', dns_ipv6: false,
    tun_ipv6_requested: 'off', ipv6_packet: 'disabled', native_ipv6_available: false, client_count: 0,
  },
  doctor: [], doctor_healthy: true, leases: [], policies: [], providers: { proxy_providers: [], rule_providers: [] },
  recovery: { stage: 'idle', required: false },
  sleep_prevention: { enabled: false, active: false },
}

type HostRoutingFixture = {
  schema_version: number
  supported: boolean
  desired: boolean
  enabled: boolean
  gateway_ready: boolean
  host_ipv4: string
  host_interface: string
  gateway_ipv4: string
  fallback_gateway: string
  dns_redirect: boolean
  dns_mode: 'auto' | 'opensurge' | 'host'
  protect_tailscale: boolean
  tailscale_detected: boolean
  tailscale_interface: string
  tailscale_dns_protected: boolean
  tailscale_routes_protected: boolean
  checked_at: string
}

let hostRouting: HostRoutingFixture = {
  schema_version: 2,
  supported: true,
  desired: false,
  enabled: false,
  gateway_ready: false,
  host_ipv4: '192.168.2.240',
  host_interface: 'br0',
  gateway_ipv4: '192.168.2.241',
  fallback_gateway: '192.168.2.1',
  dns_redirect: false,
  dns_mode: 'auto',
  protect_tailscale: true,
  tailscale_detected: true,
  tailscale_interface: 'tailscale0',
  tailscale_dns_protected: true,
  tailscale_routes_protected: true,
  checked_at: '2026-09-16T00:00:00Z',
}

describe('QNAPNetworkPage host takeover coexistence controls', () => {
  afterEach(() => { cleanup(); vi.clearAllMocks(); vi.unstubAllGlobals() })

  beforeEach(() => {
    hostRouting = {
      ...hostRouting,
      desired: false,
      enabled: false,
      gateway_ready: false,
      dns_redirect: false,
      dns_mode: 'auto',
      protect_tailscale: true,
    }
    vi.mocked(api.config).mockResolvedValue(config)
    vi.mocked(api.networkDefaults).mockResolvedValue(networkDefaults)
    vi.mocked(api.gateway).mockImplementation(async action => ({ id: action, kind: action, state: 'running' }))
    vi.mocked(api.saveConfig).mockResolvedValue(config)
    vi.mocked(request).mockImplementation(async (path, init) => {
      if (path === '/api/v1/qnap-host-routing') {
        if (init?.method === 'PUT') {
          const body = JSON.parse(String(init.body)) as { enabled: boolean; dns_mode: 'auto' | 'opensurge' | 'host'; protect_tailscale: boolean }
          hostRouting = {
            ...hostRouting,
            desired: body.enabled,
            enabled: body.enabled,
            gateway_ready: body.enabled,
            dns_mode: body.dns_mode,
            protect_tailscale: body.protect_tailscale,
            dns_redirect: body.enabled && body.dns_mode !== 'host',
          }
        }
        return hostRouting
      }
      throw new Error(`unexpected request path ${path}`)
    })
    vi.mocked(waitForOperation).mockResolvedValue({ id: 'operation', kind: 'start', state: 'succeeded' })
  })

  it('focuses the page on network configuration and keeps the unified Tailscale status visible', async () => {
    render(<QNAPNetworkPage overview={overview} onChanged={async () => {}} onNavigate={() => {}} onNotify={() => {}} />)

    const statusPanel = await screen.findByLabelText('NAS 主机接管与 Tailscale 状态')
    expect(within(statusPanel).getByText('Tailscale')).toBeTruthy()
    expect(within(statusPanel).getByText('已检测')).toBeTruthy()
    expect(within(statusPanel).getByText('tailscale0')).toBeTruthy()
    expect(within(statusPanel).getByText('MagicDNS')).toBeTruthy()
    expect(within(statusPanel).getByText('Tailnet 路由')).toBeTruthy()
    expect(within(statusPanel).getAllByText('已保护')).toHaveLength(2)
    expect(screen.queryByRole('textbox', { name: 'OpenSurge 配置文件内容' })).toBeNull()
    expect(screen.queryByText('使用 Profile Overlay')).toBeNull()
    expect(screen.queryByRole('heading', { name: 'QNAP 网关网络' })).toBeNull()
    expect(screen.queryByRole('button', { name: /启动网关|停止网关/ })).toBeNull()
    expect(screen.queryByText('局域网 API 令牌')).toBeNull()
  })

  it('renders the NAS takeover status with the same compact card structure', async () => {
    render(<QNAPNetworkPage overview={overview} onChanged={async () => {}} onNavigate={() => {}} onNotify={() => {}} />)
    const statusPanel = await screen.findByLabelText('NAS 主机接管与 Tailscale 状态')
    const main = statusPanel.querySelector('.qnap-host-status-main') as HTMLElement
    expect(within(main).getByText('NAS 主机接管')).toBeTruthy()
    expect(within(main).getByText('未启用')).toBeTruthy()
    expect(main.querySelector('.mp-status-light')).toBeNull()
  })

  it('persists DNS mode and Tailscale protection through the host-routing endpoint', async () => {
    const onNotify = vi.fn()
    render(<QNAPNetworkPage overview={overview} onChanged={async () => {}} onNavigate={() => {}} onNotify={onNotify} />)

    const mode = await screen.findByRole('combobox', { name: 'NAS DNS 接管模式' })
    await userEvent.selectOptions(mode, 'host')
    const coexistence = screen.getByRole('checkbox', { name: /Tailscale 共存保护/ })
    await userEvent.click(coexistence)
    await userEvent.click(screen.getByRole('button', { name: '保存接管策略' }))

    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/v1/qnap-host-routing', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ enabled: false, dns_mode: 'host', protect_tailscale: false }),
    })))
    expect(onNotify).toHaveBeenCalledWith(expect.objectContaining({ tone: 'success', title: 'NAS 接管策略已保存' }))
  })

  it('persists the dedicated LAN SOCKS5 port through the runtime config', async () => {
    render(<QNAPNetworkPage overview={overview} onChanged={async () => {}} onNavigate={() => {}} onNotify={() => {}} />)

    const toggle = await screen.findByRole('switch', { name: 'LAN SOCKS5 / SOCKS5H' })
    expect(toggle.closest('article')?.classList.contains('qnap-runtime-card-wide')).toBe(true)
    expect(toggle.closest('article')?.classList.contains('qnap-runtime-socks-card')).toBe(true)
    await userEvent.click(toggle)
    const port = screen.getByRole('spinbutton', { name: 'LAN SOCKS 端口' })
    await userEvent.clear(port)
    await userEvent.type(port, '17891')
    await userEvent.click(screen.getByRole('button', { name: '保存运行参数' }))

    await waitFor(() => expect(api.saveConfig).toHaveBeenCalledWith(expect.objectContaining({
      lan_proxy: { enabled: true, socks_port: 17891 },
    })))
  })

  it('keeps advanced proxy and deployment shortcuts off the network page', async () => {
    const onNavigate = vi.fn()
    render(<QNAPNetworkPage overview={overview} onChanged={async () => {}} onNavigate={onNavigate} onNotify={() => {}} />)

    await screen.findByRole('heading', { name: 'NAS 主机接管' })
    expect(screen.queryByRole('button', { name: '打开代理与规则源' })).toBeNull()
    expect(screen.queryByText('高级代理配置')).toBeNull()
    expect(screen.queryByText('部署网络修改')).toBeNull()
    expect(screen.queryByText('IPv6 未接管')).toBeNull()
    expect(screen.queryByText('DNS 上游')).toBeNull()
    expect(screen.getByRole('switch', { name: 'Store fake-ip' })).toBeTruthy()
    expect(screen.getByRole('switch', { name: 'TUN strict-route' })).toBeTruthy()
    expect(onNavigate).not.toHaveBeenCalled()
  })
})
