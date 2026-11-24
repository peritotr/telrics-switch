// SPDX-License-Identifier: AGPL-3.0-only
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The panel talks to the management API on the same origin; in development
// Vite forwards API paths to a locally running `make api-run`.
const api = process.env.API_URL ?? 'http://127.0.0.1:58080'

export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 58081,
    strictPort: true,
    proxy: { '/v1': api, '/healthz': api },
  },
})
