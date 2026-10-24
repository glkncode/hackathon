import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  css: {
    modules: {
      // Keep cross-module selectors stable when a stylesheet changes during HMR.
      generateScopedName: '_[hash:base64:8]',
    },
  },
  // Reuse Tailwind's existing Lightning CSS dependency to merge repeated
  // CSS Modules imports and cascade layers in the production stylesheet.
  build: { cssMinify: 'lightningcss' },
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
