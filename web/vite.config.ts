import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// https://vite.dev/config/
export default defineConfig({
  plugins: [svelte()],
  // Relative base so assets work under embedded FileServer paths.
  base: '/',
  server: {
    port: 5173,
    // Dev: proxy API/SSE to the Go daemon (default web_port).
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8787',
        changeOrigin: true,
        // Keep SSE streams open (Vite/http-proxy defaults can be short).
        timeout: 0,
        proxyTimeout: 0,
      },
    },
  },
  build: {
    // Makefile copies this into internal/webui/dist for embed.FS.
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: true,
  },
})
