// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { activateLanguage } from '../i18n'
import '../i18n.traffic.en'
import { TutorialPage } from './TutorialPage'

describe('TutorialPage', () => {
  const writeText = vi.fn(async () => {})

  beforeEach(() => {
    activateLanguage('zh-Hans')
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  })

  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('hosts the AI routing-analysis guide as a tutorial workflow', async () => {
    const { container } = render(<TutorialPage />)

    expect(screen.getByText('AI 分流分析')).toBeTruthy()
    expect(screen.getByText('GET /api/remote/v1/diagnostics')).toBeTruthy()
    expect(screen.getByText('GET /api/remote/v1/policies')).toBeTruthy()
    expect(screen.getByText('GET / PUT /api/remote/v1/profile-overlay')).toBeTruthy()
    expect(container.querySelector('.traffic-v3-ai-flow')).toBeNull()

    await userEvent.click(screen.getByRole('button', { name: '复制 AI 分流分析说明' }))
    expect(writeText).toHaveBeenCalledTimes(1)
    expect(writeText.mock.calls[0][0]).toContain('/api/remote/v1/diagnostics')
    expect(await screen.findByText('AI 分流分析说明已复制。')).toBeTruthy()
  })
})
