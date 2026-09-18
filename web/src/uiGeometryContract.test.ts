import { describe, expect, it } from 'vitest'
import designSystem from './design-system.css?inline'
import trafficStyles from './pages/TrafficAnalysisPage.css?inline'

function cssRule(css: string, selector: string) {
  const start = css.indexOf(selector)
  if (start < 0) throw new Error('Missing CSS rule: ' + selector)
  const open = css.indexOf('{', start)
  const close = css.indexOf('}', open)
  return css.slice(open + 1, close)
}

describe('UI geometry CSS contracts', () => {
  it('lets QNAP runtime cards grow past the shared minimum height', () => {
    const body = cssRule(designSystem, 'html[data-product-target="qnap"] .workspace-canvas .qnap-runtime-grid>.qnap-runtime-card')
    expect(body).toContain('min-height:94px!important')
    expect(body).toContain('height:auto!important')
    expect(body).not.toContain('height:94px!important')
  })

  it('keeps real-time connection search actions on the shared control geometry', () => {
    const body = cssRule(trafficStyles, '.traffic-v3-search-actions button')
    expect(body).toContain('min-height:var(--ui-control-height,40px)')
    expect(body).toContain('font-size:var(--ui-font-control,13px)')
  })
})
