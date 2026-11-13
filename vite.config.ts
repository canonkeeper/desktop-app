import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';

/**
 * The edition the build makes (specs/2026-11-07-desktop-app.md DA16.4): the site by default, the desktop app with
 * CK_EDITION=desktop. The app reads its edition only through `@edition`.
 */
export function editionAlias(edition = process.env.CK_EDITION ?? 'site') {
  if (edition !== 'site' && edition !== 'desktop') throw new Error(`CK_EDITION must be site or desktop, not ${edition}`);
  return { '@edition': fileURLToPath(new URL(`./src/editions/${edition}`, import.meta.url)) };
}

type ThemeChoice = 'light' | 'dark' | 'system';

/**
 * The theme an edition paints until the author chooses one (specs/2026-11-13-desk-settings.md ST3.1): its
 * `src/editions/<edition>/theme.json` names it — the app «Как в системе»; an edition without the file is light, as the
 * site has always been.
 */
export function editionThemeDefault(edition = process.env.CK_EDITION ?? 'site'): ThemeChoice | null {
  const file = path.join(path.dirname(fileURLToPath(import.meta.url)), 'src', 'editions', edition, 'theme.json');
  if (!existsSync(file)) return null;
  const choice: unknown = (JSON.parse(readFileSync(file, 'utf8')) as { default?: unknown }).default;
  if (choice !== 'light' && choice !== 'dark' && choice !== 'system') throw new Error(`${file}: default must be light, dark or system`);
  return choice;
}

/** The page of the build with the theme of its edition on <html>, where public/site-theme.js reads it before the first paint. */
export function editionThemeHtml(html: string, choice: ThemeChoice | null) {
  if (!choice) return html;
  if (!html.includes('<html lang="ru">')) throw new Error('index.html must open with <html lang="ru">');
  return html.replace('<html lang="ru">', `<html lang="ru" data-theme-default="${choice}">`);
}

const editionTheme: Plugin = {
  name: 'canonkeeper-edition-theme',
  transformIndexHtml: (html) => editionThemeHtml(html, editionThemeDefault()),
};

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
  plugins: [react(), testHealthPlugin, editionTheme],
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
