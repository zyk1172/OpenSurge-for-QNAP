// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

vi.mock('../api', () => ({
  api: {
    refreshLocalConnections: vi.fn(),
    refreshDeviceConnections: vi.fn(),
    refreshPolicyConnections: vi.fn(),
  },
}))

import { api } from '../api'
import { ConnectionRefreshPrompts, queueConnectionRefreshSuggestion, type ConnectionRefreshSuggestionItem } from './ConnectionRefreshPrompts'

beforeEach(() => {
  vi.mocked(api.refreshLocalConnections).mockResolvedValue({ schema_version: 1, scope: 'gateway_local', matched_connections: 2, closed_connections: 2 })
  vi.mocked(api.refreshDeviceConnections).mockResolvedValue({ schema_version: 1, scope: 'device', device_id: 'phone', matched_connections: 0, closed_connections: 0 })
  vi.mocked(api.refreshPolicyConnections).mockResolvedValue({ schema_version: 1, scope: 'policy_group', policy_group: '共享/香港 策略', matched_connections: 3, closed_connections: 3 })
})

afterEach(() => { cleanup(); vi.clearAllMocks() })

it('refreshes the exact shared policy group only after explicit consent', async () => {
  const suggestion = { id: 3, key: 'policy_group:共享/香港 策略', scope: 'policy_group' as const, group: '共享/香港 策略', subject: '香港策略', selection: '香港 B' }
  const refreshed = vi.fn()
  render(<ConnectionRefreshPrompts suggestions={[suggestion]} onDismiss={vi.fn()} onRefreshed={refreshed} />)

  expect(screen.getByText(/精确关闭当前经过此组的连接/)).toBeTruthy()
  expect(api.refreshPolicyConnections).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: '刷新经过此策略组的连接' }))
  expect(api.refreshPolicyConnections).toHaveBeenCalledExactlyOnceWith('共享/香港 策略')
  expect(api.refreshLocalConnections).not.toHaveBeenCalled()
  expect(api.refreshDeviceConnections).not.toHaveBeenCalled()
  expect(await screen.findByText('已关闭 3 个连接，等待客户端建立新连接。')).toBeTruthy()
  await waitFor(() => expect(refreshed).toHaveBeenCalledOnce())
})

it('retries a failed policy-group refresh without widening scope', async () => {
  vi.mocked(api.refreshPolicyConnections).mockRejectedValueOnce(new Error('closed 1 of 3 matching connections'))
  const suggestion = { id: 4, key: 'policy_group:Main', scope: 'policy_group' as const, group: 'Main', subject: 'Main', selection: 'Proxy-B' }
  render(<ConnectionRefreshPrompts suggestions={[suggestion]} onDismiss={vi.fn()} />)

  await userEvent.click(screen.getByRole('button', { name: '刷新经过此策略组的连接' }))
  expect(await screen.findByText('出口已切换，连接刷新失败')).toBeTruthy()
  expect(screen.getByText('closed 1 of 3 matching connections')).toBeTruthy()
  await userEvent.click(screen.getByRole('button', { name: '重试刷新连接' }))
  expect(api.refreshPolicyConnections).toHaveBeenCalledTimes(2)
  expect(api.refreshLocalConnections).not.toHaveBeenCalled()
  expect(api.refreshDeviceConnections).not.toHaveBeenCalled()
})

it('replaces the same group prompt with its latest selection and keeps other scopes', () => {
  const group = { key: 'policy_group:Main', scope: 'policy_group' as const, group: 'Main', subject: 'Main', selection: 'Proxy-A' }
  const gateway = { key: 'gateway_local', scope: 'gateway_local' as const, subject: '网关本机', selection: 'DIRECT' }
  let current: ConnectionRefreshSuggestionItem[] = queueConnectionRefreshSuggestion([], group, 1)
  current = queueConnectionRefreshSuggestion(current, gateway, 2)
  current = queueConnectionRefreshSuggestion(current, { ...group, selection: 'Proxy-B' }, 3)
  expect(current).toEqual([{ ...gateway, id: 2 }, { ...group, selection: 'Proxy-B', id: 3 }])
})
