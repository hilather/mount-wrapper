import { defineConfig } from 'vitest/config'

// Separate from vite.config.ts to avoid Vite 8 / Vitest plugin type clashes under tsc.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
})
