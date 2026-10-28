import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// The SPA talks to the Go core's HTTP/SSE adapter. In dev, proxy /api to a
// locally running adapter; in the Wails build the adapter is served from the
// same origin, so the same relative paths work.
export default defineConfig({
  plugins: [svelte()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
