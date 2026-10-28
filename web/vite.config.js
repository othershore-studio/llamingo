import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// The SPA talks to the Go core's HTTP/SSE adapter. In dev, proxy /api to a
// locally running adapter; in the Wails build the adapter is served from the
// same origin, so the same relative paths work.
export default defineConfig({
  // Relative asset paths so the built SPA works when served from the Wails
  // asset server's root as well as from the dev server.
  base: './',
  plugins: [svelte()],
  build: {
    // Don't wipe web/dist on build: it holds a tracked .gitignore that keeps
    // the directory present for the Wails //go:embed on a fresh checkout.
    emptyOutDir: false,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
