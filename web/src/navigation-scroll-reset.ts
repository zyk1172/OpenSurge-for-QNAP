const navigationEvent = 'opensurge:navigation'

type PatchedHistory = History & { __opensurgeScrollResetInstalled?: boolean }

const patchedHistory = window.history as PatchedHistory

if (!patchedHistory.__opensurgeScrollResetInstalled) {
  patchedHistory.__opensurgeScrollResetInstalled = true

  const pushState = window.history.pushState.bind(window.history)
  const replaceState = window.history.replaceState.bind(window.history)

  window.history.pushState = ((data: unknown, unused: string, url?: string | URL | null) => {
    pushState(data, unused, url)
    window.dispatchEvent(new Event(navigationEvent))
  }) as History['pushState']

  window.history.replaceState = ((data: unknown, unused: string, url?: string | URL | null) => {
    replaceState(data, unused, url)
    window.dispatchEvent(new Event(navigationEvent))
  }) as History['replaceState']

  const resetRouteScroll = () => {
    // Hash navigation is intentional (for example the gateway lifecycle
    // control). Let that target own the scroll position.
    if (window.location.hash) return
    window.requestAnimationFrame(() => {
      window.scrollTo({ top: 0, left: 0, behavior: 'auto' })
    })
  }

  window.addEventListener(navigationEvent, resetRouteScroll)
  window.addEventListener('popstate', resetRouteScroll)
}
