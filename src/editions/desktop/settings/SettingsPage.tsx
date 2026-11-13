// SPDX-License-Identifier: AGPL-3.0-only
import { useId } from 'react';
import type { EditionPageProps } from '../../../app/edition';
import { toHref } from '../../../shared/navigation/app-route';
import { PageHead } from '../../../ui/shell/PageHead';
import { settingsPartOf, settingsParts } from './parts';
import styles from './Settings.module.css';

/**
 * «Настройки» of the app (specs/2026-11-13-desk-settings.md ST1.2; board 44, `Pattern/Desktop/Settings`): the content of
 * the page in the one shell — its parts on the left, the open one on the right; the address holds the open part.
 */
export function SettingsPage({ api, navigate, route }: Pick<EditionPageProps, 'api' | 'navigate' | 'route'>) {
  const open = settingsPartOf(route.query.get('part'));
  const heading = useId();
  const { Part } = open;
  return (
    <div className={styles.page}>
      <PageHead surface="settings" />
      <div className={styles.body}>
        <nav aria-label="Части настроек" className={styles.nav}>
          <ul className={styles.navList}>
            {settingsParts.map(({ Icon, key, label }) => (
              <li key={key}>
                <a aria-current={key === open.key ? 'page' : undefined} className={styles.navItem} href={toHref('/settings', { part: key })}
                  onClick={(event) => {
                    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
                    event.preventDefault();
                    navigate('/settings', { part: key }, { replace: true });
                  }}>
                  <Icon aria-hidden="true" size={15} />
                  <span>{label}</span>
                </a>
              </li>
            ))}
          </ul>
        </nav>
        <section aria-labelledby={heading} className={styles.part}>
          <h2 className={styles.partTitle} id={heading}>{open.label}</h2>
          <Part api={api} />
        </section>
      </div>
    </div>
  );
}
