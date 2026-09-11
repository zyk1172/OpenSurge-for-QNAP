import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import { activateLanguage, initialRequestedLanguage, prepareLanguage } from './i18n'
import './i18n.profile-overlay.en'
import './i18n.qnap.en'
import './styles.css'
import './qnap-enhancements.css'

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
