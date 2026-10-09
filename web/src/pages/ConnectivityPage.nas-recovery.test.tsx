// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { Overview } from '../types'

vi.mock('../product', () => ({ isNASBuild: true }))
vi.mock('../api', () => ({ api: { connectivity: vi.fn(async () => ({ targets: [] })), testConnectivity: vi.fn(), gateway: vi.fn(async () => ({ id: 'recover-1' })) }, waitForOperation: vi.fn(async () => {}) }))
import { api } from '../api'
import { ConnectivityPage } from './ConnectivityPage'

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllMocks() })

const failedDNS = { status: { gateway: 'degraded', runtime_state: 'active', desired_running: true, mihomo: 'running', dns: 'stopped', local_dns: 'stopped' }, mihomo_recovery: { state: 'failed', reason: 'dns_missing' } } as Overview

it('recovers the complete NAS gateway when the engine runs but DNS is absent', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const changed = vi.fn(async () => {})
  render(<ConnectivityPage overview={failedDNS} onChanged={changed} />)
  await userEvent.click(await screen.findByRole('button', { name: '恢复完整网关' }))
  await waitFor(() => expect(api.gateway).toHaveBeenCalledWith('recover-gateway'))
  await waitFor(() => expect(changed).toHaveBeenCalled())
  expect(window.confirm).toHaveBeenCalledWith(expect.not.stringContaining('Mac'))
})

it('does not offer automatic recovery after an intentional NAS gateway stop', async () => {
  render(<ConnectivityPage overview={{ ...failedDNS, status: { ...failedDNS.status, gateway: 'stopped', runtime_state: 'none', desired_running: false } }} onChanged={async () => {}} />)
  expect(screen.queryByRole('button', { name: '恢复完整网关' })).toBeNull()
  expect(api.gateway).not.toHaveBeenCalled()
})
