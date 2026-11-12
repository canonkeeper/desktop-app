import { cleanup, configure } from '@testing-library/react';
import { afterAll, afterEach, beforeAll } from 'vitest';
import { mswApiBaseUrl } from './msw/handlers';
import { mswServer } from './msw/server';

// A page that reads several queries stands in more than the default second when the machine also runs the e2e shards and
// the backend tests: a found element still answers at once, only a missing one waits longer.
configure({ asyncUtilTimeout: 4000 });

beforeAll(() => {
  installBrowserBlobPolyfills();
  window.confirm = () => false;
  mswServer.listen({
    onUnhandledRequest(request) {
      if (request.url.startsWith(mswApiBaseUrl)) {
        throw new Error(`Unhandled MSW API request: ${request.method} ${request.url}`);
      }
    },
  });
  installBrowserGeometryPolyfills();
});

afterEach(() => {
  mswServer.resetHandlers();
  cleanup();
  // A test leaves nothing in the storage of the browser for the next one: drafts of forms live there.
  window.localStorage.clear();
  window.sessionStorage.clear();
});

afterAll(() => {
  mswServer.close();
});

function installBrowserBlobPolyfills() {
  Blob.prototype.text ??= function text() {
    return new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onerror = () => reject(reader.error ?? new DOMException('Failed to read Blob as text.'));
      reader.onload = () => resolve(String(reader.result ?? ''));
      reader.readAsText(this);
    });
  };
}

function installBrowserGeometryPolyfills() {
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  const rect = {
    bottom: 0,
    height: 0,
    left: 0,
    right: 0,
    toJSON: () => ({}),
    top: 0,
    width: 0,
    x: 0,
    y: 0,
  } as DOMRect;

  document.elementFromPoint = document.elementFromPoint ?? (() => document.body);
  HTMLElement.prototype.getBoundingClientRect = HTMLElement.prototype.getBoundingClientRect ?? (() => rect);
  HTMLElement.prototype.getClientRects = HTMLElement.prototype.getClientRects ?? (() => [rect] as unknown as DOMRectList);
  Range.prototype.getBoundingClientRect = Range.prototype.getBoundingClientRect ?? (() => rect);
  Range.prototype.getClientRects = Range.prototype.getClientRects ?? (() => [rect] as unknown as DOMRectList);
  if (typeof Text !== 'undefined') {
    (Text.prototype as unknown as { getBoundingClientRect?: () => DOMRect }).getBoundingClientRect ??= () => rect;
    (Text.prototype as unknown as { getClientRects?: () => DOMRectList }).getClientRects ??= () => [rect] as unknown as DOMRectList;
  }
}
