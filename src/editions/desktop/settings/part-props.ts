// SPDX-License-Identifier: AGPL-3.0-only
import type { CanonKeeperApiClient } from '../../../shared/api';

/** What a part of «Настройки» gets from the page (specs/2026-11-13-desk-settings.md ST1.3). */
export interface SettingsPartProps {
  api: CanonKeeperApiClient;
}
