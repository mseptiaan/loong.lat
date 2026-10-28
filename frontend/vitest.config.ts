import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    include: ['packages/**/*.test.ts', 'dashboard/src/**/*.test.ts', 'backoffice/src/**/*.test.ts', 'mobile/src/**/*.test.ts', 'landing/src/**/*.test.ts'],
    environment: 'node',
  },
})
