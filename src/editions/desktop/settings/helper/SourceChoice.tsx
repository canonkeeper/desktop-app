// SPDX-License-Identifier: AGPL-3.0-only
import { RotateCcw } from 'lucide-react';
import { useCallback, useRef, useState, type ReactNode } from 'react';
import { Button } from '../../../../ui/controls/Button';
import { Alert } from '../../../../ui/feedback/Alert';
import { useToasts } from '../../../../ui/feedback/useToastQueue';
import { Confirm } from '../../../../ui/overlays/Confirm';
import { useDesktopAccount } from '../../account/account-api';
import { LinkStart } from '../../account/LinkStart';
import type { SettingsPartProps } from '../part-props';
import { chooseWords, choiceRefusal, sourceChoiceId, useChooseHelper, type HelperSource } from './choose-helper';
import { connectWords } from './ConnectWindow';
import type { AiSettingsState } from './helper-api';
import { helperWords } from './helper-words';
import styles from './Helper.module.css';

/** The words of the choice of the source (board 44·9, `Pattern/Desktop/Settings Helper`). */
export const sourceWords = {
  legend: 'Откуда помощник',
  linkFirst: 'Сначала свяжите приложение с аккаунтом',
  chatModel: (name: string) => `Модель для чата: ${name}`,
  cardsModel: (name: string) => `Модель для чтения книг: ${name}`,
} as const;

interface SourceChoiceProps {
  api: SettingsPartProps['api'];
  state: AiSettingsState;
  /** The form of «Свой ИИ»; it tells whether it holds what the author has not saved. */
  renderOwn: (onUnsaved: (held: boolean) => void) => ReactNode;
}

/**
 * The choice of the source of «Помощник» (specs/2026-11-15-desk-account-link-web.md AW3.1; board 44·9): a choice sends the
 * settings whole — the source with the profile of «Свой ИИ» the server keeps —, so the profile lives through the helper of the
 * account and back. The helper of the account has nothing to set up: its words and the models of the server; no «Проверить
 * связь» and no list of models. Not linked — «Сначала свяжите приложение с аккаунтом» with «Связать с аккаунтом» and the intent
 * to choose it (AW3.2). «Применится после ответа помощника» stands under the choice, by the sign of the server (AW3.5). The shown
 * source is the server's, or the one the author picked until the server answered; «Свой ИИ» without a profile shows its form,
 * and «Сохранить» chooses it. The account in the place of unsaved edits of the form — a key typed and not saved too — asks
 * first the question of a way out with unsaved changes (specs/2026-11-13-desk-helper-web.md HW1.6).
 */
export function SourceChoice({ api, renderOwn, state }: SourceChoiceProps) {
  const toasts = useToasts();
  const [picked, setPicked] = useState<HelperSource | null>(null);
  // Whether the form of «Свой ИИ» holds what the author has not saved; the question before the account takes its place.
  const unsaved = useRef(false);
  const heldUnsaved = useCallback((held: boolean) => { unsaved.current = held; }, []);
  const [leavingForm, setLeavingForm] = useState(false);
  const choose = useChooseHelper(api);
  const account = useDesktopAccount(api).data;
  const shown: HelperSource = choose.isPending ? choose.variables : picked ?? (state.settings.source === 'account' ? 'account' : 'own');
  const refusal = choose.isError ? choiceRefusal(choose.error) : null;

  function send(source: HelperSource) {
    choose.mutate(source, {
      onSuccess: () => setPicked(null),
      // 409: the link is read again, and the helper of the account says «Связать с аккаунтом» in its place.
      onError: (error) => {
        setPicked(choiceRefusal(error) === 'helper.account_unlinked' ? 'account' : null);
        if (choiceRefusal(error) !== 'ai.account_unreachable' && choiceRefusal(error) !== 'helper.account_unlinked') {
          toasts.push({ message: chooseWords.failed, tone: 'error' });
        }
      },
    });
  }

  function pickAccount() {
    choose.reset();
    setPicked('account');
    if (account?.state === 'linked') send('account');
  }

  function pick(source: HelperSource) {
    if (source === shown || choose.isPending) return;
    if (source === 'account') {
      if (unsaved.current) setLeavingForm(true);
      else pickAccount();
      return;
    }
    choose.reset();
    // Back to «Свой ИИ»: its profile kept is chosen at once; without one its form opens and «Сохранить» chooses it.
    if (state.settings.source === 'account' && state.settings.own) send('own');
    else setPicked('own');
  }

  return (
    <div className={styles.helper}>
      <fieldset className={styles.source} id={sourceChoiceId}>
        <legend>{sourceWords.legend}</legend>
        {(['account', 'own'] as const).map((source) => (
          <label className={styles.option} key={source}>
            <input checked={shown === source} disabled={choose.isPending} name="helper-source" onChange={() => pick(source)} type="radio" value={source} />
            {source === 'account' ? connectWords.account : connectWords.own}
          </label>
        ))}
      </fieldset>
      {state.appliesAfterAnswer ? <Alert role="note" tone="info">{helperWords.appliesAfterAnswer}</Alert> : null}
      {refusal === 'ai.account_unreachable' ? (
        <Alert className={styles.refused} tone="error">
          <span>{chooseWords.unreachable}</span>
          <Button onClick={() => send('account')} size="sm" variant="ghost"><RotateCcw aria-hidden="true" size={14} />{chooseWords.retry}</Button>
        </Alert>
      ) : null}
      {shown === 'account' ? (
        <div className={styles.account}>
          {state.settings.source === 'account' && state.account ? (
            <>
              <p className={styles.text}>{connectWords.accountLine}</p>
              <p className={styles.note}>{sourceWords.chatModel(state.account.chatModel)}</p>
              <p className={styles.note}>{sourceWords.cardsModel(state.account.cardsModel)}</p>
            </>
          ) : account && account.state !== 'linked' ? (
            <>
              <p className={styles.text}>{sourceWords.linkFirst}</p>
              <LinkStart account={account} chooseHelper />
            </>
          ) : <p className={styles.text}>{connectWords.accountLine}</p>}
        </div>
      ) : renderOwn(heldUnsaved)}
      {leavingForm ? (
        <Confirm
          action={helperWords.leaveAction}
          irreversible
          onCancel={() => setLeavingForm(false)}
          onConfirm={() => { setLeavingForm(false); pickAccount(); }}
          title={helperWords.leaveTitle}
        >
          {helperWords.leaveLine}
        </Confirm>
      ) : null}
    </div>
  );
}
