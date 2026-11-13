// SPDX-License-Identifier: AGPL-3.0-only
import { lazy } from 'react';
import { z } from 'zod';
import type { EditionPage, EditionRoute } from '../../../app/edition';
import { searchValue } from '../../../routing/routes';

const importSettingsPage = () => import('./SettingsPage');
const SettingsPage = lazy(() => importSettingsPage().then((module) => ({ default: module.SettingsPage })));

/** «Настройки» of the app (specs/2026-11-13-desk-settings.md ST1.2): the open part in the address. */
export const settingsRoute: EditionRoute = {
  path: '/settings',
  validateSearch: z.object({ part: searchValue }).catch({}).parse,
};

export const settingsPage: EditionPage = {
  render: ({ api, navigate, route }) => <SettingsPage api={api} navigate={navigate} route={route} />,
  preload: importSettingsPage,
};
