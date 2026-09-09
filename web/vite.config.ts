import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '')
  const releaseTag = env.OPENSURGE_RELEASE_TAG?.trim() || 'v0.2.0-dev'
  // The fork is a QNAP/Linux product by default. Keeping an explicit target
  // boundary lets upstream macOS-oriented UI tests remain useful while the
  // remaining legacy controls are retired incrementally.
  const target = env.OPENSURGE_TARGET?.trim() || (mode === 'test' ? 'mac' : 'qnap')

  return {
    plugins: [react()],
    define: {
      'import.meta.env.VITE_OPENSURGE_RELEASE_TAG': JSON.stringify(releaseTag),
      'import.meta.env.VITE_OPENSURGE_TARGET': JSON.stringify(target),
    },
    build: {
      outDir: '../internal/webui/dist',
      emptyOutDir: true,
    },
    server: {
      port: 5173,
      proxy: {
        '/api': 'http://127.0.0.1:61767',
        '/bootstrap': 'http://127.0.0.1:61767',
      },
    },
  }
})
