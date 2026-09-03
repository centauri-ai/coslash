import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      '/api': {
        target: `http://127.0.0.1:${process.env.COSLASH_API_PORT ?? '8787'}`,
        changeOrigin: true,
        configure: (proxy) => {
          let warnedAboutMissingToken = false;
          proxy.on('proxyReq', (proxyRequest) => {
            proxyRequest.removeHeader('origin');
            const home = process.env.COSLASH_HOME ?? path.join(os.homedir(), '.coslash');
            try {
              const token = fs.readFileSync(path.join(home, 'token'), 'utf8').trim();
              if (token !== '') proxyRequest.setHeader('x-coslash-token', token);
              warnedAboutMissingToken = false;
            } catch {
              if (!warnedAboutMissingToken) {
                console.warn('coSlash API token is unavailable; start the Go server before using Vite.');
                warnedAboutMissingToken = true;
              }
            }
          });
        },
      },
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
});
