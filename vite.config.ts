import { fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

/**
 * The edition the build makes (specs/2026-11-07-desktop-app.md DA16.4): the site by default, the desktop app with
 * CK_EDITION=desktop. The app reads its edition only through `@edition`.
 */
export function editionAlias(edition = process.env.CK_EDITION ?? 'site') {
  if (edition !== 'site' && edition !== 'desktop') throw new Error(`CK_EDITION must be site or desktop, not ${edition}`);
  return { '@edition': fileURLToPath(new URL(`./src/editions/${edition}`, import.meta.url)) };
}

const apiTarget =
  process.env.BACKEND_API_TARGET ?? process.env.MOCK_API_TARGET ?? 'http://127.0.0.1:8787';

const testHealthPlugin = {
  name: 'canonkeeper-test-health',
  configurePreviewServer(server: { middlewares: { use: (handler: (request: { url?: string }, response: { statusCode: number; setHeader: (name: string, value: string) => void; end: (body: string) => void }, next: () => void) => void) => void } }) {
    server.middlewares.use((request, response, next) => {
      if (request.url === '/healthz') {
        response.statusCode = 200;
        response.setHeader('content-type', 'text/plain; charset=utf-8');
        response.end('ok\n');
        return;
      }
      if (request.url === '/readyz') {
        response.statusCode = 404;
        response.setHeader('content-type', 'text/plain; charset=utf-8');
        response.end('not found\n');
        return;
      }
      next();
    });
  },
};

export const testPreviewSettings = {
  host: '127.0.0.1',
  port: 3334,
  strictPort: true,
  proxy: {
    '/api': {
      target: 'http://127.0.0.1:8789',
      changeOrigin: false,
      ws: true,
    },
  },
  plugins: [testHealthPlugin],
} as const;

export default defineConfig({
  plugins: [react(), testHealthPlugin],
  resolve: { alias: editionAlias() },
  server: {
    host: '127.0.0.1',
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: false,
      },
    },
  },
  preview: {
    host: testPreviewSettings.host,
    port: testPreviewSettings.port,
    strictPort: testPreviewSettings.strictPort,
    proxy: testPreviewSettings.proxy,
  },
});
