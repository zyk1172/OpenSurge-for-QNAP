import { useCallback, useEffect, useRef, useState } from 'react'
import { api, authenticationRequiredEvent, RequestError } from './api'
import { CommandPalette } from './components/CommandPalette'
import { PageErrorBoundary } from './components/PageErrorBoundary'
import { OperationNotifications, type OperationNotification, type OperationNotificationItem } from './components/OperationNotifications'
import { OperationProgress } from './components/OperationProgress'
import { LanguageSelector } from './components/LanguageSelector'
import { RecoveryBanner, StatusDot } from './components/Common'
import { ShellIcon } from './components/ShellIcon'
import { DashboardPage } from './pages/DashboardPage'
import { ConnectivityPage } from './pages/ConnectivityPage'
import { DevicesPage } from './pages/DevicesPage'
import { DiagnosticsPage } from './pages/DiagnosticsPage'
import { TrafficAnalysisPage } from './pages/TrafficAnalysisPage'
import { NetworkPage } from './pages/NetworkPage'
import { QNAPNetworkPage } from './pages/QNAPNetworkPage'
import { QNAPManagementPage } from './pages/QNAPManagementPage'
import { PoliciesPage, type PoliciesViewState } from './pages/PoliciesPage'
import { SourcesPage } from './pages/SourcesPage'
import { QNAPSourcesPage } from './pages/QNAPSourcesPage'
import { needsNetworkRecoveryWarning, statusLabel } from './status'
import { operationStatusUnknownMessage } from './operations'
import type { Overview } from './types'
import { activateLanguage, cacheRequestedLanguage, initialRequestedLanguage, isRequestedLanguage, prepareLanguage, t, type RequestedLanguage } from './i18n'

type Page = 'dashboard' | 'network' | 'sources' | 'devices' | 'policies' | 'management' | 'connectivity' | 'diagnostics' | 'traffic'
type Theme = 'dark' | 'light'
type NetworkNavigationTarget = 'none' | 'control' | 'bottom'
type NavGroup = 'control' | 'observe'
type NavItem = { id: Page; label: string; group: NavGroup; qnapOnly?: boolean }

const qnapBuild = import.meta.env.VITE_OPENSURGE_TARGET === 'qnap'
const releaseTag = import.meta.env.VITE_OPENSURGE_RELEASE_TAG

const nav: readonly NavItem[] = [
  { id: 'dashboard', label: '总览', group: 'control' },
  { id: 'network', label: '网络设置', group: 'control' },
  { id: 'sources', label: '代理与规则源', group: 'control' },
  { id: 'devices', label: '设备', group: 'control' },
  { id: 'policies', label: '策略', group: 'control' },
  { id: 'management', label: '管理', group: 'control', qnapOnly: true },
  { id: 'connectivity', label: '连通性', group: 'observe' },
  { id: 'diagnostics', label: '诊断', group: 'observe' },
  { id: 'traffic', label: '流量分析', group: 'observe' },
]

const availableNav = nav.filter(item => !item.qnapOnly || qnapBuild)
const commandItems = availableNav.map(item => ({ id: item.id, label: item.label, icon: item.id }))

function currentPage(): Page {
  const candidate = window.location.pathname.split('/').filter(Boolean)[0] as Page | undefined
  return availableNav.some(item => item.id === candidate) ? candidate! : 'dashboard'
}

function initialTheme(): Theme {
  const stored = window.localStorage.getItem('opensurge-theme')
  if (stored === 'dark' || stored === 'light') return stored
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

function initialSidebarCompact() {
  return window.localStorage.getItem('opensurge-sidebar') === 'compact'
}

function focusGatewayControl(target: Exclude<NetworkNavigationTarget, 'none'>) {
  const control = document.getElementById('gateway-control')
  if (!(control instanceof HTMLButtonElement)) return
  const reducedMotion = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  if (target === 'bottom') {
    window.scrollTo?.({ top: document.documentElement.scrollHeight, behavior: reducedMotion ? 'auto' : 'smooth' })
  } else {
    control.scrollIntoView?.({ behavior: reducedMotion ? 'auto' : 'smooth', block: 'center' })
  }
  if (!control.disabled) control.focus({ preventScroll: true })
}

function networkNavigationHash(target: NetworkNavigationTarget) {
  if (target === 'control') return '#gateway-control'
  if (target === 'bottom') return '#gateway-control-bottom'
  return ''
}

export function App() {
  const [page, setPage] = useState<Page>(currentPage)
  const [overview, setOverview] = useState<Overview | null>(null)
  const [error, setError] = useState('')
  const [authenticationRequired, setAuthenticationRequired] = useState(false)
  const [theme, setTheme] = useState<Theme>(initialTheme)
  const [language, setLanguage] = useState<RequestedLanguage>(initialRequestedLanguage)
  const [languageChanging, setLanguageChanging] = useState(false)
  const [devicesDirty, setDevicesDirty] = useState(false)
  const [policiesViewState, setPoliciesViewState] = useState<PoliciesViewState>({ search: '', scope: 'global', activeGroup: null })
  const [sleepPreventionChanging, setSleepPreventionChanging] = useState(false)
  const [notifications, setNotifications] = useState<OperationNotificationItem[]>([])
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [sidebarCompact, setSidebarCompact] = useState(initialSidebarCompact)
  const [commandOpen, setCommandOpen] = useState(false)
  const notificationID = useRef(0)
  const sleepPreventionGeneration = useRef(0)
  const languageGeneration = useRef(0)
  const policiesScrollPosition = useRef<number | null>(null)
  const pageRef = useRef(page)
  const devicesDirtyRef = useRef(devicesDirty)
  pageRef.current = page
  devicesDirtyRef.current = devicesDirty

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    window.localStorage.setItem('opensurge-theme', theme)
  }, [theme])

  useEffect(() => {
    window.localStorage.setItem('opensurge-sidebar', sidebarCompact ? 'compact' : 'expanded')
  }, [sidebarCompact])

  useEffect(() => {
    activateLanguage(language)
    cacheRequestedLanguage(language)
  }, [language])

  useEffect(() => {
    const openCommandPalette = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'k') return
      event.preventDefault()
      setCommandOpen(current => !current)
    }
    window.addEventListener('keydown', openCommandPalette)
    return () => window.removeEventListener('keydown', openCommandPalette)
  }, [])

  const commitLanguage = useCallback(async (nextLanguage: RequestedLanguage) => {
    await prepareLanguage(nextLanguage)
    activateLanguage(nextLanguage)
    setLanguage(nextLanguage)
  }, [])

  const refresh = useCallback(async () => {
    const sleepGeneration = sleepPreventionGeneration.current
    const requestedLanguageGeneration = languageGeneration.current
    try {
      const nextOverview = await api.overview()
      setOverview(current => sleepGeneration === sleepPreventionGeneration.current || !current
        ? nextOverview
        : { ...nextOverview, sleep_prevention: current.sleep_prevention })
      setError('')
      if (requestedLanguageGeneration === languageGeneration.current && isRequestedLanguage(nextOverview.ui_preferences?.language)) {
        await commitLanguage(nextOverview.ui_preferences.language)
      }
    } catch (cause) {
      if (cause instanceof RequestError && cause.status === 401) {
        setAuthenticationRequired(true)
        setError('')
        return
      }
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }, [commitLanguage])

  const changeLanguage = async (nextLanguage: RequestedLanguage) => {
    if (languageChanging || nextLanguage === language) return
    const previousLanguage = language
    languageGeneration.current += 1
    setLanguageChanging(true)
    try {
      await commitLanguage(nextLanguage)
      const preferences = await api.setUIPreferences({ language: nextLanguage })
      languageGeneration.current += 1
      await commitLanguage(preferences.language)
      await refresh()
    } catch (cause) {
      languageGeneration.current += 1
      await commitLanguage(previousLanguage)
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setLanguageChanging(false)
    }
  }

  useEffect(() => {
    const requireAuthentication = () => {
      setAuthenticationRequired(true)
      setError('')
    }
    window.addEventListener(authenticationRequiredEvent, requireAuthentication)
    return () => window.removeEventListener(authenticationRequiredEvent, requireAuthentication)
  }, [])

  useEffect(() => {
    if (authenticationRequired) return
    void refresh()
    const timer = window.setInterval(() => void refresh(), 8000)
    const events = typeof EventSource === 'undefined' ? null : new EventSource('/api/v1/events')
    events?.addEventListener('state', () => void refresh())
    const onPop = () => {
      const next = currentPage()
      if (pageRef.current === 'devices' && next !== 'devices' && devicesDirtyRef.current && !window.confirm(t('设备页还有尚未保存的修改，确定离开并放弃这些修改吗？'))) {
        history.pushState({}, '', '/devices')
        return
      }
      if (pageRef.current === 'devices' && next !== 'devices') setDevicesDirty(false)
      if (pageRef.current === 'policies' && next !== 'policies') policiesScrollPosition.current = window.scrollY
      setPage(next)
      setSidebarOpen(false)
      setCommandOpen(false)
    }
    window.addEventListener('popstate', onPop)
    return () => {
      window.clearInterval(timer)
      events?.close()
      window.removeEventListener('popstate', onPop)
    }
  }, [authenticationRequired, refresh])

  const go = (next: Page, networkTarget: NetworkNavigationTarget = 'none') => {
    setSidebarOpen(false)
    if (next === page) {
      if (networkTarget !== 'none') {
        history.replaceState({}, '', `/${next}${networkNavigationHash(networkTarget)}`)
        focusGatewayControl(networkTarget)
      }
      return
    }
    if (page === 'devices' && next !== 'devices' && devicesDirty && !window.confirm(t('设备页还有尚未保存的修改，确定离开并放弃这些修改吗？'))) return
    if (page === 'devices' && next !== 'devices') setDevicesDirty(false)
    if (page === 'policies' && next !== 'policies') policiesScrollPosition.current = window.scrollY
    history.pushState({}, '', `/${next}${networkNavigationHash(networkTarget)}`)
    setPage(next)
  }

  const selectCommandItem = (id: string) => {
    const target = availableNav.find(item => item.id === id)
    if (!target) return
    setCommandOpen(false)
    go(target.id)
  }

  const setSleepPrevention = async (enabled: boolean) => {
    if (sleepPreventionChanging) return
    sleepPreventionGeneration.current += 1
    setSleepPreventionChanging(true)
    try {
      const sleepPrevention = await api.setSleepPrevention(enabled)
      sleepPreventionGeneration.current += 1
      setOverview(current => current ? { ...current, sleep_prevention: sleepPrevention } : current)
      await refresh()
    } catch (cause) {
      sleepPreventionGeneration.current += 1
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setSleepPreventionChanging(false)
    }
  }

  const notify = useCallback((notification: OperationNotification) => {
    const uncertain = notification.message.includes(operationStatusUnknownMessage) || notification.message.includes(t(operationStatusUnknownMessage))
    const item = { ...notification, title: uncertain ? t('结果尚未确认') : notification.title, id: ++notificationID.current }
    setNotifications(current => [...current, item].slice(-3))
  }, [])

  const dismissNotification = useCallback((id: number) => {
    setNotifications(current => current.filter(notification => notification.id !== id))
  }, [])

  const updatePoliciesViewState = useCallback((patch: Partial<PoliciesViewState>) => {
    setPoliciesViewState(current => {
      const next = { ...current, ...patch }
      return next.search === current.search && next.scope === current.scope && next.activeGroup === current.activeGroup ? current : next
    })
  }, [])

  const updatePoliciesScrollPosition = useCallback((scrollY: number) => {
    policiesScrollPosition.current = scrollY
  }, [])

  const activeItem = availableNav.find(item => item.id === page) ?? availableNav[0]
  const gatewayStatus = statusLabel(overview?.status.gateway, overview?.status.runtime_state)
  const controlNav = availableNav.filter(item => item.group === 'control')
  const observeNav = availableNav.filter(item => item.group === 'observe')

  return <div className={`app-shell ${sidebarCompact ? 'sidebar-compact' : ''} ${sidebarOpen ? 'mobile-nav-open' : ''}`}>
    <div className="app-wallpaper" aria-hidden="true"><span className="wallpaper-orb one" /><span className="wallpaper-orb two" /><span className="wallpaper-orb three" /></div>
    <button type="button" className="mobile-nav-backdrop" aria-label="Close navigation" onClick={() => setSidebarOpen(false)} />
    <aside className="sidebar">
      <div className="sidebar-brand-row">
        <div className="brand"><img className="brand-mark" src="/opensurge-icon.png" alt="" aria-hidden="true" /><div><strong>OpenSurge</strong><small>{qnapBuild ? 'for QNAP' : 'for Mac'}</small></div></div>
        <button type="button" className="sidebar-collapse" aria-label="Toggle compact navigation" aria-pressed={sidebarCompact} onClick={() => setSidebarCompact(current => !current)}><ShellIcon name="collapse" /></button>
      </div>
      <div className="sidebar-nav-scroll">
        <small className="nav-section-label">CONTROL</small>
        <nav aria-label="OpenSurge sections">
          {controlNav.map(item => <button key={item.id} className={page === item.id ? 'active' : ''} aria-current={page === item.id ? 'page' : undefined} title={t(item.label)} onClick={() => go(item.id)}><span className="nav-icon"><ShellIcon name={item.id} /></span><span className="nav-label">{t(item.label)}</span></button>)}
        </nav>
        <small className="nav-section-label secondary">OBSERVE</small>
        <nav aria-label="OpenSurge observability">
          {observeNav.map(item => <button key={item.id} className={page === item.id ? 'active' : ''} aria-current={page === item.id ? 'page' : undefined} title={t(item.label)} onClick={() => go(item.id)}><span className="nav-icon"><ShellIcon name={item.id} /></span><span className="nav-label">{t(item.label)}</span></button>)}
        </nav>
      </div>
      <div className="sidebar-controls">
        <LanguageSelector language={language} changing={languageChanging} onChange={next => void changeLanguage(next)} />
        <button type="button" className="theme-toggle sidebar-theme-toggle" aria-pressed={theme === 'light'} aria-label={t(theme === 'dark' ? '切换为浅色模式' : '切换为深色模式')} onClick={() => setTheme(current => current === 'dark' ? 'light' : 'dark')}><ShellIcon name={theme === 'dark' ? 'sun' : 'moon'} /><span>{t(theme === 'dark' ? '浅色模式' : '深色模式')}</span></button>
        {!qnapBuild && <>
          <label className={`sidebar-switch ${overview?.sleep_prevention?.active ? 'active' : ''}`} title={t('阻止空闲睡眠和合盖睡眠。合盖运行可能明显增加耗电与发热，请勿放入不通风的包内。')}><input type="checkbox" checked={overview?.sleep_prevention?.active ?? false} disabled={!overview || sleepPreventionChanging} onChange={event => void setSleepPrevention(event.target.checked)} /><span><strong>{t(sleepPreventionChanging ? '正在切换…' : '合盖保持运行')}</strong><small>{t(overview?.sleep_prevention?.active ? '系统睡眠已临时禁用' : '默认关闭 · 本次运行有效')}</small></span></label>
          {overview?.sleep_prevention?.error && <small className="sidebar-control-error" role="status">{overview.sleep_prevention.error}</small>}
        </>}
      </div>
      <div className="sidebar-status"><StatusDot status={overview?.status.gateway ?? 'unreachable'} /><div><strong>{gatewayStatus}</strong><small>{qnapBuild ? releaseTag : `${releaseTag} Wind Rose`}</small></div></div>
    </aside>
    <main className="workspace">
      <header className="workspace-toolbar">
        <div className="workspace-toolbar-start">
          <button type="button" className="mobile-menu-button" aria-label="Open navigation" aria-expanded={sidebarOpen} onClick={() => setSidebarOpen(true)}><ShellIcon name="menu" /></button>
          <button type="button" className="global-search-trigger" aria-label={t('打开快速跳转')} onClick={() => setCommandOpen(true)}>
            <ShellIcon name="search" />
            <span className="global-search-copy"><strong>{t('搜索页面、功能或状态…')}</strong><small>{t(activeItem.label)}</small></span>
            <kbd>⌘ K</kbd>
          </button>
        </div>
        <div className="workspace-toolbar-end">
          <span className="toolbar-page-label">{t(activeItem.label)}</span>
          <div className="toolbar-gateway-status" title={gatewayStatus} aria-label={gatewayStatus}><StatusDot status={overview?.status.gateway ?? 'unreachable'} /></div>
        </div>
      </header>
      <div className="workspace-canvas">
        {authenticationRequired ? <section className="session-expired" role="alert"><span aria-hidden="true">!</span><div><h1>{t('Web GUI 与 OpenSurge 的安全连接已过期')}</h1>{qnapBuild ? <p><a href="/auth/">重新登录</a></p> : <p>{t('请点击 macOS 菜单栏中的 OpenSurge 图标，然后选择“打开 OpenSurge 面板”。')}</p>}</div></section> : <>
          {!qnapBuild && overview?.recovery.required && needsNetworkRecoveryWarning(overview.recovery.stage) && <RecoveryBanner recovery={overview.recovery.stage} onOpen={() => go('network', 'control')} />}
          {error && <div className="error-banner" role="alert"><span>!</span><p>{error}</p><button onClick={() => void refresh()}>{t('重试')}</button></div>}
          <PageErrorBoundary key={page}>
            {page === 'dashboard' && <DashboardPage overview={overview} onChanged={refresh} onOpenNetwork={action => go('network', action === 'cleanup' ? 'control' : action === 'stop' ? 'bottom' : 'none')} />}
            {page === 'network' && (qnapBuild
              ? <QNAPNetworkPage overview={overview} onChanged={refresh} onNavigate={() => go('sources')} onNotify={notify} />
              : <NetworkPage overview={overview} onChanged={refresh} onNavigate={() => go('devices')} onNotify={notify} />)}
            {page === 'sources' && (qnapBuild
              ? <QNAPSourcesPage overview={overview} onChanged={refresh} onNotify={notify} />
              : <SourcesPage overview={overview} onChanged={refresh} onNotify={notify} />)}
            {page === 'devices' && <DevicesPage overview={overview} onChanged={refresh} onNavigate={go} onDirtyChange={setDevicesDirty} onNotify={notify} />}
            {page === 'policies' && <PoliciesPage overview={overview} onChanged={refresh} viewState={policiesViewState} onViewStateChange={updatePoliciesViewState} restoreScrollY={policiesScrollPosition.current} onScrollPositionChange={updatePoliciesScrollPosition} />}
            {page === 'management' && qnapBuild && <QNAPManagementPage />}
            {page === 'connectivity' && <ConnectivityPage overview={overview} onChanged={refresh} />}
            {page === 'diagnostics' && <DiagnosticsPage overview={overview} />}
            {page === 'traffic' && <TrafficAnalysisPage />}
          </PageErrorBoundary>
        </>}
      </div>
    </main>
    <CommandPalette open={commandOpen} activeID={page} items={commandItems} onClose={() => setCommandOpen(false)} onSelect={selectCommandItem} />
    {!authenticationRequired && <OperationProgress onOpenDiagnostics={() => go('diagnostics')} />}
    <OperationNotifications notifications={notifications} onDismiss={dismissNotification} />
  </div>
}
