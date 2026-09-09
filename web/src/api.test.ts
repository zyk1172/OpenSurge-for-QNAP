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
