// SPDX-License-Identifier: AGPL-3.0-only
import { lazy } from 'react';
import { z } from 'zod';
import type { EditionPage, EditionRoute } from '../../../app/edition';
import { searchValue } from '../../../routing/routes';

const importSettingsPage = () => import('./SettingsPage');
const SettingsPage = lazy(() => importSettingsPage().then((module) => ({ default: module.SettingsPage })));

/**
 * «Настройки» of the app (specs/2026-11-13-desk-settings.md ST1.2): the open part in the address, and the kind of the rows of
 * «Журнал» (specs/2026-11-14-desk-journal.md JR3.1) — a kind there is not is dropped.
 */
export const settingsRoute: EditionRoute = {
  path: '/settings',
  validateSearch: z.object({
    part: searchValue,
    kind: z.enum(['question', 'reading', 'materials', 'check', 'program']).optional().catch(undefined),
  }).catch({}).parse,
};

export const settingsPage: EditionPage = {
  render: ({ api, navigate, route }) => <SettingsPage api={api} navigate={navigate} route={route} />,
  preload: importSettingsPage,
};
