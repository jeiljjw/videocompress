import { defineConfig } from 'vitest/config'
import type { Plugin } from 'vite'
import react from '@vitejs/plugin-react'

// Vite empties dist/ on every build, which would delete the committed
// frontend/dist/.gitkeep that `//go:embed all:frontend/dist` depends on.
// Re-emit the marker as part of the bundle so it always exists after a build.
const keepDistMarker: Plugin = {
  name: 'keep-dist-marker',
  generateBundle() {
    this.emitFile({ type: 'asset', fileName: '.gitkeep', source: '' })
  },
}

export default defineConfig({
  plugins: [react(), keepDistMarker],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    css: false,
  },
})
