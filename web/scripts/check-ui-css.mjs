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

const hostPolicy = ruleBody(design, '.qnap-host-policy-card')
if (!hostPolicy.includes('min-height:94px!important')) throw new Error('QNAP host policy cards must use the shared 94px card height')
if (!hostPolicy.includes('height:94px!important')) throw new Error('Desktop QNAP host policy cards must stay aligned with surrounding status cards')
if (!hostPolicy.includes('grid-template-columns:minmax(0,1fr) auto!important')) throw new Error('QNAP host policy cards must use the compact copy/control layout')

const runtime = ruleBody(design, 'html[data-product-target="qnap"] .workspace-canvas .qnap-runtime-grid>.qnap-runtime-card')
if (!runtime.includes('min-height:94px!important')) throw new Error('QNAP runtime card minimum height contract changed')
if (!runtime.includes('height:94px!important')) throw new Error('Desktop QNAP runtime cards must stay aligned to the host-status card height')

const runtimeWide = ruleBody(design, 'html[data-product-target="qnap"] .workspace-canvas .qnap-runtime-grid>.qnap-runtime-card-wide')
if (!runtimeWide.includes('grid-column:span 2!important')) throw new Error('Wide QNAP runtime card must span two card columns')

const runtimeSocks = ruleBody(design, 'html[data-product-target="qnap"] .workspace-canvas .qnap-runtime-socks-card')
if (!runtimeSocks.includes('grid-template-areas:')) throw new Error('LAN SOCKS runtime card must use the compact horizontal layout')
if (!runtimeSocks.includes('padding-left:14px!important')) throw new Error('LAN SOCKS runtime card must preserve the shared left content inset')
if (!runtimeSocks.includes('padding-right:14px!important')) throw new Error('LAN SOCKS runtime card must preserve the shared right content inset')
if (!runtimeSocks.includes('"title port endpoint"')) throw new Error('LAN SOCKS runtime title must stay in the first hierarchy row')
if (!runtimeSocks.includes('"toggle port endpoint"')) throw new Error('LAN SOCKS runtime switch must stay in the middle hierarchy row')
if (!runtimeSocks.includes('"note port endpoint"')) throw new Error('LAN SOCKS runtime note must stay in the bottom hierarchy row')

const searchActions = ruleBody(traffic, '.traffic-v3-search-actions button')
if (!searchActions.includes('min-height:var(--ui-control-height,40px)')) throw new Error('Traffic search buttons must use shared control height')
if (!searchActions.includes('font-size:var(--ui-font-control,13px)')) throw new Error('Traffic search buttons must use shared control font size')


const toolbar = ruleBody(design, '.workspace-toolbar')
if (!toolbar.includes('display:none!important')) throw new Error('Desktop workspace toolbar must collapse after page/live badges are removed')
if (!toolbar.includes('height:0!important')) throw new Error('Collapsed desktop workspace toolbar must not reserve vertical space')

const textInput = ruleBody(design, ':where(input:not([type="checkbox"]):not([type="radio"]))')
if (!textInput.includes('padding:0 12px!important')) throw new Error('Text/number inputs must align with shared control horizontal padding')

const connectivityEvidence = ruleBody(design, '.workspace-canvas .connectivity-target .target-details>summary')
if (!connectivityEvidence.includes('text-align:left!important')) throw new Error('Connectivity evidence disclosure must align with the expected-route column')

console.log('UI CSS contracts OK')
