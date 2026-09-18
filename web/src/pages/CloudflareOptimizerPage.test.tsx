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
      candidate_limit: 256,
      tcp_concurrency: 64,
      tcp_attempts: 2,
      tcp_timeout_ms: 800,
      https_candidate_count: 15,
      http_timeout_ms: 2000,
      download_candidate_count: 3,
      download_seconds: 2,
      download_max_bytes: 4194304,
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

  it('keeps the optimizer surface simple and on shared design primitives', async () => {
    const { container } = render(<CloudflareOptimizerPage />)
    expect(await screen.findByText('自动优选周期')).toBeTruthy()

    expect(container.querySelector('.ui-summary-grid')).toBeTruthy()
    expect(container.querySelectorAll('.ui-panel').length).toBe(4)
    expect(container.querySelector('.ui-card.cloudflare-target-editor')).toBeTruthy()
    expect(screen.getByLabelText('域名列表')).toBeTruthy()
    expect(container.querySelector('.ui-table-wrap .ui-table')).toBeTruthy()
    expect(container.querySelector('.ui-action-bar')).toBeTruthy()

    expect(screen.queryByText('测试路径')).toBeNull()
    expect(screen.queryByText('候选 IP')).toBeNull()
    expect(screen.queryByText('TCP 并发')).toBeNull()
    expect(screen.queryByText('HTTPS 候选')).toBeNull()
    expect(screen.queryByText('Cron 表达式')).toBeNull()
    expect(screen.getByLabelText('周期')).toBeTruthy()
    expect(screen.getByLabelText('测速模式')).toBeTruthy()

    expect(container.querySelector('.connectivity-overview')).toBeNull()
    expect(container.querySelector('.source-card')).toBeNull()
    expect(container.querySelector('.source-actions')).toBeNull()
  })

  it('accepts multiline domain input, removes blank lines and deduplicates on save', async () => {
    render(<CloudflareOptimizerPage />)
    const domains = await screen.findByLabelText('域名列表')
    expect((domains as HTMLTextAreaElement).value).toBe('api.example.com')
    expect((screen.getByRole('button', { name: '保存设置' }) as HTMLButtonElement).disabled).toBe(true)

    await userEvent.clear(domains)
    await userEvent.type(domains, 'cdn.example.com\nassets.example.com\n\ncdn.example.com')
    await userEvent.click(screen.getByRole('button', { name: '保存设置' }))

    await waitFor(() => {
      const put = vi.mocked(request).mock.calls.find(([, init]) => init?.method === 'PUT')
      expect(put).toBeTruthy()
      const payload = JSON.parse(String(put?.[1]?.body))
      expect(payload.targets).toEqual([
        { domain: 'cdn.example.com', enabled: true, test_path: '/' },
        { domain: 'assets.example.com', enabled: true, test_path: '/' },
      ])
    })
  })

  it('maps one scan-mode choice to the full internal preset', async () => {
    render(<CloudflareOptimizerPage />)
    await screen.findAllByText('测速模式')

    await userEvent.selectOptions(screen.getByLabelText('测速模式'), 'full')
    await userEvent.click(screen.getByRole('button', { name: '保存设置' }))

    await waitFor(() => {
      const put = vi.mocked(request).mock.calls.find(([, init]) => init?.method === 'PUT')
      expect(put).toBeTruthy()
      const payload = JSON.parse(String(put?.[1]?.body))
      expect(payload.scan).toEqual({
        budget_seconds: 90,
        candidate_limit: 512,
        tcp_concurrency: 96,
        tcp_attempts: 3,
        tcp_timeout_ms: 900,
        https_candidate_count: 24,
        http_timeout_ms: 3000,
        download_candidate_count: 4,
        download_seconds: 3,
        download_max_bytes: 8388608,
      })
    })
  })

  it('renders the simplified page in the selected English locale', async () => {
    activateLanguage('en')
    render(<CloudflareOptimizerPage />)

    expect(await screen.findByRole('button', { name: 'Optimize now' })).toBeTruthy()
    expect(screen.getByText('Optimization targets')).toBeTruthy()
    expect(screen.getByText('Automatic optimization cycle')).toBeTruthy()
    expect(screen.getAllByText('Scan mode').length).toBeGreaterThan(0)
    expect(screen.getByText('Current IP')).toBeTruthy()
    expect(screen.queryByText('自动优选周期')).toBeNull()
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/v1/cloudflare-opt', undefined))
  })
})
