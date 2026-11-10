// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { DiagnosticSignals } from './DiagnosticSignals'

afterEach(cleanup)
describe('DiagnosticSignals', () => {
  it('supports older servers without structured summaries', () => {
    const { container } = render(<DiagnosticSignals signals={undefined} />)
    expect(container.textContent).toBe('')
  })
  it('shows deduplicated connection failures with an explicit attribution boundary', () => {
    render(<DiagnosticSignals signals={{ sampled_lines: 100, connection_failures: 42, other_warnings: 0, omitted_groups: 2, groups: [{ emitter: 'mihomo', kind: 'connection', source_ip: '192.168.2.240', target: 'example.com:443', policy: 'DIRECT', rule: 'Match/', reason: 'timeout', count: 42 }] }} />)
    expect(screen.getByText('42 次')).toBeTruthy()
    expect(screen.getByText('连接失败 · 超时')).toBeTruthy()
    expect(screen.getByText('mihomo · 192.168.2.240 → example.com:443')).toBeTruthy()
    expect(screen.getByText(/来源 IP 不能确定应用/)).toBeTruthy()
    expect(screen.getByText(/另有 2 组未展示/)).toBeTruthy()
    expect(screen.queryByText('Transmission')).toBeNull()
  })
})
