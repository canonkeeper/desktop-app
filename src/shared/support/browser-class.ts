import type { Schema } from '../api';

/** The class of a browser by its user agent: what an error report and a message of feedback name, never the agent itself. */
export function browserClass(userAgent: string): Schema<'ClientErrorReport'>['browser'] {
  if (/YaBrowser\//.test(userAgent)) return 'yandex';
  if (/Edg\//.test(userAgent)) return 'edge';
  if (/Firefox\//.test(userAgent)) return 'firefox';
  if (/Chrome\//.test(userAgent)) return 'chrome';
  if (/Safari\//.test(userAgent)) return 'safari';
  return 'other';
}
