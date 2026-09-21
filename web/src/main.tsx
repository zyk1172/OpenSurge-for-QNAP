import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import './navigation-scroll-reset'
import { activateLanguage, initialRequestedLanguage, prepareLanguage } from './i18n'
import './i18n.profile-overlay.en'
import './i18n.traffic.en'
import './i18n.shell.en'
import './i18n.qnap.en'
import './i18n.qnap-host.en'
import './i18n.layout-audit.en'
import './i18n.ui-refresh.en'
import './i18n.cloudflare.en'

// Legacy feature/layout layers remain during migration because they still own
// page-specific geometry. They are no longer allowed to define the shared
// visual contract. The bridge maps their high-specificity exceptions back to
// shared tokens, then design-system.css loads last as the visual authority.
import './styles.css'
import './qnap-enhancements.css'
import './control-center-v3.css'
import './mobile-control-center-v3.css'
import './mobile-touch-fixes.css'
import './moviepilot-glass-v4.css'
import './ui-density-fixes.css'
import './ui-control-integrity.css'
import './ui-layout-system.css'
import './moviepilot-ui-v5.css'
import './moviepilot-ui-v5-fixes.css'
import './legacy-design-bridge.css'
import './design-system.css'
import './mobile-responsive.css'

const productTarget = (import.meta.env.VITE_OPENSURGE_TARGET ?? 'mac').trim().toLowerCase()
document.documentElement.dataset.productTarget = productTarget
document.title = productTarget === 'qnap' ? 'OpenSurge for QNAP' : 'OpenSurge for Mac'

async function start() {
  const language = initialRequestedLanguage()
  await prepareLanguage(language)
  activateLanguage(language)

  createRoot(document.getElementById('root')!).render(
    <StrictMode><App /></StrictMode>,
  )
}

void start()
