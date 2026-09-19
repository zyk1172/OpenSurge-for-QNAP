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
    schedule: { mode: 'interval' as const, every_days: 1, at: '04:00' },
    health: {
      enabled: true,
      check_interval_minutes: 30,
      latency_threshold_ms: 100,
      loss_rate_threshold: 0,
    },
    scan: {
      budget_seconds: 75,
      candidate_limit: 1024,
      tcp_concurrency: 128,
      tcp_attempts: 3,
      tcp_timeout_ms: 800,
      max_latency_ms: 100,
      max_loss_rate: 0,
      https_candidate_count: 30,
      http_timeout_ms: 2500,
      download_candidate_count: 8,
      download_seconds: 4,
      download_max_bytes: 8388608,
      min_download_mbps: 0,
    },
    targets: [{ domain: 'api.example.com', enabled: true, test_path: '/' }],
  },
  state: {
    running: false,
    checking: false,
    last_run_at: '2026-09-18T01:20:00Z',
    next_run_at: '2026-09-19T04:00:00Z',
    last_health_check_at: '2026-09-18T02:00:00Z',
    next_health_check_at: '2026-09-18T02:30:00Z',
    health: [{
      domain: 'api.example.com',
      ip: '104.18.12.10',
      healthy: true,
      latency_ms: 31,
      loss_rate: 0,
      ttfb_ms: 82,
      checked_at: '2026-09-18T02:00:00Z',
    }],
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

  it('uses the shared design primitives for continuous optimization', async () => {
    const { container } = render(<CloudflareOptimizerPage />)
    expect(await screen.findByRole('heading', { name: '持续监控' })).toBeTruthy()

    expect(container.querySelector('.ui-summary-grid')).toBeTruthy()
    expect(container.querySelectorAll('.ui-panel').length).toBe(4)
    expect(container.querySelector('.ui-card.cloudflare-target-editor')).toBeTruthy()
    expect(screen.getByLabelText('域名列表')).toBeTruthy()
    expect(container.querySelectorAll('.ui-table-wrap .ui-table').length).toBe(2)
    expect(container.querySelector('.ui-action-bar')).toBeTruthy()

    expect(container.querySelector('.cloudflare-monitor-grid')?.children).toHaveLength(5)
    expect(container.querySelector('.cloudflare-monitor-actions .cloudflare-check-button')).toBeTruthy()
    expect(screen.getByLabelText('检查间隔')).toBeTruthy()
    expect(screen.getByLabelText('延迟阈值')).toBeTruthy()
    expect(screen.getByLabelText('最大丢包')).toBeTruthy()
    expect(screen.getByLabelText('强制重新优选')).toBeTruthy()
    expect(screen.getByLabelText('测速模式')).toBeTruthy()
    expect(screen.getByLabelText('优选延迟上限')).toBeTruthy()
    expect(screen.getByLabelText('最低下载速度（Mbps）')).toBeTruthy()

    expect(screen.queryByText('测试路径')).toBeNull()
    expect(screen.queryByText('Cron 表达式')).toBeNull()
    expect(container.querySelector('.connectivity-overview')).toBeNull()
    expect(container.querySelector('.source-card')).toBeNull()
  })

  it('accepts multiline domains and deduplicates them on save', async () => {
    render(<CloudflareOptimizerPage />)
    const domains = await screen.findByLabelText('域名列表')
    expect((domains as HTMLTextAreaElement).value).toBe('api.example.com')

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

  it('maps scan-mode choices to bounded stronger presets', async () => {
    render(<CloudflareOptimizerPage />)
    await screen.findByText('完整优选策略')

    await userEvent.selectOptions(screen.getByLabelText('测速模式'), 'full')
    await userEvent.click(screen.getByRole('button', { name: '保存设置' }))

    await waitFor(() => {
      const put = vi.mocked(request).mock.calls.find(([, init]) => init?.method === 'PUT')
      expect(put).toBeTruthy()
      const payload = JSON.parse(String(put?.[1]?.body))
      expect(payload.scan).toEqual({
        budget_seconds: 120,
        candidate_limit: 1536,
        tcp_concurrency: 200,
        tcp_attempts: 4,
        tcp_timeout_ms: 900,
        max_latency_ms: 100,
        max_loss_rate: 0,
        https_candidate_count: 40,
        http_timeout_ms: 3000,
        download_candidate_count: 12,
        download_seconds: 5,
        download_max_bytes: 12582912,
        min_download_mbps: 0,
      })
    })
  })

  it('updates health settings and can run a current-IP check', async () => {
    render(<CloudflareOptimizerPage />)
    await screen.findByRole('heading', { name: '持续监控' })

    await userEvent.selectOptions(screen.getByLabelText('检查间隔'), '60')
    await userEvent.selectOptions(screen.getByLabelText('延迟阈值'), '150')
    await userEvent.click(screen.getByRole('button', { name: '保存设置' }))

    await waitFor(() => {
      const put = vi.mocked(request).mock.calls.find(([, init]) => init?.method === 'PUT')
      const payload = JSON.parse(String(put?.[1]?.body))
      expect(payload.health.check_interval_minutes).toBe(60)
      expect(payload.health.latency_threshold_ms).toBe(150)
    })

    vi.mocked(request).mockClear()
    await userEvent.click(screen.getByRole('button', { name: '立即检查' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/v1/cloudflare-opt/check', { method: 'POST', body: '{}' }))
  })

  it('renders the continuous optimizer in English', async () => {
    activateLanguage('en')
    render(<CloudflareOptimizerPage />)

    expect(await screen.findByRole('button', { name: 'Optimize now' })).toBeTruthy()
    expect(screen.getByText('Optimization targets')).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Continuous monitoring' })).toBeTruthy()
    expect(screen.getByText('Full optimization strategy')).toBeTruthy()
    expect(screen.getByText('Current IP')).toBeTruthy()
    expect(screen.getByText('Healthy')).toBeTruthy()
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/v1/cloudflare-opt', undefined))
  })
  it('preserves custom quality filters when scan intensity changes', async () => {
    render(<CloudflareOptimizerPage />)
    await screen.findByText('完整优选策略')

    await userEvent.selectOptions(screen.getByLabelText('优选延迟上限'), '300')
    await userEvent.clear(screen.getByLabelText('最低下载速度（Mbps）'))
    await userEvent.type(screen.getByLabelText('最低下载速度（Mbps）'), '25')
    await userEvent.selectOptions(screen.getByLabelText('测速模式'), 'deep')
    await userEvent.click(screen.getByRole('button', { name: '保存设置' }))

    await waitFor(() => {
      const put = vi.mocked(request).mock.calls.find(([, init]) => init?.method === 'PUT')
      const payload = JSON.parse(String(put?.[1]?.body))
      expect(payload.scan.max_latency_ms).toBe(300)
      expect(payload.scan.min_download_mbps).toBe(25)
      expect(payload.scan.budget_seconds).toBe(180)
      expect(payload.scan.candidate_limit).toBe(2048)
    })
  })

})
