// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation } from '@tanstack/react-query';
import { Trash2 } from 'lucide-react';
import { useId, useRef, useState } from 'react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import type { CanonKeeperApiClient } from '../../../shared/api';
import { Button } from '../../../ui/controls/Button';
import { Field } from '../../../ui/controls/Field';
import { Confirm } from '../../../ui/overlays/Confirm';
import { useDesktopAccount } from '../account/account-api';
import { accountWords } from '../account/account-words';
import { useProgramAccess } from './helper/programs-api';
import { programsWords } from './helper/programs-words';
import styles from './Settings.module.css';

/** The word the author writes; the server compares it again (backend/internal/domain/desktop/erase.go, EraseWord). */
const eraseWord = 'стереть';

/** The word is written whatever its case and the spaces at its ends (ER1.1). */
export function eraseWordWritten(text: string) {
  return text.trim().toLocaleLowerCase('ru') === eraseWord;
}

/** The words of the board 44·12, the column of a build without «Рисунки» — this edition has none —, and of the spec
 * (specs/2026-11-13-desk-erase.md ER1.1; specs/2026-11-13-desk-boards4.md). */
const words = {
  open: 'Стереть все данные…',
  title: 'Стереть все данные CanonKeeper на этом компьютере?',
  line: 'Удалятся все саги, книги, главы в работе, чаты и настройки. Вернуть их будет нельзя. Файлы саг, которые вы сохранили, и папки Obsidian останутся.',
  field: 'Напишите «стереть»',
  hint: '«Стереть всё» станет доступна, когда в поле будет это слово.',
  action: 'Стереть всё',
  busy: 'Стираю…',
  failed: 'Стирание не началось. Попробуйте ещё раз.',
  silent: 'Стирание не началось: CanonKeeper не ответил. Попробуйте ещё раз.',
} as const;

/**
 * «Стереть все данные…», the last row of «Данные» (specs/2026-11-13-desk-erase.md ER1.1; board 44·11, 44·12): the last
 * question with the word to write; with it the backend asks the shell to erase, and the window says «Стираю…» with no way
 * out until the app closes itself — the shell stops the backend, closes the window and removes what the app wrote. A linked
 * app is unlinked first: the question says it, and what to do when the server does not answer
 * (specs/2026-11-15-desk-account-link-web.md AW1.5; board 44·12).
 */
export function EraseData({ api }: { api: Pick<CanonKeeperApiClient, 'eraseDesktopData' | 'getAccount' | 'getProgramAccess'> }) {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState('');
  const field = useRef<HTMLInputElement>(null);
  const id = useId();
  const erase = useMutation({ mutationFn: (confirm: string) => api.eraseDesktopData({ confirm }) });
  const linked = useDesktopAccount(api, open).data?.state === 'linked';
  // The settings of the programs of the author the app never writes stay: the window says how to take them away
  // (specs/2026-11-15-desk-mcp.md MC3.4), once the reading was ever on.
  const programs = useProgramAccess(api, open).data;
  // 202 is the shell that got the request: what follows is the app closing, never a way back to the page.
  const busy = erase.isPending || erase.isSuccess;
  const written = eraseWordWritten(text);
  const close = () => {
    setOpen(false);
    setText('');
    erase.reset();
  };
  return (
    <div className={styles.section}>
      <div>
        <Button onClick={() => setOpen(true)} variant="danger">
          <Trash2 aria-hidden="true" size={16} />{words.open}
        </Button>
      </div>
      {open ? (
        <Confirm
          action={words.action}
          actionIcon={<Trash2 aria-hidden="true" size={16} />}
          actionReason={written ? null : words.hint}
          also={linked ? accountWords.erase : undefined}
          busy={busy}
          busyAction={words.busy}
          error={erase.isError ? publicApiErrorMessage(erase.error, words.failed, { network: words.silent, server: words.failed }) : undefined}
          field={(
            <Field
              autoComplete="off"
              disabled={busy}
              hint={written ? undefined : words.hint}
              id={id}
              label={words.field}
              onChange={(event) => setText(event.target.value)}
              ref={field}
              spellCheck={false}
              value={text}
            />
          )}
          focusRef={field}
          irreversible
          onCancel={close}
          onConfirm={() => {
            if (written && !busy) erase.mutate(text.trim());
          }}
          title={words.title}
          width={480}
        >
          {words.line}
          {programs?.ever ? (
            <>
              <span className={styles.eraseLine}>{programsWords.erase.commands}</span>
              <code className={styles.eraseCommand}>{programsWords.erase.claudeRemove}</code>
              <code className={styles.eraseCommand}>{programsWords.erase.codexRemove}</code>
              <span className={styles.eraseLine}>{programs.system === 'windows' ? programsWords.erase.windows : programsWords.erase.linux}</span>
            </>
          ) : null}
        </Confirm>
      ) : null}
    </div>
  );
}
