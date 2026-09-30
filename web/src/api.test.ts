// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, authenticationRequiredEvent, createOperationID, request } from './api'

describe('Control API requests', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('announces authentication expiry for any API request that receives 401', async () => {
    const listener = vi.fn()
    window.addEventListener(authenticationRequiredEvent, listener, { once: true })
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false,
      status: 401,
      statusText: 'Unauthorized',
      json: async () => ({ error: { code: 'authentication_required', message: 'expired' } }),
    })))

    await expect(request('/api/v1/overview')).rejects.toMatchObject({
      status: 401,
      code: 'authentication_required',
    })
    expect(listener).toHaveBeenCalledOnce()
  })

  it('uses the policy workspace endpoint for read, selection, and delay tests', async () => {
    const snapshot = { schema_version: 1, mode: 'prepared', revision: 'r', groups: [], health: { schema_version: 1, test_url: '', proxies: [] } }
    const fetcher = vi.fn(async () => ({ ok: true, json: async () => snapshot }))
    vi.stubGlobal('fetch', fetcher)

    for (const action of [{ action: 'read' }, { action: 'select', group: 'AI / Home', policy: 'Exit Node' }, { action: 'test', names: ['Exit Node'] }] as const) {
      expect(await api.policyWorkspace(action.action === 'test' ? { ...action, names: [...action.names] } : action)).toEqual(snapshot)
      expect(fetcher).toHaveBeenLastCalledWith('/api/v1/policy-workspace', expect.objectContaining({ method: 'POST', credentials: 'same-origin', body: JSON.stringify(action) }))
    }
  })

  it('encodes the complete policy group name for a scoped connection refresh', async () => {
    const fetcher = vi.fn(async () => ({ ok: true, json: async () => ({ scope: 'policy_group', closed_connections: 0 }) }))
    vi.stubGlobal('fetch', fetcher)
    await api.refreshPolicyConnections('共享/香港 策略')
    expect(fetcher).toHaveBeenCalledExactlyOnceWith(`/api/v1/policies/${encodeURIComponent('共享/香港 策略')}/connections/refresh`, expect.objectContaining({ method: 'POST', credentials: 'same-origin' }))
  })

  it('delivers split UTF-8 policy results before the final workspace', async () => {
    let stream!: ReadableStreamDefaultController<Uint8Array>
    const body = new ReadableStream<Uint8Array>({ start(controller) { stream = controller } })
    const fetcher = vi.fn(async () => new Response(body, { headers: { 'Content-Type': 'text/event-stream' } }))
    vi.stubGlobal('fetch', fetcher)
    const onResult = vi.fn()
    const controller = new AbortController()
    let completed = false
    const requestPromise = api.policyWorkspace({ action: 'test', names: ['东京'] }, { onResult, signal: controller.signal })
    void requestPromise.then(() => { completed = true })
    const result = { name: '东京', status: 'reachable' as const, delay_ms: 42, tested_at: '2026-09-29T00:00:00Z', test_url: 'https://example.invalid/' }
    const bytes = new TextEncoder().encode(`: keepalive\r\n\r\ndata: ${JSON.stringify({ type: 'result', result })}\r\n\r\n`)
    for (const byte of bytes) stream.enqueue(new Uint8Array([byte]))
    await vi.waitFor(() => expect(onResult).toHaveBeenCalledExactlyOnceWith(result))
    expect(completed).toBe(false)
    const workspace = { schema_version: 1, mode: 'running' as const, revision: 'r', groups: [], health: { schema_version: 1, test_url: '', proxies: [] } }
    stream.enqueue(new TextEncoder().encode(`data: ${JSON.stringify({ type: 'complete', workspace })}\n\n`))
    expect(await requestPromise).toEqual(workspace)
    expect(fetcher).toHaveBeenCalledExactlyOnceWith('/api/v1/policy-workspace', expect.objectContaining({
      headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
      signal: controller.signal,
    }))
  })

  it.each(['error', 'disconnect'])('keeps delivered policy results and rejects an incomplete %s stream', async failure => {
    const result = { name: 'A', status: 'reachable' as const, delay_ms: 42, tested_at: '', test_url: '' }
    const frames = `data: ${JSON.stringify({ type: 'result', result })}\n\n${failure === 'error' ? 'data: {"type":"error","error":"controller unavailable"}\n\n' : ''}`
    const fetcher = vi.fn(async () => new Response(frames, { headers: { 'Content-Type': 'text/event-stream' } }))
    vi.stubGlobal('fetch', fetcher)
    const onResult = vi.fn()
    await expect(api.policyWorkspace({ action: 'test', names: ['A'] }, { onResult, signal: new AbortController().signal }))
      .rejects.toThrow(failure === 'error' ? 'controller unavailable' : '节点检测连接已中断')
    expect(onResult).toHaveBeenCalledExactlyOnceWith(result)
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it('creates an operation id when randomUUID is unavailable on LAN HTTP', () => {
    vi.stubGlobal('crypto', {
      getRandomValues: (bytes: Uint8Array) => {
        bytes.fill(0xab)
        return bytes
      },
    })

    expect(createOperationID()).toBe(`op-${'ab'.repeat(16)}`)
  })

  it('still sends tracked gateway mutations when randomUUID is unavailable', async () => {
    vi.stubGlobal('crypto', {
      getRandomValues: (bytes: Uint8Array) => {
        bytes.fill(0x2a)
        return bytes
      },
    })
    const operationID = `op-${'2a'.repeat(16)}`
    const operation = {
      id: operationID,
      kind: 'start',
      state: 'succeeded',
      phase: 'completed',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    }
    const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
      if (path === '/api/v1/gateway/start') {
        return { ok: true, json: async () => operation }
      }
      if (path === `/api/v1/operations/${operationID}`) {
        return { ok: true, json: async () => operation }
      }
      throw new Error(`unexpected request: ${path} ${init?.method ?? 'GET'}`)
    })
    vi.stubGlobal('fetch', fetcher)

    await expect(api.gateway('start')).resolves.toEqual(operation)
    expect(fetcher).toHaveBeenCalledWith('/api/v1/gateway/start', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      headers: expect.objectContaining({ 'Idempotency-Key': operationID }),
    }))
  })
})
