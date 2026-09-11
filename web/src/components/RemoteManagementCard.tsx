import { useEffect, useMemo, useState } from 'react'
import { t } from '../i18n'

export type RemoteTokenStatus = {
  schema_version: number
  enabled: boolean
  token_prefix?: string
  created_at?: string
  api_base: string
  capabilities: string
}

type RemoteTokenIssued = RemoteTokenStatus & { token: string }

async function remoteAuthRequest<T>(method: 'GET' | 'POST' | 'DELETE'): Promise<T> {
  const response = await fetch('/api/auth/remote-token', {
    method,
    credentials: 'same-origin',
    headers: method === 'GET' ? undefined : { 'Content-Type': 'application/json' },
  })
  if (!response.ok) {
    let message = response.statusText
    try {
      const body = await response.json() as { error?: string | { message?: string } }
      if (typeof body.error === 'string') message = body.error
      else if (body.error?.message) message = body.error.message
    } catch { /* non-JSON error */ }
    throw new Error(message || `HTTP ${response.status}`)
  }
  return response.json() as Promise<T>
}

async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = value
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  document.execCommand('copy')
  textarea.remove()
}

export function RemoteManagementCard() {
  const [status, setStatus] = useState<RemoteTokenStatus | null>(null)
  const [issuedToken, setIssuedToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')

  const load = async () => {
    try {
      setStatus(await remoteAuthRequest<RemoteTokenStatus>('GET'))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  useEffect(() => { void load() }, [])

  const apiBase = useMemo(() => status ? `${window.location.origin}${status.api_base}` : `${window.location.origin}/api/remote/v1`, [status])
  const capabilities = useMemo(() => status ? `${window.location.origin}${status.capabilities}` : `${window.location.origin}/api/remote/v1/capabilities`, [status])
  const connectionBlock = [
    t('OpenSurge for QNAP 远程管理'),
    t('API 地址：{{url}}', { url: apiBase }),
    t('认证：Authorization: Bearer <TOKEN>'),
    t('能力清单：{{url}}', { url: capabilities }),
    t('先读取 GET /capabilities；配置写入保留 ETag/If-Match，异步操作按 operation id 查询结果。'),
  ].join('\n')

  const copy = async (label: string, value: string) => {
    try {
      await copyText(value)
      setCopied(label)
      window.setTimeout(() => setCopied(current => current === label ? '' : current), 1800)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  const rotate = async () => {
    if (status?.enabled && !window.confirm(t('确定轮换远程管理令牌？现有令牌将立即失效。'))) return
    setBusy(true)
    setError('')
    setCopied('')
    try {
      const next = await remoteAuthRequest<RemoteTokenIssued>('POST')
      setStatus(next)
      setIssuedToken(next.token)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setBusy(false)
    }
  }

  const revoke = async () => {
    if (!status?.enabled || !window.confirm(t('确定撤销远程管理令牌？现有 AI/API 客户端将立即失去访问权限。'))) return
    setBusy(true)
    setError('')
    setCopied('')
    try {
      const next = await remoteAuthRequest<RemoteTokenStatus>('DELETE')
      setStatus(next)
      setIssuedToken('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      setBusy(false)
    }
  }

  return <section className="section qnap-remote-management" id="remote-management">
    <div className="section-title-row">
      <div>
        <p className="eyebrow">{t('远程管理')}</p>
        <h2>{t('局域网管理令牌')}</h2>
        <p className="muted">{t('供可信 AI 或自动化客户端调用 QNAP 管理 API；内部 Control Token 不会暴露。')}</p>
      </div>
      <span className={`status-badge ${status?.enabled ? 'ok' : ''}`}>{t(status?.enabled ? '已启用' : '未启用')}</span>
    </div>

    {error && <div className="notice warn" role="alert"><strong>{t('远程管理错误')}</strong><p>{error}</p></div>}

    <div className="inventory">
      <span><strong>{t('API 地址')}</strong><br /><code>{apiBase}</code></span>
      <span><strong>{t('能力清单')}</strong><br /><code>{capabilities}</code></span>
      <span><strong>{t('令牌')}</strong><br />{status?.enabled ? <code>{status.token_prefix}…</code> : t('未创建')}</span>
      <span><strong>{t('创建时间')}</strong><br />{status?.created_at ? new Date(status.created_at).toLocaleString() : '—'}</span>
    </div>

    <div className="source-actions">
      <button type="button" onClick={() => void copy('api', apiBase)}>{copied === 'api' ? t('已复制') : t('复制 API 地址')}</button>
      <button type="button" onClick={() => void copy('agent', connectionBlock)}>{copied === 'agent' ? t('已复制') : t('复制 AI 配置')}</button>
      <button className="primary" type="button" disabled={busy} onClick={() => void rotate()}>{busy ? t('处理中…') : status?.enabled ? t('轮换令牌') : t('创建令牌')}</button>
      {status?.enabled && <button className="danger" type="button" disabled={busy} onClick={() => void revoke()}>{t('撤销令牌')}</button>}
    </div>

    {issuedToken && <div className="notice warn" role="status">
      <strong>{t('请立即复制此令牌，关闭后不再显示。')}</strong>
      <p>{t('仅保存 SHA-256 摘要；轮换或撤销后旧令牌立即失效。')}</p>
      <div className="remote-token-row">
        <input aria-label={t('远程管理令牌')} readOnly value={issuedToken} onFocus={event => event.currentTarget.select()} />
        <button type="button" onClick={() => void copy('token', issuedToken)}>{copied === 'token' ? t('已复制') : t('复制令牌')}</button>
      </div>
    </div>}

    <div className="notice remote-management-usage">
      <strong>{t('AI/API 用法')}</strong>
      <p>{t('使用 Authorization: Bearer <TOKEN> 访问远程 API，并先读取 GET /capabilities。')}</p>
      <p>{t('仅开放 QNAP 管理面；浏览器 Cookie 和宿主网络修改接口不可用。')}</p>
    </div>
  </section>
}
