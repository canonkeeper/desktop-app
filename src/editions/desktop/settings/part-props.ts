// SPDX-License-Identifier: AGPL-3.0-only
import type { CanonKeeperApiClient } from '../../../shared/api';

/** What a part of «Настройки» gets from the page (specs/2026-11-13-desk-settings.md ST1.3). */
export interface SettingsPartProps {
  api: CanonKeeperApiClient;
  /** The choice of the open part in the address, `kind`: the kind of the rows of «Журнал» (specs/2026-11-14-desk-journal.md JR3.1). */
  kind?: string | null;
  /** Writes the choice of the part into the address; null takes it away. */
  choose?: (kind: string | null) => void;
}

/**
 * The part «Аккаунт» in the address (`/settings?part=account`, specs/2026-11-15-desk-account-link-web.md AW2.1): here, without
 * the code of the page.
 */
export const accountPartKey = 'account';

/**
 * The part «Помощник» in the address (`/settings?part=helper`, specs/2026-11-13-desk-helper-web.md HW1.1): here, without the
 * code of the page, so the button of the line of the reading opens it.
 */
export const helperPartKey = 'helper';

/**
 * The part «Обновления» in the address (`/settings?part=updates`, specs/2026-11-15-desk-updates-web.md UW2.1): here, without
 * the code of the page, so «Открыть обновления» of the strip opens it.
 */
export const updatesPartKey = 'updates';

/** The part «Журнал» in the address (`/settings?part=log`, specs/2026-11-14-desk-journal.md JR3.1). */
export const logPartKey = 'log';
