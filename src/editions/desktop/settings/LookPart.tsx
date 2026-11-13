// SPDX-License-Identifier: AGPL-3.0-only
import { SiteThemeChoice } from '../../../ui/SiteThemeChoice';
import type { SettingsPartProps } from './part-props';
import styles from './Settings.module.css';

/**
 * «Вид» (specs/2026-11-13-desk-settings.md ST3.1; board 44·13, `Pattern/Desktop/Settings Look`): «Тема: Светлая · Тёмная ·
 * Как в системе» — the same choice of the same owner as the site's; until the author chooses, the app is «Как в системе».
 */
export function LookPart(_: SettingsPartProps) {
  return (
    <>
      <div className={styles.themeRow}>
        <span aria-hidden="true" className={styles.text}>Тема</span>
        <SiteThemeChoice />
      </div>
      <p className={styles.note}>«Как в системе» — как на этом компьютере: приложение станет тёмным вместе с ним.</p>
    </>
  );
}
