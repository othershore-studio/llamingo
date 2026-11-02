import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { rmSync } from 'node:fs'

// The SPA talks to the Go core's HTTP/SSE adapter. In dev, proxy /api to a
// locally running adapter; in the Wails build the adapter is served from the
// same origin, so the same relative paths work.
export default defineConfig({
  // Relative asset paths so the built SPA works when served from the Wails
  // asset server's root as well as from the dev server.
  base: './',
  plugins: [
    svelte(),
    {
      // Clear stale hashed assets before each build. emptyOutDir is false (below)
      // to preserve dist/.gitignore — but without this, vite's content-hashed files
      // accumulate across builds and bloat the //go:embed'd bundle (desktop + mobile).
      name: 'clean-dist-assets',
      apply: 'build',
      buildStart() {
        rmSync(new URL('./dist/assets', import.meta.url), { recursive: true, force: true })
      },
    },
  ],
  build: {
    // Don't wipe web/dist on build: it holds a tracked .gitignore that keeps the
    // directory present for the Wails //go:embed on a fresh checkout. The plugin
    // above clears the stale hashed assets that emptyOutDir:false would otherwise keep.
    emptyOutDir: false,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
