// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../api', () => ({ request: vi.fn() }))

import { request } from '../api'
import { activateLanguage } from '../i18n'
import '../i18n.cloudflare.en'
import { CloudflareOptimizerPage } from './CloudflareOptimizerPage'

const response = {
  config: {
    schema_version: 1,
    enabled: true,
    schedule: { mode: 'interval' as const, every_days: 7, at: '04:00' },
    scan: {
      budget_seconds: 60,
      candidate_limit: 384,
      tcp_concurrency: 64,
      tcp_attempts: 3,
      tcp_timeout_ms: 1000,
      https_candidate_count: 24,
      http_timeout_ms: 3000,
      download_candidate_count: 3,
      download_seconds: 3,
      download_max_bytes: 8388608,
    },
    targets: [{ domain: 'api.example.com', enabled: true, test_path: '/' }],
  },
  state: {
    running: false,
    last_run_at: '2026-09-18T01:20:00Z',
    next_run_at: '2026-09-25T04:00:00Z',
    results: [{
      domain: 'api.example.com',
      selected: { ip: '104.18.12.10', latency_ms: 32, loss_rate: 0, ttfb_ms: 85, download_mbps: 42.4, colo: 'SIN' },
      alternatives: [],
      updated_at: '2026-09-18T01:20:00Z',
    }],
  },
}

describe('CloudflareOptimizerPage', () => {
  beforeEach(() => {
    activateLanguage('zh-Hans')
    vi.mocked(request).mockResolvedValue(response as never)
  })

  afterEach(() => {
    activateLanguage('zh-Hans')
    cleanup()
    vi.clearAllMocks()
  })

  it('uses shared design-system primitives instead of cross-feature visual classes', async () => {
    const { container } = render(<CloudflareOptimizerPage />)
    expect(await screen.findByText('自动优选')).toBeTruthy()

    expect(container.querySelector('.ui-summary-grid')).toBeTruthy()
    expect(container.querySelectorAll('.ui-panel').length).toBe(4)
    expect(container.querySelector('.ui-card.cloudflare-target-card')).toBeTruthy()
    expect(container.querySelector('.ui-table-wrap .ui-table')).toBeTruthy()
    expect(container.querySelector('.ui-action-bar')).toBeTruthy()

    expect(container.querySelector('.connectivity-overview')).toBeNull()
    expect(container.querySelector('.source-card')).toBeNull()
    expect(container.querySelector('.source-actions')).toBeNull()
    expect(container.querySelector('.section-heading')).toBeNull()
    expect(container.querySelector('.table-wrap')).toBeNull()
    expect(container.querySelector('.sticky-actions')).toBeNull()
  })

  it('marks the settings dirty through the shared form controls', async () => {
    render(<CloudflareOptimizerPage />)
    const domain = await screen.findByDisplayValue('api.example.com')
    expect((screen.getByRole('button', { name: '保存设置' }) as HTMLButtonElement).disabled).toBe(true)

    await userEvent.clear(domain)
    await userEvent.type(domain, 'cdn.example.com')

    expect(screen.getByText('有未保存修改')).toBeTruthy()
    expect((screen.getByRole('button', { name: '保存设置' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('renders the whole page in the selected English locale', async () => {
    activateLanguage('en')
    render(<CloudflareOptimizerPage />)

    expect(await screen.findByRole('button', { name: 'Optimize now' })).toBeTruthy()
    expect(screen.getByText('Automatic optimization')).toBeTruthy()
    expect(screen.getByText('Optimization targets')).toBeTruthy()
    expect(screen.getByText('Scan limits')).toBeTruthy()
    expect(screen.getByText('Current IP')).toBeTruthy()
    expect(screen.queryByText('自动优选')).toBeNull()
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/v1/cloudflare-opt', undefined))
  })
})
