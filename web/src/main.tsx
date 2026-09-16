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
import './styles.css'
import './qnap-enhancements.css'
import './control-center-v3.css'
import './mobile-control-center-v3.css'
import './mobile-touch-fixes.css'
import './moviepilot-glass-v4.css'
import './ui-density-fixes.css'
import './ui-control-integrity.css'
import './ui-layout-system.css'

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
