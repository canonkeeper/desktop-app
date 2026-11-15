// SPDX-License-Identifier: AGPL-3.0-only
import { Bug, Globe, Mail } from 'lucide-react';
import { useId } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import { desktopShell } from '../../../shared/desktop/shell';
import { ButtonLink } from '../../../ui/controls/Button';
import { LinkAction } from '../../../ui/controls/LinkAction';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { external } from '../external';
import { useDesktopState } from './desktop-state';
import type { SettingsPartProps } from './part-props';
import styles from './Settings.module.css';

/** The open core of CanonKeeper: the code of the app and its issues (ST4.1). */
const sourceUrl = 'https://github.com/canonkeeper/desktop-app';
const issuesUrl = `${sourceUrl}/issues`;
/** «Написать нам» of the site: its home with the form of the contacts open (route `/` with `contact=write`). */
const contactUrl = 'https://canonkeeper.ru/?contact=write';

/** The words of the list of the network (specs/2026-11-14-desk-journal.md JR4.3, Data). */
export const networkWords = {
  none: 'Сейчас никуда',
  when: 'Адреса появятся здесь, когда вы подключите помощника, свяжете приложение с аккаунтом или включите проверку обновлений.',
  proxy: (host: string) => `Запросы к помощнику и серверу идут через прокси из настроек окружения: ${host}`,
  /** The reading of the sagas by the programs of the author is on (specs/2026-11-15-desk-mcp.md MC3.5): no address of the app. */
  programs: 'Чтение саг вашим Claude или Codex включено — куда они отправляют прочитанное, решают они',
} as const;

/**
 * «О программе» (specs/2026-11-13-desk-settings.md ST4.1–ST4.3; board 44·16, `Pattern/Desktop/Settings About`): the version
 * the backend names, the open core and its code, where the app goes in the network by the list of the server and the proxy of
 * the environment (specs/2026-11-14-desk-journal.md JR4.3), «Сообщить о проблеме» and «Написать нам». No line of a work not
 * done yet.
 */
export function AboutPart({ api }: SettingsPartProps) {
  const state = useDesktopState(api);
  const toasts = useToasts();
  const shell = desktopShell();
  const network = useId();
  return (
    <>
      {state.isPending ? <SkeletonArea count={1} shape="lines" /> : state.data?.version ? <p className={styles.text}>{`Версия ${state.data.version}`}</p> : null}
      <p className={styles.openRow}>
        <span className={styles.text}>Ядро CanonKeeper открыто</span>
        <LinkAction {...external(sourceUrl, shell, toasts)} className={styles.source}>Исходный код</LinkAction>
      </p>
      <div aria-labelledby={network} className={styles.section} role="group">
        <p className={styles.label} id={network}>Куда приложение ходит в сеть</p>
        {state.isPending ? <SkeletonArea count={2} shape="lines" /> : state.isError ? (
          <ErrorBlock {...loadFailureWords(state.error)} onRetry={() => void state.refetch()} retrying={state.isRefetching} title="Не открылись данные приложения" />
        ) : (
          <>
            {state.data.network.length === 0 ? (
              <>
                <p className={styles.text}>{networkWords.none}</p>
                <p className={styles.note}>{networkWords.when}</p>
              </>
            ) : (
              <ul className={styles.rows}>
                {state.data.network.map(({ host, purpose }) => (
                  <li className={styles.networkRow} key={host}><Globe aria-hidden="true" className={styles.rowIcon} size={16} />{`${host} — ${purpose}`}</li>
                ))}
              </ul>
            )}
            {state.data.programsRead ? <p className={styles.note}>{networkWords.programs}</p> : null}
            {state.data.proxy ? <p className={styles.note}>{networkWords.proxy(state.data.proxy)}</p> : null}
          </>
        )}
      </div>
      <div className={styles.actions}>
        <ButtonLink {...external(issuesUrl, shell, toasts)} variant="outline"><Bug aria-hidden="true" size={16} />Сообщить о проблеме</ButtonLink>
        <ButtonLink {...external(contactUrl, shell, toasts)} variant="outline"><Mail aria-hidden="true" size={16} />Написать нам</ButtonLink>
      </div>
    </>
  );
}
