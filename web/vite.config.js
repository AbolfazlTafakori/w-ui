import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],

  // The build lands directly in the Go module's embed directory, so
  // `npm run build && go build` produces one binary carrying both halves.
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 700,
  },

  // In development the Vue dev server owns the page and forwards API calls to
  // the Go process, so the frontend reloads without rebuilding the binary.
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:2096',
        changeOrigin: true,
      },
    },
  },
})
