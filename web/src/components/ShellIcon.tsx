export type ShellIconName = 'dashboard' | 'network' | 'sources' | 'devices' | 'policies' | 'connectivity' | 'diagnostics' | 'traffic' | 'menu' | 'collapse' | 'sun' | 'moon' | 'search'

export function ShellIcon({ name }: { name: ShellIconName }) {
  const common = { width: 20, height: 20, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 1.8, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, 'aria-hidden': true }
  if (name === 'dashboard') return <svg {...common}><rect x="3" y="3" width="7" height="7" rx="2"/><rect x="14" y="3" width="7" height="5" rx="2"/><rect x="14" y="12" width="7" height="9" rx="2"/><rect x="3" y="14" width="7" height="7" rx="2"/></svg>
  if (name === 'network') return <svg {...common}><circle cx="12" cy="5" r="2.2"/><circle cx="5" cy="18" r="2.2"/><circle cx="19" cy="18" r="2.2"/><path d="M12 7.2v4M6.8 16.5 10.4 13M17.2 16.5 13.6 13"/><circle cx="12" cy="12" r="1.5"/></svg>
  if (name === 'sources') return <svg {...common}><path d="M4 7.5A3.5 3.5 0 0 1 7.5 4h9A3.5 3.5 0 0 1 20 7.5v9a3.5 3.5 0 0 1-3.5 3.5h-9A3.5 3.5 0 0 1 4 16.5z"/><path d="M8 9h8M8 12h8M8 15h5"/></svg>
  if (name === 'devices') return <svg {...common}><rect x="4" y="3" width="16" height="12" rx="2.5"/><path d="M9 20h6M12 15v5"/></svg>
  if (name === 'policies') return <svg {...common}><path d="M4 7h10M18 7h2M4 17h2M10 17h10"/><circle cx="16" cy="7" r="2"/><circle cx="8" cy="17" r="2"/></svg>
  if (name === 'connectivity') return <svg {...common}><path d="M5 12a7 7 0 0 1 14 0"/><path d="M8 12a4 4 0 0 1 8 0"/><circle cx="12" cy="12" r="1.2"/><path d="M12 13.5V20"/></svg>
  if (name === 'diagnostics') return <svg {...common}><path d="M4 12h3l2-6 4 12 2-6h5"/><path d="M5 4h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2Z"/></svg>
  if (name === 'traffic') return <svg {...common}><path d="M5 19V9M12 19V5M19 19v-7"/><path d="m3 11 5-5 4 3 6-6 3 3"/></svg>
  if (name === 'menu') return <svg {...common}><path d="M4 7h16M4 12h16M4 17h16"/></svg>
  if (name === 'collapse') return <svg {...common}><path d="M15 5 8 12l7 7"/></svg>
  if (name === 'search') return <svg {...common}><circle cx="11" cy="11" r="6"/><path d="m16 16 4 4"/></svg>
  if (name === 'sun') return <svg {...common}><circle cx="12" cy="12" r="3.5"/><path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M18.7 5.3l-1.4 1.4M6.7 17.3l-1.4 1.4"/></svg>
  return <svg {...common}><path d="M20 15.2A8 8 0 0 1 8.8 4 8.1 8.1 0 1 0 20 15.2Z"/></svg>
}
