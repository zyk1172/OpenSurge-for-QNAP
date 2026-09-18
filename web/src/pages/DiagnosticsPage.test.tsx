// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Overview } from '../types'

vi.mock('../api', () => ({ api: { diagnostics: vi.fn(), doctorStatus: vi.fn(), runDoctor: vi.fn(), refreshProvider: vi.fn() } }))

import { api } from '../api'
import { DiagnosticsPage } from './DiagnosticsPage'

const overview = {
  revision: 'revision-a',
  status: { gateway: 'running' },
  providers: { proxy_providers: [], rule_providers: [] },
  recovery: { stage: 'idle', required: false },
} as unknown as Overview

describe('DiagnosticsPage environment check', () => {
  beforeEach(() => {
    vi.mocked(api.diagnostics).mockResolvedValue({ schema_version: 1, revision: 'revision-a', connections: { upload_total: 0, download_total: 0, connections: [] }, logs: {}, operations: [], recovery: { stage: 'idle', required: false } })
    vi.mocked(api.doctorStatus).mockResolvedValue({ schema_version: 1, state: 'idle', current: true, checks: [], healthy: false })
    vi.mocked(api.runDoctor).mockResolvedValue({ schema_version: 1, state: 'succeeded', revision: 'revision-a', current: true, healthy: false, checks: [{ name: 'mihomo config validation', ok: false, message: 'invalid' }] })
  })

  afterEach(() => { cleanup(); vi.clearAllMocks() })

  it('keeps provider status cards compact with status, copy, and refresh in one row', async () => {
    render(<DiagnosticsPage overview={{ ...overview, providers: { proxy_providers: [{ name: 'AI', proxy_count: 3, vehicle_type: 'Compatible', proxies: [{ alive: true }] }], rule_providers: [] } } as unknown as Overview} />)
    const refresh = await screen.findByRole('button', { name: '刷新' })
    expect(refresh.classList.contains('provider-refresh-button')).toBe(true)
    const row = refresh.closest('.row') as HTMLElement
    expect(row.querySelector('.status-dot')).toBeTruthy()
    expect(row.querySelector('.grow')?.textContent).toContain('AI')
  })

  it('loads cached state without running the check and starts it only after an explicit click', async () => {
    render(<DiagnosticsPage overview={overview} />)

    expect(await screen.findByRole('button', { name: '运行检查' })).toBeTruthy()
    expect(api.doctorStatus).toHaveBeenCalledTimes(1)
    expect(api.runDoctor).not.toHaveBeenCalled()
    expect(screen.getByText('按“运行检查”开始完整检查。')).toBeTruthy()

    await userEvent.click(screen.getByRole('button', { name: '运行检查' }))
    await waitFor(() => expect(api.runDoctor).toHaveBeenCalledTimes(1))
    expect(await screen.findByText('mihomo 配置校验')).toBeTruthy()
    expect(screen.getByText('invalid')).toBeTruthy()
    expect(screen.getByRole('button', { name: '重新检查' })).toBeTruthy()
  })
})
