import { useEffect, useMemo, useRef, useState } from 'react'
import { t } from '../i18n'
import { ShellIcon, type ShellIconName } from './ShellIcon'

export interface CommandPaletteItem {
  id: string
  label: string
  icon: ShellIconName
}

export function CommandPalette({
  open,
  activeID,
  items,
  onClose,
  onSelect,
}: {
  open: boolean
  activeID: string
  items: CommandPaletteItem[]
  onClose: () => void
  onSelect: (id: string) => void
}) {
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const inputRef = useRef<HTMLInputElement | null>(null)

  const filtered = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase()
    if (!needle) return items
    return items.filter(item => `${t(item.label)} ${item.id}`.toLocaleLowerCase().includes(needle))
  }, [items, query])

  useEffect(() => {
    if (!open) return
    setQuery('')
    setActiveIndex(0)
    const frame = window.requestAnimationFrame(() => inputRef.current?.focus())
    return () => window.cancelAnimationFrame(frame)
  }, [open])

  useEffect(() => {
    if (activeIndex < filtered.length) return
    setActiveIndex(Math.max(0, filtered.length - 1))
  }, [activeIndex, filtered.length])

  useEffect(() => {
    if (!open) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onClose()
        return
      }
      if (event.key === 'ArrowDown') {
        event.preventDefault()
        setActiveIndex(current => filtered.length ? (current + 1) % filtered.length : 0)
        return
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault()
        setActiveIndex(current => filtered.length ? (current - 1 + filtered.length) % filtered.length : 0)
        return
      }
      if (event.key === 'Enter' && filtered[activeIndex]) {
        event.preventDefault()
        onSelect(filtered[activeIndex].id)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [activeIndex, filtered, onClose, onSelect, open])

  if (!open) return null

  return <div className="command-palette-layer" role="presentation" onMouseDown={event => {
    if (event.target === event.currentTarget) onClose()
  }}>
    <section className="command-palette" role="dialog" aria-modal="true" aria-labelledby="command-palette-title">
      <header className="command-palette-search">
        <ShellIcon name="search" />
        <input
          ref={inputRef}
          value={query}
          onChange={event => { setQuery(event.target.value); setActiveIndex(0) }}
          placeholder={t('搜索 OpenSurge 页面…')}
          aria-label={t('搜索 OpenSurge 页面')}
        />
        <kbd>ESC</kbd>
      </header>
      <div className="command-palette-heading">
        <strong id="command-palette-title">{t('快速跳转')}</strong>
        <small>{t('输入页面名称，然后回车打开。')}</small>
      </div>
      <div className="command-palette-results" role="listbox" aria-label={t('OpenSurge 页面')}>
        {filtered.length ? filtered.map((item, index) => <button
          key={item.id}
          type="button"
          className={`${index === activeIndex ? 'active' : ''} ${item.id === activeID ? 'current' : ''}`}
          role="option"
          aria-selected={index === activeIndex}
          onMouseEnter={() => setActiveIndex(index)}
          onClick={() => onSelect(item.id)}
        >
          <span className="command-result-icon"><ShellIcon name={item.icon} /></span>
          <span className="command-result-copy"><strong>{t(item.label)}</strong><small>/{item.id}</small></span>
          {item.id === activeID ? <span className="command-current-badge">{t('当前页面')}</span> : <span className="command-enter-hint">↵</span>}
        </button>) : <div className="command-empty">{t('没有匹配的页面')}</div>}
      </div>
    </section>
  </div>
}
