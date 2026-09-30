export const isNASBuild = ['qnap', 'nas', 'synology', 'fnos', 'generic'].includes(import.meta.env.VITE_OPENSURGE_TARGET ?? 'mac')

const portableNAS = ['nas', 'synology', 'fnos', 'generic'].includes(import.meta.env.VITE_OPENSURGE_TARGET ?? '')
export const product = {
  platform: portableNAS ? 'generic' : 'qnap',
  name: portableNAS ? 'OpenSurge for NAS' : 'OpenSurge for QNAP',
  network_driver: portableNAS ? 'macvlan' : 'qnet',
  host_takeover: !portableNAS,
  experimental: portableNAS,
}

export async function loadProduct() {
  if (!isNASBuild) return
  try {
    const response = await fetch('/api/product', { cache: 'no-store', signal: AbortSignal.timeout(5000) })
    if (!response.ok) return
    const metadata = await response.json() as typeof product
    if (!['qnap', 'synology', 'fnos', 'generic'].includes(metadata.platform)) return
    Object.assign(product, metadata)
  } catch {
    // Keep a conservative build-time fallback if the Web boundary is offline.
  }
}
