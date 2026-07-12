import { defineConfig } from 'vitest/config'

// The modules under test are pure (no DOM, no React), so the node environment is
// correct - do not pull in jsdom. Globals stay OFF so every test imports from
// 'vitest' explicitly and `astro check` needs no extra global type config.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
})
