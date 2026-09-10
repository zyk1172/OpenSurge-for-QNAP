import { useEffect, useMemo, useState } from 'react'

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
  const connectionBlock = useMemo(() => [
    'OpenSurge for QNAP remote management',
    `Base URL: ${apiBase}`,
    'Authentication: Authorization: Bearer <TOKEN>',
    `Capabilities: ${capabilities}`,
    'Start with GET /capabilities, then use the listed endpoints. Preserve ETag/If-Match on configuration writes and follow operation ids for asynchronous gateway actions.',
  ].join('\n'), [apiBase, capabilities])

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
    if (status?.enabled && !window.confirm('Rotate the remote management token? The existing token will stop working immediately.')) return
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
    if (!status?.enabled || !window.confirm('Revoke the remote management token now? Existing AI/API clients will lose access immediately.')) return
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
        <p className="eyebrow">REMOTE AI MANAGEMENT</p>
        <h2>LAN management token</h2>
        <p className="muted">Give an AI agent or automation client full access to the QNAP management API without exposing the loopback Control token.</p>
      </div>
      <span className={`status-badge ${status?.enabled ? 'ok' : ''}`}>{status?.enabled ? 'Enabled' : 'Disabled'}</span>
    </div>

    {error && <div className="notice warn" role="alert"><strong>Remote management error</strong><p>{error}</p></div>}

    <div className="inventory">
      <span><strong>API base</strong><br /><code>{apiBase}</code></span>
      <span><strong>Discovery</strong><br /><code>{capabilities}</code></span>
      <span><strong>Token</strong><br />{status?.enabled ? <code>{status.token_prefix}…</code> : 'Not created'}</span>
      <span><strong>Created</strong><br />{status?.created_at ? new Date(status.created_at).toLocaleString() : '—'}</span>
    </div>

    <div className="source-actions">
      <button type="button" onClick={() => void copy('api', apiBase)}>{copied === 'api' ? 'Copied API URL' : 'Copy API URL'}</button>
      <button type="button" onClick={() => void copy('agent', connectionBlock)}>{copied === 'agent' ? 'Copied AI setup' : 'Copy AI setup'}</button>
      <button className="primary" type="button" disabled={busy} onClick={() => void rotate()}>{busy ? 'Working…' : status?.enabled ? 'Rotate token' : 'Create token'}</button>
      {status?.enabled && <button className="danger" type="button" disabled={busy} onClick={() => void revoke()}>Revoke token</button>}
    </div>

    {issuedToken && <div className="notice warn" role="status">
      <strong>Copy this token now — it will not be shown again.</strong>
      <p>The stored record contains only a SHA-256 digest. Rotating or revoking immediately invalidates this value.</p>
      <div className="remote-token-row">
        <input aria-label="Remote management token" readOnly value={issuedToken} onFocus={event => event.currentTarget.select()} />
        <button type="button" onClick={() => void copy('token', issuedToken)}>{copied === 'token' ? 'Copied' : 'Copy token'}</button>
      </div>
    </div>}

    <div className="notice">
      <strong>AI/API usage</strong>
      <p>Send <code>Authorization: Bearer &lt;TOKEN&gt;</code> to the remote API. Start with <code>GET {status?.capabilities ?? '/api/remote/v1/capabilities'}</code>. The discovery response describes configuration, lifecycle, source import/apply, devices, policies, routing, providers, diagnostics, Doctor, Tailscale and operation tracking endpoints.</p>
      <p>Browser cookies are not accepted on the remote API prefix. QNAP-blocked macOS host-network operations remain blocked.</p>
    </div>
  </section>
}
