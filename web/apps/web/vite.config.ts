import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// API и медиа проксируются в dev, чтобы браузер видел один origin:
// сессионная cookie SameSite=Lax иначе не поедет на другой порт.
// API и медиа проксируются и в dev, и в preview: браузер обязан видеть
// ОДИН origin, иначе сессионная cookie SameSite=Lax на другой порт не поедет,
// а нативный HLS-плеер в iOS не подставит к сегментам ничего своего.
const proxy = {
  '/api': { target: process.env.CORE_URL ?? 'http://localhost:8080', changeOrigin: true },
  '/stream': { target: process.env.GATEWAY_URL ?? 'http://localhost:8082', changeOrigin: true },
}

export default defineConfig({
  plugins: [react()],
  server: { host: '0.0.0.0', port: 5173, proxy },
  preview: { host: '0.0.0.0', port: 5173, proxy },
})
