// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import './design-system.css'
import './pages/TrafficAnalysisPage.css'

function findStyleRule(match: (selector: string) => boolean) {
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      const selector = (rule as CSSStyleRule).selectorText
      if (selector && match(selector)) return rule as CSSStyleRule
    }
  }
  throw new Error('Expected CSS rule was not loaded')
}

describe('UI geometry CSS contracts', () => {
  it('lets QNAP runtime cards grow past the shared minimum height', () => {
    const rule = findStyleRule(selector => selector.includes('.qnap-runtime-grid') && selector.includes('.qnap-runtime-card'))
    expect(rule.style.getPropertyValue('min-height')).toBe('94px')
    expect(rule.style.getPropertyValue('height')).toBe('auto')
    expect(rule.style.getPropertyPriority('height')).toBe('important')
  })

  it('keeps real-time connection search actions on the shared control geometry', () => {
    const rule = findStyleRule(selector => selector === '.traffic-v3-search-actions button')
    expect(rule.style.getPropertyValue('min-height')).toBe('var(--ui-control-height,40px)')
    expect(rule.style.getPropertyValue('font-size')).toBe('var(--ui-font-control,13px)')
  })
})
