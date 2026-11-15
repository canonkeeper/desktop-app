// SPDX-License-Identifier: AGPL-3.0-only
import { ApiStatusError } from '../../../shared/api';
import type { AppUpdate } from './update-words';

/** An update of an AppImage of 1.2.0 that checked nothing yet, with what the test gives. */
export function updateAt(given: Partial<AppUpdate>): AppUpdate {
  return {
    version: '1.2.0', daily: true, kind: 'appimage', phase: 'idle', found: 'never', available: null, percent: null,
    refusal: null, package: null, checkedAt: null, ...given,
  };
}

/** The release of the boards: 1.3.0 with its notes. */
export const release = { version: '1.3.0', notes: '- Северный предел читается быстрее\n- Карта саги показывает дороги' };

/** A refusal of the backend with its status and code. */
export function refusal(status: number, code?: string) {
  return new ApiStatusError(status, { type: 'about:blank', title: 'Refused', status, code: code ?? '', requestId: 'req-update' }, new Response(null, { status }));
}
