/// <reference types="vitest" />
import { defineConfig, loadEnv, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'

// Plausible is opt-in: self-hosted builds ship no third-party script.
function plausible(src: string, domain: string): Plugin {
  return {
    name: 'plausible',
    transformIndexHtml() {
      if (!src || !domain) return []
      return [{ tag: 'script', attrs: { defer: true, 'data-domain': domain, src }, injectTo: 'head' }]
    },
  }
}

// A self-hosted instance must not be indexed, whatever its robots.txt says.
function noindex(selfHosted: boolean): Plugin {
  return {
    name: 'noindex',
    transformIndexHtml() {
      if (!selfHosted) return []
      return [{ tag: 'meta', attrs: { name: 'robots', content: 'noindex, nofollow' }, injectTo: 'head' }]
    },
  }
}

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), 'VITE_')
  return {
    plugins: [
      react(),
      plausible(env.VITE_PLAUSIBLE_SRC, env.VITE_PLAUSIBLE_DOMAIN),
      noindex(env.VITE_SELF_HOSTED === 'true'),
    ],
    // Unit tests cover the pure logic the e2e suite can only reach through a
    // rendered page: status derivation, error parsing, slugs. e2e/ is Playwright's
    // and must stay out, or vitest tries to run it.
    test: {
      // .test.tsx files render hooks and components, so they need a DOM; the
      // plain .test.ts ones are pure logic and would pay for jsdom for nothing.
      include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
      environment: 'node',
      environmentMatchGlobs: [['src/**/*.test.tsx', 'jsdom']],
    },
    server: {
      port: 3000,
      // Views are lazy routes: without this the dev server compiles each one on
      // its first visit, which is slow enough on a cold CI runner to fail e2e waits.
      warmup: { clientFiles: ['./src/views/**/*.tsx'] },
      proxy: {
        '/api': {
          target: 'http://localhost:8080',
          changeOrigin: true,
        },
      },
    },
  }
})
