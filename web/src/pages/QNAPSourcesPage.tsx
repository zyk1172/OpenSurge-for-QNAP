import { useCallback, useEffect, useRef, useState } from 'react'
import { api, request } from '../api'
import { Empty, PageHeader, SectionTitle } from '../components/Common'
import type { OperationNotification } from '../components/OperationNotifications'
import { ProfileOverlayPanel } from '../components/ProfileOverlayPanel'
import type { Overview, ProfileOverlay, Source } from '../types'
import { t } from '../i18n'

type Action = 'import-url' | 'import-file' | 'refresh' | 'apply' | 'copy-path' | 'delete' | null

type DeleteSourceResponse = { deleted: boolean; id: string; cleanup_warning?: string }

export function QNAPSourcesPage({ overview, onChanged, onNotify }: {
  overview: Overview | null
  onChanged: () => void | Promise<void>
  onNotify: (notification: OperationNotification) => void
}) {
  const [sources, setSources] = useState<Source[]>([])
  const [overlay, setOverlay] = useState<ProfileOverlay | null>(null)
  const [revision, setRevision] = useState('')
  const [name, setName] = useState('')
  const [url, setURL] = useState('')
  const [pending, setPending] = useState<Source | null>(null)
  const [action, setAction] = useState<Action>(null)
  const [activeSource, setActiveSource] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)
  const running = overview?.status.gateway === 'running' || overview?.status.gateway === 'degraded'
  const busy = action !== null

  const refresh = useCallback(async () => {
    try {
      const [sourceResponse, profileOverlay] = await Promise.all([api.sources(), api.profileOverlay()])
      setSources(sourceResponse.sources ?? [])
      setRevision(sourceResponse.revision)
      setOverlay(profileOverlay)
      setError('')
      return sourceResponse
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
      throw cause
    }
  }, [])

  useEffect(() => { void refresh().catch(() => {}) }, [refresh])

  const run = async (kind: Action, sourceID: string, operation: () => Promise<unknown>, success: string) => {
    setAction(kind)
    setActiveSource(sourceID)
    setError('')
    setMessage('')
    try {
      await operation()
      await refresh()
      setMessage(success)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setAction(null)
      setActiveSource('')
    }
  }

  const importFile = (file: File) => {
    if (!/\.ya?ml$/i.test(file.name)) {
      setError(t('只能导入 .yaml 或 .yml 文件。'))
      return
    }
    void run('import-file', '', () => api.importFile(file), t('{{name}} 已导入并保存为草稿。', { name: file.name }))
  }

  const apply = async () => {
    if (!pending || !revision) return
    const selected = pending
    setAction('apply')
    setActiveSource(selected.id)
    setError('')
    setMessage('')
    try {
      await api.applySource(selected.id, revision)
      const [sourceResponse, profileOverlay] = await Promise.all([api.sources(), api.profileOverlay()])
      setSources(sourceResponse.sources ?? [])
      setRevision(sourceResponse.revision)
      setOverlay(profileOverlay)
      const persisted = sourceResponse.sources?.find(item => item.id === selected.id)
      if (!persisted || (!persisted.desired && !persisted.applied)) {
        throw new Error(t('应用结果未能从持久化状态中确认，请刷新后重试。'))
      }
      setPending(null)
      if (persisted.applied) {
        setMessage(t('{{name}} 已成为当前运行版本。', { name: selected.name }))
        onNotify({ tone: 'success', title: t('订阅已应用'), message: t('当前网关已使用该版本。') })
      } else {
        setMessage(t('{{name}} 已设为下次启动版本。', { name: selected.name }))
        onNotify({ tone: 'success', title: t('下次启动版本已保存'), message: t('配置已持久化。') })
      }
      await Promise.resolve(onChanged())
    } catch (cause) {
      const failure = cause instanceof Error ? cause.message : String(cause)
      setError(failure)
      onNotify({ tone: 'error', title: t('订阅应用失败'), message: failure })
    } finally {
      setAction(null)
      setActiveSource('')
    }
  }

  const copyPath = async (source: Source) => {
    setAction('copy-path')
    setActiveSource(source.id)
    setError('')
    try {
      const location = await api.sourceSnapshotLocation(source.id)
      await copyText(location.path)
      setMessage(t('{{name}} 的快照路径已复制。', { name: source.name }))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setAction(null)
      setActiveSource('')
    }
  }

  const deleteSource = async (source: Source) => {
    if (source.applied || source.desired || busy) return
    if (!window.confirm(t('确定删除 {{name}}？本地快照和保存的订阅凭据也会删除。', { name: source.name }))) return
    setAction('delete')
    setActiveSource(source.id)
    setError('')
    setMessage('')
    try {
      const result = await request<DeleteSourceResponse>(`/api/v1/qnap/sources/${encodeURIComponent(source.id)}`, { method: 'DELETE' })
      await refresh()
      setMessage(t('{{name}} 已删除。', { name: source.name }))
      if (result.cleanup_warning) {
        onNotify({ tone: 'warning', title: t('来源已删除'), message: t('来源记录已删除，但清理旧文件时出现提示：{{error}}', { error: result.cleanup_warning }) })
      } else {
        onNotify({ tone: 'success', title: t('来源已删除'), message: t('本地快照与保存的订阅凭据已一并清理。') })
      }
    } catch (cause) {
      const failure = cause instanceof Error ? cause.message : String(cause)
      setError(failure)
      onNotify({ tone: 'error', title: t('删除来源失败'), message: failure })
    } finally {
      setAction(null)
      setActiveSource('')
    }
  }

  return <>
    <PageHeader eyebrow="QNAP SOURCES" title="代理与规则源" description="导入、校验并应用 Mihomo 配置来源。" />
    {error && <div className="notice warn" role="alert"><strong>{t('操作未完成')}</strong><p>{error}</p></div>}
    {message && <div className="ok-notice" role="status"><strong>{t('状态已更新')}</strong><p>{message}</p></div>}

    <section className="section source-import-panel">
      <SectionTitle title="添加配置来源" subtitle="导入后先保存为草稿，应用前自动校验" />
      <div className="source-import-grid">
        <article className="source-import-card">
          <div className="source-import-head"><span aria-hidden="true">↗</span><div><small>REMOTE PROFILE</small><h3>{t('HTTPS 订阅')}</h3></div></div>
          <label><span>{t('来源名称')}</span><input value={name} onChange={event => setName(event.target.value)} placeholder={t('例如 Home')} /></label>
          <label><span>{t('订阅地址')}</span><input value={url} onChange={event => setURL(event.target.value)} placeholder="https://…" /></label>
          <button className="primary source-action-button" type="button" disabled={busy || !url} onClick={() => void run('import-url', '', () => api.importURL(name, url), t('订阅已保存为草稿。'))}>{action === 'import-url' ? t('正在导入…') : t('导入为草稿')}</button>
        </article>
        <article className="source-import-card local">
          <div className="source-import-head"><span aria-hidden="true">⇧</span><div><small>LOCAL PROFILE</small><h3>{t('本地 Mihomo YAML')}</h3></div></div>
          <label
            className={`dropzone source-dropzone ${dragging ? 'drag-active' : ''}`}
            onDragEnter={event => { event.preventDefault(); if (!busy) { dragDepth.current += 1; setDragging(true) } }}
            onDragOver={event => { event.preventDefault(); if (!busy) event.dataTransfer.dropEffect = 'copy' }}
            onDragLeave={event => { event.preventDefault(); dragDepth.current = Math.max(0, dragDepth.current - 1); if (!dragDepth.current) setDragging(false) }}
            onDrop={event => {
              event.preventDefault(); dragDepth.current = 0; setDragging(false)
              if (busy) return
              const files = Array.from(event.dataTransfer.files)
              if (files.length !== 1) { setError(t('一次只能拖入一个 YAML 文件。')); return }
              importFile(files[0])
            }}
          >
            <span className="dropzone-icon" aria-hidden="true">＋</span>
            <strong>{t(action === 'import-file' ? '正在导入…' : dragging ? '松开即可导入' : '选择或拖入 YAML')}</strong>
            <small>.yaml / .yml</small>
            <input type="file" accept=".yaml,.yml,text/yaml" disabled={busy} onChange={event => { const file = event.target.files?.[0]; if (file) importFile(file); event.currentTarget.value = '' }} />
          </label>
        </article>
      </div>
    </section>

    <section className="section source-library">
      <SectionTitle title="已导入来源" subtitle="持久化版本与当前应用状态" />
      {sources.length ? <div className="source-grid">{sources.map(source => {
        const inventory = source.effective_inventory ?? source.inventory
        const valid = source.valid && source.overlay_compatible !== false
        const state = source.applied ? t('运行版本') : source.desired ? t('下次启动版本') : valid ? t('结构有效') : t('存在冲突')
        const applying = action === 'apply' && activeSource === source.id
        const refreshing = action === 'refresh' && activeSource === source.id
        const copying = action === 'copy-path' && activeSource === source.id
        const deleting = action === 'delete' && activeSource === source.id
        const inUse = source.applied || source.desired
        return <article className="source-card" key={source.id}>
          <div className="source-head"><div><small>{source.kind}</small><h3>{source.name}</h3></div><span className={source.applied || source.desired || valid ? 'pill ok' : 'pill bad'}>{state}</span></div>
          <p className="source-origin" title={source.origin}><span aria-hidden="true">⌁</span>{source.origin}</p>
          {source.snapshot_display_path && <div className="source-location">
            <div className="source-location-copy"><small>{t('持久化快照')}</small><code dir="ltr">{source.snapshot_display_path}</code><span>{t('OpenSurge 管理')}</span></div>
            <div className="source-location-actions"><button type="button" disabled={busy} onClick={() => void copyPath(source)}>{copying ? t('复制中…') : t('复制路径')}</button></div>
          </div>}
          <div className="source-inventory">
            <Metric value={inventory?.proxy_groups?.length ?? 0} label="策略组" />
            <Metric value={inventory?.proxy_providers?.length ?? 0} label="Provider" />
            <Metric value={inventory?.rule_count ?? 0} label="规则" />
            <Metric value={(source.versions?.length ?? 0) + 1} label="版本" />
          </div>
          <div className={`source-validation ${valid ? 'valid' : 'invalid'}`}><span aria-hidden="true">{valid ? '✓' : '!'}</span><div><strong>{t(valid ? '候选配置可应用' : '候选配置存在问题')}</strong><small>{source.overlay_validation || source.validation || ''}</small></div></div>
          <div className="source-actions">
            {source.origin.startsWith('https://') && <button type="button" disabled={busy} onClick={() => void run('refresh', source.id, () => api.refreshSource(source.id), t('{{name}} 已刷新为新草稿。', { name: source.name }))}>{refreshing ? t('正在刷新…') : t('刷新草稿')}</button>}
            <button className="primary" type="button" disabled={busy || !revision || !valid || (source.desired && !running) || source.applied} onClick={() => setPending(source)}>{applying ? t('正在应用…') : t(running ? '应用并重载' : '设为下次启动版本')}</button>
            <button className="danger" type="button" disabled={busy || inUse} title={inUse ? t('当前运行或下次启动使用中的来源不能直接删除。') : t('删除配置来源')} onClick={() => void deleteSource(source)}>{deleting ? t('正在删除…') : t('删除配置')}</button>
          </div>
        </article>
      })}</div> : <Empty text={t('尚未导入任何来源')} />}
    </section>

    <ProfileOverlayPanel overlay={overlay} sources={sources} onSaved={async saved => { setOverlay(saved); await refresh() }} />

    {pending && <dialog className="reload-dialog" open aria-modal="true" aria-labelledby="qnap-source-apply-title">
      <h2 id="qnap-source-apply-title">{t(running ? '应用并重载网关？' : '设为下次启动版本？')}</h2>
      <p>{t(running ? '应用前会校验最终配置。' : '所选版本将持久化，并在下次启动时使用。')}</p>
      <div className="dialog-actions"><button type="button" disabled={busy} onClick={() => setPending(null)}>{t('取消')}</button><button className="primary" type="button" autoFocus disabled={busy} onClick={() => void apply()}>{action === 'apply' ? t('正在验证…') : t(running ? '确认应用' : '确认保存')}</button></div>
    </dialog>}
  </>
}

function Metric({ value, label }: { value: number; label: string }) {
  return <span><strong>{value}</strong><small>{t(label)}</small></span>
}

async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value)
    return
  }
  const field = document.createElement('textarea')
  field.value = value
  field.setAttribute('readonly', '')
  field.style.position = 'fixed'
  field.style.opacity = '0'
  document.body.appendChild(field)
  field.select()
  const copied = document.execCommand('copy')
  field.remove()
  if (!copied) throw new Error(t('浏览器未允许复制路径，请手动复制上方容器路径。'))
}
