import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    host: 'localhost',
    port: 5173,
    strictPort: true,
    proxy: {
      // Keep the browser's Origin: the Go allowlist must include the Vite origin.
      '/api': { target: 'http://localhost:8080', changeOrigin: false },
    },
  },
  preview: { host: 'localhost', port: 5173, strictPort: true },
});
