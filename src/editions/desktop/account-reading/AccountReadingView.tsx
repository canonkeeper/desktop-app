// SPDX-License-Identifier: AGPL-3.0-only
import { BookOpen, Pause, Play, RotateCw, Settings } from 'lucide-react';
import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import type { PreparationActionInput, PreparationLine, PreparationView } from '../../../entities/manuscript/preparation-slot';
import { Button, type ButtonVariant } from '../../../ui/controls/Button';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { TariffsLink } from '../chat-ways';
import { useSetConsent, type ConsentChange } from '../reading/reading-api';
import { updatesPartKey } from '../settings/part-props';
import { accountReadingLinesOf, type AccountAction, type AccountLineWords } from './account-reading-line';
import { accountReadingWords, accountRefusalWords } from './account-reading-words';
import { AccountConsentWindow } from './AccountConsentWindow';

/** What a write of the line asks of the consent of the account: no sum (specs/2026-11-13-desk-account-ai.md AI3.4). */
function changeOf(action: AccountAction): ConsentChange | null {
  switch (action.kind) {
    case 'pause':
      return { state: 'paused' };
    case 'continue':
      return { state: 'allowed' };
    case 'retryUnread':
      return { state: 'allowed', retryUnread: true };
    default:
      return null;
  }
}

const refusedWords: Partial<Record<AccountAction['kind'], string>> = {
  pause: accountReadingWords.notPaused,
  continue: accountReadingWords.notContinued,
  retryUnread: accountReadingWords.retryRefused,
};

const icons: Partial<Record<AccountAction['kind'], ReactNode>> = {
  consent: <BookOpen aria-hidden="true" size={14} />,
  pause: <Pause aria-hidden="true" size={14} />,
  continue: <Play aria-hidden="true" size={14} />,
  retryUnread: <RotateCw aria-hidden="true" size={14} />,
  updates: <Settings aria-hidden="true" size={14} />,
};

/**
 * The line of the reading of the books by the helper of the account and its window (specs/2026-11-15-desk-account-words-web.md
 * AW5.1–AW5.5): the block the one slot of the edition gives when the server says the account reads (`reading.source`). A write
 * of a button sends one operation of the consent and is over once the new status is read; a refusal is a line of the one area
 * of toasts. «Тарифы» opens the page of the tiers of the site, «Открыть обновления» the part «Обновления». No sample is asked,
 * no window of a sum opens. Not `active` — the reading is not the account's: its window closes and it says nothing.
 */
export function useAccountReadingView({ navigate, projectId, status }: PreparationActionInput, active: boolean): PreparationView | null {
  const toasts = useToasts();
  // The window open over the page: an ephemeral fact of the page; «Назад» and Esc close it (overlay history).
  const [open, setOpen] = useState(false);
  const closeWindow = useCallback(() => setOpen(false), []);
  useEffect(() => {
    if (!active) setOpen(false);
  }, [active]);
  const write = useSetConsent(projectId);
  const pending = write.isPending ? write.variables?.label ?? null : null;

  function press(action: AccountAction) {
    if (action.kind === 'consent') {
      setOpen(true);
      return;
    }
    if (action.kind === 'updates') {
      navigate('/settings', { part: updatesPartKey });
      return;
    }
    const change = changeOf(action);
    if (!change || write.isPending || action.kind === 'tariffs') return;
    write.mutate({ change, label: action.label }, {
      onSuccess: () => {
        if (action.kind === 'retryUnread') toasts.push({ message: accountReadingWords.retryStarted, tone: 'success' });
      },
      onError: (error) => toasts.push({
        message: refusedWords[action.kind] ?? accountReadingWords.notContinued,
        detail: publicApiErrorMessage(error, accountReadingWords.tryAgain, { codes: accountRefusalWords }),
        tone: 'error',
      }),
    });
  }

  function button(action: AccountAction, line: AccountLineWords, last: boolean) {
    if (action.kind === 'tariffs') return <TariffsLink key="tariffs" />;
    const writes = changeOf(action) !== null;
    // The main button is the last one of the line, unless the place puts «Задать вопрос» after it.
    const variant: ButtonVariant = last && !line.ask ? 'primary' : 'outline';
    return (
      <Button
        disabledReason={writes && pending && pending !== action.label ? `Дождитесь, пока применится «${pending}»` : null}
        key={action.kind}
        loading={writes && pending === action.label}
        onClick={() => press(action)}
        size="sm"
        variant={variant}
      >
        {icons[action.kind] ?? null}{action.label}
      </Button>
    );
  }

  if (!active) return null;
  const lines: PreparationLine[] = (accountReadingLinesOf(status) ?? []).map((line) => ({
    label: line.label,
    title: line.title,
    actions: <>{line.actions.map((action, at) => button(action, line, at === line.actions.length - 1))}</>,
    ask: line.ask,
    tone: line.tone,
    progress: line.progress,
    under: line.under,
  }));
  if (lines.length === 0 && !open) return null;
  return { lines, windows: open ? <AccountConsentWindow onClose={closeWindow} projectId={projectId} /> : null };
}
