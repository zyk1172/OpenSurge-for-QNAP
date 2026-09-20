import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'
import { Empty, PageHeader } from '../components/Common'
import { OutletSummary } from '../components/OutletSummary'
import { PolicyGroupHealthCard } from '../components/PolicyGroupHealthCard'
import { PolicyGroupNav } from '../components/PolicyGroupNav'
import { usePolicyWorkspace } from '../hooks/usePolicyWorkspace'
import { policyDisplayName } from '../policyDisplay'
import type { LocalRouting, Overview, ProxyGroup, ProxyHealthEntry } from '../types'
import { t } from '../i18n'

export type PolicyScope = 'all' | 'global' | 'device'

export type PoliciesViewState = {
  search: string
  scope: PolicyScope
  activeGroup: string | null
}

type PoliciesPageProps = {
  overview: Overview | null
  onChanged: () => Promise<void>
  viewState: PoliciesViewState
  onViewStateChange: (patch: Partial<PoliciesViewState>) => void
  restoreScrollY: number | null
  onScrollPositionChange: (scrollY: number) => void
}

const emptyGroups: ProxyGroup[] = []
const qnapBuild = import.meta.env.VITE_OPENSURGE_TARGET === 'qnap'

export function PoliciesPage({ overview, onChanged, viewState, onViewStateChange, restoreScrollY, onScrollPositionChange }: PoliciesPageProps) {
  const { search, scope, activeGroup } = viewState
  const refreshKey = JSON.stringify([overview?.revision, overview?.status.gateway, overview?.status.mihomo, overview?.desired_digest, overview?.applied_digest, overview?.desired_profile_digest, overview?.applied_profile_digest, overview?.policies])
  const { snapshot, byName, testing, loading, error, refresh, test, select: selectWorkspacePolicy } = usePolicyWorkspace(refreshKey)
  const groupRefs = useRef(new Map<string, HTMLElement>())
  const controlsRef = useRef<HTMLDivElement | null>(null)
  const navigationTargetRef = useRef<string | null>(activeGroup)
  const navigationUnlockTimerRef = useRef<number | null>(null)
  const initialRestoreGroup = useRef(activeGroup)
  const initialRestoreScrollY = useRef(restoreScrollY)
  const initialRestoreDone = useRef(false)
  const activeGroupRef = useRef(activeGroup)
  activeGroupRef.current = activeGroup
  const groups = snapshot?.groups ?? emptyGroups
  const filteredGroups = useMemo(() => groups.filter(group => {
    const device = group.name.startsWith('device/')
    if (scope === 'global' && device) return false
    if (scope === 'device' && !device) return false
    const query = search.trim().toLowerCase()
    return !query || group.name.toLowerCase().includes(query) || policyDisplayName(group.name, byName.get(group.name)).toLowerCase().includes(query) || group.options.some(option => option.toLowerCase().includes(query))
  }), [groups, scope, search, byName])
  const groupNames = useMemo(() => filteredGroups.map(group => group.name), [filteredGroups])
  const visibleNames = useMemo(() => [...new Set(filteredGroups.flatMap(group => group.options))], [filteredGroups])
  const testableNames = useMemo(() => visibleNames.filter(name => byName.get(name)?.probeable), [visibleNames, byName])
  const reachable = visibleNames.filter(name => byName.get(name)?.status === 'reachable').length
  const tested = visibleNames.filter(name => {
    const status = byName.get(name)?.status
    return status && status !== 'untested' && status !== 'not_applicable'
  }).length

  const select = async (group: string, policy: string) => {
    await selectWorkspacePolicy(group, policy)
    await onChanged()
  }

  const registerGroup = useCallback((name: string) => (node: HTMLElement | null) => {
    if (node) groupRefs.current.set(name, node)
    else groupRefs.current.delete(name)
  }, [])

  const navigateToGroup = useCallback((name: string) => {
    const target = groupRefs.current.get(name)
    if (!target) return
    const reducedMotion = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
    navigationTargetRef.current = name
    activeGroupRef.current = name
    if (navigationUnlockTimerRef.current !== null) window.clearTimeout(navigationUnlockTimerRef.current)
    navigationUnlockTimerRef.current = window.setTimeout(() => {
      navigationTargetRef.current = null
      navigationUnlockTimerRef.current = null
    }, reducedMotion ? 100 : 1200)
    onViewStateChange({ activeGroup: name })
    target.scrollIntoView?.({ behavior: reducedMotion ? 'auto' : 'smooth', block: 'start' })
  }, [onViewStateChange])

  useEffect(() => () => {
    if (navigationUnlockTimerRef.current !== null) window.clearTimeout(navigationUnlockTimerRef.current)
  }, [])

  useLayoutEffect(() => {
    if (!snapshot || initialRestoreDone.current) return
    initialRestoreDone.current = true
    const name = initialRestoreGroup.current
    const target = name ? groupRefs.current.get(name) : undefined
    if (target) {
      target.scrollIntoView?.({ behavior: 'auto', block: 'start' })
      navigationUnlockTimerRef.current = window.setTimeout(() => {
        navigationTargetRef.current = null
        navigationUnlockTimerRef.current = null
      }, 250)
    }
    else if (initialRestoreScrollY.current !== null) window.scrollTo?.({ top: initialRestoreScrollY.current, behavior: 'auto' })
  }, [snapshot])

  useEffect(() => {
    if (!snapshot) return
    if (activeGroup && !groupNames.includes(activeGroup)) onViewStateChange({ activeGroup: null })
  }, [snapshot, activeGroup, groupNames, onViewStateChange])

  useEffect(() => {
    if (!snapshot) return
    let frame = 0
    const syncActiveGroup = () => {
      frame = 0
      onScrollPositionChange(window.scrollY)
      const marker = Math.min(180, Math.max(96, window.innerHeight * 0.2))
      const atPageBottom = window.scrollY > 0 && window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 2
      const lockedGroup = navigationTargetRef.current
      const lockedElement = lockedGroup ? groupRefs.current.get(lockedGroup) : undefined
      const controlsBottom = controlsRef.current?.getBoundingClientRect().bottom ?? marker
      const lockedTop = lockedElement?.getBoundingClientRect().top
      const reachedLockedGroup = lockedTop !== undefined && Math.abs(lockedTop - controlsBottom - 8) <= 12
      if (lockedGroup && lockedElement && !reachedLockedGroup && !atPageBottom) {
        if (lockedGroup !== activeGroupRef.current) onViewStateChange({ activeGroup: lockedGroup })
        return
      }
      if (lockedGroup && (reachedLockedGroup || atPageBottom)) {
        navigationTargetRef.current = null
        if (navigationUnlockTimerRef.current !== null) window.clearTimeout(navigationUnlockTimerRef.current)
        navigationUnlockTimerRef.current = null
      }
      let nextGroup: string | null = null
      for (const name of groupNames) {
        const element = groupRefs.current.get(name)
        if (!element) continue
        if (element.getBoundingClientRect().top <= marker) nextGroup = name
        else break
      }
      if (!nextGroup && activeGroupRef.current && groupNames.includes(activeGroupRef.current)) {
        const activeElement = groupRefs.current.get(activeGroupRef.current)
        const activeRect = activeElement?.getBoundingClientRect()
        if (activeRect && activeRect.top < window.innerHeight && activeRect.bottom > controlsBottom) nextGroup = activeGroupRef.current
      }
      const lastGroup = groupNames.at(-1)
      const lastElement = lastGroup ? groupRefs.current.get(lastGroup) : undefined
      if (atPageBottom && lastGroup && lastElement && lastElement.getBoundingClientRect().top < window.innerHeight) nextGroup = lastGroup
      if (nextGroup !== activeGroupRef.current) onViewStateChange({ activeGroup: nextGroup })
    }
    const scheduleSync = () => {
      if (!frame) frame = window.requestAnimationFrame(syncActiveGroup)
    }
    syncActiveGroup()
    window.addEventListener('scroll', scheduleSync, { passive: true })
    window.addEventListener('resize', scheduleSync)
    return () => {
      window.cancelAnimationFrame(frame)
      window.removeEventListener('scroll', scheduleSync)
      window.removeEventListener('resize', scheduleSync)
    }
  }, [snapshot, groupNames, onScrollPositionChange, onViewStateChange])

  return <>
    <PageHeader eyebrow="POLICIES" title={qnapBuild ? '策略与节点' : '策略与节点健康'} description={qnapBuild ? '选择策略出口并查看节点延迟。' : '查看当前配置的策略组、节点选择与延迟；未启动网关时也可以提前选择出口。'} action={<div className="source-head">{snapshot && <span className={`effect-badge ${snapshot.mode === 'running' ? 'live' : ''}`}>{t(snapshot.mode === 'running' ? '运行中配置' : '待启动配置')}</span>}<button className="primary" type="button" disabled={!testableNames.length || testableNames.some(name => testing.has(name))} onClick={() => void test(testableNames)}>{testing.size ? t('正在检测 {{count}} 个节点…', { count: testing.size }) : t('检测当前视图')}</button></div>} />
    <LocalMacGlobalPolicy
      running={overview?.status.gateway === 'running'}
      healthByName={byName}
      testing={testing}
      onTest={test}
      onChanged={async () => { await onChanged(); await refresh() }}
    />
    <section className="policy-health-overview" aria-label={t('节点健康概览')}><div><small>{t('当前视图')}</small><strong>{filteredGroups.length}</strong><span>{t('策略组')}</span></div><div><small>{t('已检测')}</small><strong>{tested}</strong><span>{t('出口')}</span></div><div><small>{t('当前可达')}</small><strong>{reachable}</strong><span>{t('出口')}</span></div><div className="health-legend"><span><i className="legend-dot excellent" />{t('快速')}</span><span><i className="legend-dot good" />{t('可用')}</span><span><i className="legend-dot slow" />{t('较慢')}</span><span><i className="legend-dot unreachable" />{t('不可达')}</span></div></section>
    <div className="policy-controls-sticky" ref={controlsRef}>
      <section className="policy-toolbar"><label className="policy-search"><span className="sr-only">{t('搜索策略组或节点')}</span><input type="search" value={search} placeholder={t('搜索策略组或节点')} onChange={event => onViewStateChange({ search: event.target.value })} /></label><div className="segmented" role="group" aria-label={t('策略组范围')}>{([['global', '全局策略'], ['device', '设备策略'], ['all', '全部']] as const).map(([value, label]) => <button type="button" key={value} aria-pressed={scope === value} onClick={() => onViewStateChange({ scope: value })}>{t(label)}</button>)}</div></section>
      <PolicyGroupNav groups={groupNames} activeGroup={activeGroup} onNavigate={navigateToGroup} displayName={name => policyDisplayName(name, byName.get(name))} />
    </div>
    {error && <div className="notice warn" role="alert">{t('策略配置暂不可用：{{error}}', { error })} <button type="button" disabled={loading} onClick={() => void refresh()}>{t('重试')}</button></div>}
    <section className="policy-health-list">{filteredGroups.map(group => <PolicyGroupHealthCard key={group.name} group={group} search={search.trim()} healthByName={byName} testing={testing} onTest={test} onSelect={policy => select(group.name, policy)} articleRef={registerGroup(group.name)} navigationActive={group.name === activeGroup} />)}</section>
    {!snapshot && loading && <p role="status">{t('正在准备策略配置…')}</p>}
    {snapshot && !filteredGroups.length && <Empty text={t(groups.length ? '当前筛选没有匹配的策略组或节点' : qnapBuild ? '当前配置没有策略组。请先导入 Mihomo YAML 或添加全局策略组。' : '当前配置还没有策略组；可以导入 mihomo YAML，也可以只在“全局附加配置”中添加节点与策略组。')} />}
    <p className="evidence-note"><strong>{t('检测范围：')}</strong>{t(qnapBuild ? '测速由 OpenSurge 网关经 Mihomo 发起，仅代表网关路径。' : '延迟由网关 Mac 上的 mihomo 访问探测地址得到；它不代表某台下游设备的 DHCP、DNS 或 TUN 路径已经完成端到端验收。')}</p>
  </>
}

function LocalMacGlobalPolicy({
  running,
  healthByName,
  testing,
  onTest,
  onChanged,
}: {
  running: boolean
  healthByName: Map<string, ProxyHealthEntry>
  testing: Set<string>
  onTest: (names: string[], group?: string) => Promise<void>
  onChanged: () => Promise<void>
}) {
  const [routing, setRouting] = useState<LocalRouting | null>(null)
  const [error, setError] = useState('')
  const requestVersion = useRef(0)

  const refresh = useCallback(async () => {
    const version = ++requestVersion.current
    if (!running) {
      setRouting(null)
      setError('')
      return
    }
    try {
      const updated = await api.localRouting()
      if (version === requestVersion.current) { setRouting(updated); setError('') }
    } catch (cause) {
      if (version === requestVersion.current) setError(cause instanceof Error ? cause.message : String(cause))
    }
  }, [running])

  useEffect(() => {
    void refresh()
    return () => { requestVersion.current += 1 }
  }, [refresh])

  const select = async (policy: string) => {
    if (!routing) return
    const updated = await api.setLocalRouting(routing.mode, policy)
    setRouting(updated)
    await onChanged()
  }

  return <section className="policy-local-mac" aria-labelledby="policy-local-mac-title">
    <div className="policy-local-mac-copy">
      <div className="source-head"><div><small>{qnapBuild ? 'GATEWAY' : 'THIS MAC'}</small><h2 id="policy-local-mac-title">{t(qnapBuild ? '网关本机出口' : '本机全局策略组')}</h2></div><span className="effect-badge live">{t(qnapBuild ? '仅影响网关' : '仅影响本机')}</span></div>
      <p>{t(qnapBuild ? '不影响下游设备策略。' : '设备页选择“固定出口”时使用；更换策略不会改变下游设备。')}</p>
    </div>
    <div className="policy-local-mac-outlet">
      {running && routing?.global_group
        ? <OutletSummary
            title={t(qnapBuild ? '网关当前出口' : '本机全局出口')}
            ariaLabel={t(qnapBuild ? '网关本机出口 当前策略 {{selected}}' : '本机全局策略组 当前策略 {{selected}}', { selected: routing.global_group.selected })}
            group={routing.global_group}
            healthByName={healthByName}
            testing={testing}
            onTest={onTest}
            onSelect={select}
          />
        : <button className="outlet-summary unavailable" type="button" disabled><span className="outlet-summary-copy"><small>{t(qnapBuild ? '网关当前出口' : '本机全局出口')}</small><strong>{t(running ? '正在读取…' : '启动网关后可用')}</strong></span></button>}
      {error && <div className="notice warn" role="alert">{error}</div>}
    </div>
  </section>
}
