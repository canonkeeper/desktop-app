// SPDX-License-Identifier: AGPL-3.0-only
import { CircleAlert, CircleCheck, Clock3, ExternalLink, List, LoaderCircle, Package, RefreshCw, RotateCw, WifiOff, X } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import { desktopShell } from '../../../shared/desktop/shell';
import { Button, ButtonLink } from '../../../ui/controls/Button';
import { Alert } from '../../../ui/feedback/Alert';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { Progress } from '../../../ui/feedback/Progress';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { external } from '../external';
import type { SettingsPartProps } from '../settings/part-props';
import { useAppUpdate, useUpdateActions } from './app-update';
import { Switch } from '../Switch';
import { partStateOf, releasesUrl, updateWords as words, type PartCard, type PartState, type StateButton } from './update-words';
import { WhatsNew } from './WhatsNew';
import styles from './Updates.module.css';

/** The reason of a closed button of the state, or none: the button is open. */
function reasonOf(button: StateButton) {
  return button && button !== 'shown' ? button.reason : null;
}

/**
 * «Настройки → Обновления» (specs/2026-11-15-desk-updates-web.md UW2.1–UW2.5; board 44·14, `Pattern/Desktop/Settings
 * Updates`): the version that runs, the switch of the daily check, «Проверить сейчас» and the state — each state its words
 * and its buttons by the table «Состояния части»: a button stands, stands closed with its reason, or is not there. The
 * part reads the update when the author enters it and after its own actions; it counts and remembers nothing.
 */
export function UpdatesPart({ api }: SettingsPartProps) {
  const update = useAppUpdate(api, { enter: true });
  const actions = useUpdateActions(api);
  // The window «Что нового» is a fact of this page only.
  const [notesOpen, setNotesOpen] = useState(false);
  if (update.isPending) return <SkeletonArea count={3} shape="lines" />;
  if (update.isError) {
    return <ErrorBlock {...loadFailureWords(update.error)} onRetry={() => void update.refetch()} retrying={update.isRefetching} title="Не открылись данные приложения" />;
  }
  const state = partStateOf(update.data);
  const pending = actions.check.isPending || actions.install.isPending || actions.cancel.isPending;
  const install = (reason: string | null = null, label = words.install) => (
    <Button disabledReason={reason} loading={actions.install.isPending} onClick={() => {
      if (!pending) actions.install.mutate();
    }} size="sm" variant="primary">
      <RotateCw aria-hidden="true" size={16} />{label}
    </Button>
  );
  return (
    <div className={styles.part}>
      {state.version ? <p className={styles.text}>{`Версия ${state.version}`}</p> : null}
      {state.daily === null ? null : (
        <div className={styles.daily}>
          <Switch busy={actions.daily.isPending} checked={state.daily} label={words.switchLabel} onChange={(value) => actions.daily.mutate(value)} />
          {state.dailyOff ? <p className={styles.note}>{words.dailyOff}</p> : null}
        </div>
      )}
      {state.check ? (
        <div className={styles.row}>
          <Button disabledReason={reasonOf(state.check)} loading={actions.check.isPending} onClick={() => {
            if (!pending) actions.check.mutate();
          }} size="sm" variant="outline">
            <RefreshCw aria-hidden="true" size={16} />{words.checkNow}
          </Button>
        </div>
      ) : null}
      <Status state={state}
        cancel={state.cancel ? (
          <Button loading={actions.cancel.isPending} onClick={() => {
            if (!pending) actions.cancel.mutate();
          }} size="sm" variant="outline">
            <X aria-hidden="true" size={16} />{words.cancel}
          </Button>
        ) : null}
        retry={state.retry ? install() : null}
      />
      {state.card ? <AvailableCard card={state.card} install={install(reasonOf(state.card.install))} onWhatsNew={() => setNotesOpen(true)} /> : null}
      {notesOpen && state.card ? <WhatsNew notes={state.card.notes} onClose={() => setNotesOpen(false)} version={state.card.version} /> : null}
    </div>
  );
}

/** The state in its look: a quiet line, a work going on with its bar, a failure with its way out, the build updated by hand. */
function Status({ cancel, retry, state }: { cancel: ReactNode; retry: ReactNode; state: PartState }) {
  const toasts = useToasts();
  const { status } = state;
  if (!status) return null;
  const lines = <div className={styles.statusLines}>{status.lines.map((line) => <p key={line}>{line}</p>)}</div>;
  switch (status.tone) {
    case 'latest':
    case 'never':
    case 'checking':
      return (
        <>
          <div className={styles.status} data-tone={status.tone}>
            {status.tone === 'latest' ? <CircleCheck aria-hidden="true" className={styles.statusIcon} size={16} />
              : status.tone === 'never' ? <Clock3 aria-hidden="true" className={styles.statusIcon} size={16} />
                : <LoaderCircle aria-hidden="true" className={`${styles.statusIcon} ${styles.spin}`} size={16} />}
            {lines}
          </div>
          {state.lastCheck ? <p className={styles.note}>{state.lastCheck}</p> : null}
        </>
      );
    case 'checked':
      return <p className={styles.note}>{status.lines[0]}</p>;
    case 'offline':
      return (
        <>
          {state.lastCheck ? <p className={styles.note}>{state.lastCheck}</p> : null}
          <Alert className={styles.alert} role="note" tone="warning">
            <div className={styles.alertBody}><WifiOff aria-hidden="true" size={16} />{lines}</div>
          </Alert>
        </>
      );
    case 'downloading':
    case 'waiting':
    case 'installing':
      return (
        <div className={styles.going} data-tone={status.tone}>
          {lines}
          {state.progress !== null ? <Progress aria-label={status.lines[0]} className={styles.progress} value={state.progress} /> : null}
          {cancel ? <div>{cancel}</div> : null}
        </div>
      );
    case 'failure':
      return (
        <Alert className={styles.alert} role="note" tone="error">
          <div className={styles.alertBody}><CircleAlert aria-hidden="true" size={16} />{lines}</div>
          {retry ? <div className={styles.alertAction}>{retry}</div> : null}
        </Alert>
      );
    case 'manual':
      return (
        <Alert className={styles.alert} role="note" tone="info">
          <div className={styles.alertBody}><Package aria-hidden="true" size={16} />{lines}</div>
          <div className={styles.alertAction}>
            <ButtonLink {...external(releasesUrl, desktopShell(), toasts)} size="sm" variant="outline">
              <ExternalLink aria-hidden="true" size={16} />{words.releases}
            </ButtonLink>
          </div>
        </Alert>
      );
  }
}

/** «Доступна версия 1.3.0» (`Available`): the main «Обновить и перезапустить», «Что нового» when the notes say something. */
function AvailableCard({ card, install, onWhatsNew }: { card: PartCard; install: ReactNode; onWhatsNew: () => void }) {
  return (
    <section aria-label={card.title} className={styles.card}>
      <h3 className={styles.cardTitle}>{card.title}</h3>
      <div className={styles.cardActions}>
        {install}
        {card.notes.trim() ? (
          <Button onClick={onWhatsNew} size="sm" variant="ghost"><List aria-hidden="true" size={16} />{words.whatsNew}</Button>
        ) : null}
      </div>
      {card.adminAsk ? <p className={styles.adminNote}>{words.adminAsk}</p> : null}
    </section>
  );
}
