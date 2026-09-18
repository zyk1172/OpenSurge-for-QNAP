export type ShellIconName = 'dashboard' | 'network' | 'cloudflare' | 'sources' | 'devices' | 'policies' | 'management' | 'connectivity' | 'diagnostics' | 'traffic' | 'tutorial' | 'menu' | 'collapse' | 'sun' | 'moon' | 'search' | 'github'

export function ShellIcon({ name }: { name: ShellIconName }) {
  const common = { width: 20, height: 20, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 1.8, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, 'aria-hidden': true }
  if (name === 'dashboard') return <svg {...common}><rect x="3" y="3" width="7" height="7" rx="2"/><rect x="14" y="3" width="7" height="5" rx="2"/><rect x="14" y="12" width="7" height="9" rx="2"/><rect x="3" y="14" width="7" height="7" rx="2"/></svg>
  if (name === 'network') return <svg {...common}><circle cx="12" cy="5" r="2.2"/><circle cx="5" cy="18" r="2.2"/><circle cx="19" cy="18" r="2.2"/><path d="M12 7.2v4M6.8 16.5 10.4 13M17.2 16.5 13.6 13"/><circle cx="12" cy="12" r="1.5"/></svg>
  if (name === 'cloudflare') return <svg {...common}><path d="M7.2 18h10.6a3.2 3.2 0 0 0 .4-6.4A5.8 5.8 0 0 0 7.3 9.3 4.4 4.4 0 0 0 7.2 18Z"/><path d="M4 18h2.2M5.3 14.7A3.3 3.3 0 0 0 4 21h8"/></svg>
  if (name === 'sources') return <svg {...common}><path d="M4 7.5A3.5 3.5 0 0 1 7.5 4h9A3.5 3.5 0 0 1 20 7.5v9a3.5 3.5 0 0 1-3.5 3.5h-9A3.5 3.5 0 0 1 4 16.5z"/><path d="M8 9h8M8 12h8M8 15h5"/></svg>
  if (name === 'devices') return <svg {...common}><rect x="4" y="3" width="16" height="12" rx="2.5"/><path d="M9 20h6M12 15v5"/></svg>
  if (name === 'policies') return <svg {...common}><path d="M4 7h10M18 7h2M4 17h2M10 17h10"/><circle cx="16" cy="7" r="2"/><circle cx="8" cy="17" r="2"/></svg>
  if (name === 'management') return <svg {...common}><path d="M12 3.2 19 6v5.1c0 4.5-2.8 7.8-7 9.7-4.2-1.9-7-5.2-7-9.7V6z"/><path d="M9.5 11.5 11 13l3.5-3.5"/></svg>
  if (name === 'connectivity') return <svg {...common}><path d="M5 12a7 7 0 0 1 14 0"/><path d="M8 12a4 4 0 0 1 8 0"/><circle cx="12" cy="12" r="1.2"/><path d="M12 13.5V20"/></svg>
  if (name === 'diagnostics') return <svg {...common}><path d="M4 12h3l2-6 4 12 2-6h5"/><path d="M5 4h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z"/></svg>
  if (name === 'traffic') return <svg {...common}><path d="M5 19V9M12 19V5M19 19v-7"/><path d="m3 11 5-5 4 3 6-6 3 3"/></svg>
  if (name === 'tutorial') return <svg {...common}><path d="M4 5.5A2.5 2.5 0 0 1 6.5 3H11a3 3 0 0 1 3 3v15a3 3 0 0 0-3-3H6.5A2.5 2.5 0 0 0 4 20.5z"/><path d="M20 5.5A2.5 2.5 0 0 0 17.5 3H14v15h3.5a2.5 2.5 0 0 1 2.5 2.5z"/></svg>
  if (name === 'menu') return <svg {...common}><path d="M4 7h16M4 12h16M4 17h16"/></svg>
  if (name === 'collapse') return <svg {...common}><path d="M15 5 8 12l7 7"/></svg>
  if (name === 'search') return <svg {...common}><circle cx="11" cy="11" r="6"/><path d="m16 16 4 4"/></svg>
  if (name === 'github') return <svg {...common} viewBox="0 0 24 24"><path d="M12 2.8a9.2 9.2 0 0 0-2.9 17.9c.46.08.63-.2.63-.45v-1.78c-2.57.56-3.11-1.09-3.11-1.09-.42-1.07-1.03-1.36-1.03-1.36-.84-.58.06-.57.06-.57.93.07 1.42.96 1.42.96.83 1.42 2.17 1.01 2.7.77.08-.6.32-1.01.59-1.24-2.05-.23-4.21-1.03-4.21-4.57 0-1.01.36-1.84.95-2.49-.1-.23-.41-1.18.09-2.45 0 0 .78-.25 2.53.95A8.8 8.8 0 0 1 12 7.08a8.8 8.8 0 0 1 2.31.31c1.76-1.2 2.53-.95 2.53-.95.5 1.27.19 2.22.09 2.45.59.65.95 1.48.95 2.49 0 3.55-2.16 4.33-4.22 4.56.33.29.63.85.63 1.72v2.59c0 .25.17.54.64.45A9.2 9.2 0 0 0 12 2.8Z"/></svg>
  if (name === 'sun') return <svg {...common}><circle cx="12" cy="12" r="3.5"/><path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M18.7 5.3l-1.4 1.4M6.7 17.3l-1.4 1.4"/></svg>
  return <svg {...common}><path d="M20 15.2A8 8 0 0 1 8.8 4 8.1 8.1 0 1 0 20 15.2Z"/></svg>
}
