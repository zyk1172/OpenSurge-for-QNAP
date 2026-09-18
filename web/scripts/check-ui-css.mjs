import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

function read(relative) {
  return readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')
}

function ruleBody(css, selector) {
  const start = css.indexOf(selector)
  if (start < 0) throw new Error(`Missing CSS rule: ${selector}`)
  const open = css.indexOf('{', start)
  const close = css.indexOf('}', open)
  if (open < 0 || close < 0) throw new Error(`Malformed CSS rule: ${selector}`)
  return css.slice(open + 1, close)
}

const design = read('../src/design-system.css')
const traffic = read('../src/pages/TrafficAnalysisPage.css')

const runtime = ruleBody(design, 'html[data-product-target="qnap"] .workspace-canvas .qnap-runtime-grid>.qnap-runtime-card')
if (!runtime.includes('min-height:94px!important')) throw new Error('QNAP runtime card minimum height contract changed')
if (!runtime.includes('height:auto!important')) throw new Error('QNAP runtime card must be allowed to grow')
if (/(?:^|[;\\s])height:94px!important/.test(runtime)) throw new Error('QNAP runtime card must not be fixed to 94px')

const searchActions = ruleBody(traffic, '.traffic-v3-search-actions button')
if (!searchActions.includes('min-height:var(--ui-control-height,40px)')) throw new Error('Traffic search buttons must use shared control height')
if (!searchActions.includes('font-size:var(--ui-font-control,13px)')) throw new Error('Traffic search buttons must use shared control font size')

console.log('UI CSS contracts OK')
