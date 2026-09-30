import { afterEach, describe, expect, it, vi } from 'vitest'

afterEach(() => {
  vi.unstubAllEnvs()
  vi.unstubAllGlobals()
  vi.resetModules()
})

describe('portable NAS product identity', () => {
  it.each(['synology', 'fnos', 'generic'])('loads %s identity from one shared NAS build', async platform => {
    vi.stubEnv('VITE_OPENSURGE_TARGET', 'nas')
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({
      platform, name: `OpenSurge for ${platform}`, network_driver: 'macvlan', host_takeover: false, experimental: true,
    }) })))
    const { isNASBuild, product, loadProduct } = await import('./product')
    await loadProduct()
    expect(isNASBuild).toBe(true)
    expect(product.platform).toBe(platform)
    expect(product.host_takeover).toBe(false)
  })

  it('keeps host takeover disabled when portable metadata is unavailable', async () => {
    vi.stubEnv('VITE_OPENSURGE_TARGET', 'nas')
    vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('offline') }))
    const { product, loadProduct } = await import('./product')
    await loadProduct()
    expect(product.host_takeover).toBe(false)
    expect(product.experimental).toBe(true)
  })

  it('does not make NAS metadata requests in the legacy Mac target', async () => {
    vi.stubEnv('VITE_OPENSURGE_TARGET', 'mac')
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    const { isNASBuild, loadProduct } = await import('./product')
    await loadProduct()
    expect(isNASBuild).toBe(false)
    expect(fetch).not.toHaveBeenCalled()
  })
})
