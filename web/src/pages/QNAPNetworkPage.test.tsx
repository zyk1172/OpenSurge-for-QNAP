// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from '@testing-library/react'
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

let hostRouting = {
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
  dns_mode: 'auto' as const,
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
    vi.mocked(request).mockImplementation(async (_path, init) => {
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
    })
    vi.mocked(waitForOperation).mockResolvedValue({ id: 'operation', kind: 'start', state: 'succeeded' })
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ enabled: false, api_base: '/api/remote/v1', capabilities: '/api/remote/v1/capabilities' }) })))
  })

  it('shows NAS Tailscale state and removes the raw OpenSurge config-file editor', async () => {
    render(<QNAPNetworkPage overview={overview} onChanged={async () => {}} onNavigate={() => {}} onNotify={() => {}} />)

    expect(await screen.findByText('检测到 NAS 本机 Tailscale')).toBeTruthy()
    expect(screen.getAllByText('已保留给 Tailscale')).toHaveLength(2)
    expect(screen.queryByRole('textbox', { name: 'OpenSurge 配置文件内容' })).toBeNull()
    expect(screen.getByText('OpenSurge 控制面配置不再作为通用 YAML 编辑器')).toBeTruthy()
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

  it('opens Profile Overlay through the proxy and rule sources page', async () => {
    const onNavigate = vi.fn()
    render(<QNAPNetworkPage overview={overview} onChanged={async () => {}} onNavigate={onNavigate} onNotify={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: '打开代理与规则源' }))
    expect(onNavigate).toHaveBeenCalledTimes(1)
  })
})
