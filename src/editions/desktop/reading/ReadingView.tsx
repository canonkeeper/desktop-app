// SPDX-License-Identifier: AGPL-3.0-only
import { BookOpen, ExternalLink, Link2, Pause, Play, Plug, RotateCw, Settings } from 'lucide-react';
import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import type { PreparationActionInput, PreparationLine, PreparationView } from '../../../entities/manuscript/preparation-slot';
import { Button, type ButtonVariant } from '../../../ui/controls/Button';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { relinkHelper, useAccountLink } from '../account/AccountLink';
import { useHelperReason, useNotConnectedReason } from '../account/helper-reason';
import { ConnectWindow } from '../settings/helper/ConnectWindow';
import { helperPartKey } from '../settings/part-props';
import { ConsentWindow } from './ConsentWindow';
import { useReadingSample, useSetConsent, type ConsentChange } from './reading-api';
import { readingLinesOf, type ReadingAction, type ReadingLineWords } from './reading-line';
import { refusalWords, repeatWords, sampleLine } from './reading-words';
import { StopSumWindow } from './StopSumWindow';

/** What a write of the line asks of the consent (the table CN4.1 of specs/2026-11-13-desk-consent.md). */
function changeOf(action: ReadingAction): ConsentChange | null {
  switch (action.kind) {
    case 'pause':
      return { state: 'paused' };
    case 'continue':
      return { state: 'allowed' };
    case 'continueBy':
      return { state: 'allowed', continueBy: action.step };
    case 'retryUnread':
      return { state: 'allowed', retryUnread: true };
    default:
      return null;
  }
}

/** What the line says when a write of it was refused: the state of the line stays as the server says it. */
const refusedWords: Partial<Record<ReadingAction['kind'], string>> = {
  pause: 'Чтение не встало на паузу',
  continue: 'Чтение не продолжилось',
  continueBy: 'Чтение не продолжилось',
  retryUnread: repeatWords.refused,
};

/** What the line says when a write of it went through: «Повторить» says it tries again (specs/2026-11-15-desk-reading-rest.md RR2.5). */
const doneWords: Partial<Record<ReadingAction['kind'], string>> = {
  retryUnread: repeatWords.started,
};

const icons: Partial<Record<ReadingAction['kind'], ReactNode>> = {
  connect: <Plug aria-hidden="true" size={14} />,
  relink: <Link2 aria-hidden="true" size={14} />,
  linkWindow: <ExternalLink aria-hidden="true" size={14} />,
  consent: <BookOpen aria-hidden="true" size={14} />,
  pause: <Pause aria-hidden="true" size={14} />,
  continue: <Play aria-hidden="true" size={14} />,
  continueBy: <Play aria-hidden="true" size={14} />,
  settings: <Settings aria-hidden="true" size={14} />,
  retryUnread: <RotateCw aria-hidden="true" size={14} />,
};

const live = (state: string | undefined) => state === 'queued' || state === 'running';

/**
 * The line of the reading of the app by the status of the server — the value of the one slot of the edition
 * (specs/2026-11-13-desk-consent-web.md RW1; specs/2026-11-13-desk-helper-web.md HW3.1): the words of each state of the
 * reading by «Свой ИИ» with its buttons, and the windows they open — «Прочитать книги своим ИИ?» and «Сумма остановки». The
 * same on the shelf, in the chat and on the map. A write of a button sends one operation of the consent and is over once
 * the new status of the server is read: until then the button is busy; a refusal is a line of the one area of toasts, and
 * the line stays as the server says it (RW1.3). While a sample runs the line says it, by the sample of the server (RW2.2).
 * The helper not connected — the one reason of the edition with its way out (specs/2026-11-15-desk-account-link-web.md
 * AW3.3, AW3.4): «Подключить помощника» opens the window of the choice, «Связать снова» starts a link, «Открыть окно связи»
 * shows the waiting. Not `active` — the helper of the account reads the books, and its block speaks
 * (specs/2026-11-15-desk-account-words-web.md AW5.1): the windows of this block close, no sample is asked, nothing is said.
 */
export function useReadingView({ navigate, projectId, status }: PreparationActionInput, active = true): PreparationView | null {
  const toasts = useToasts();
  // The window open over the page: an ephemeral fact of the page; «Назад» and Esc close it (overlay history).
  const [open, setOpen] = useState<'connect' | 'consent' | 'sum' | null>(null);
  const closeWindow = useCallback(() => setOpen(null), []);
  useEffect(() => {
    if (!active) setOpen(null);
  }, [active]);
  const write = useSetConsent(projectId);
  const link = useAccountLink();
  const helper = useHelperReason(active && status?.preparation?.waiting === 'helper');
  // The helper not connected by the answers of the settings and the link while the status says nothing of a waiting — a read
  // saga (decision of the coordinator 15.11): the same reason and its button.
  const unconnected = useNotConnectedReason(active);
  const words = readingLinesOf(status, helper, unconnected);
  const waiting = status?.reading?.state === 'waiting_consent';
  const sample = useReadingSample(projectId, active && waiting).data;
  const reading = status?.reading ?? null;
  const pending = write.isPending ? write.variables?.label ?? null : null;

  function press(action: ReadingAction) {
    if (action.kind === 'settings') {
      navigate('/settings', { part: helperPartKey });
      return;
    }
    if (action.kind === 'relink') {
      if (link) relinkHelper(link);
      return;
    }
    if (action.kind === 'linkWindow') {
      link?.openWindow();
      return;
    }
    if (action.kind === 'connect' || action.kind === 'consent' || action.kind === 'sum') {
      setOpen(action.kind);
      return;
    }
    const change = changeOf(action);
    if (!change || write.isPending) return;
    write.mutate({ change, label: action.label }, {
      onSuccess: () => {
        const done = doneWords[action.kind];
        if (done) toasts.push({ message: done, tone: 'success' });
      },
      onError: (error) => toasts.push({
        message: refusedWords[action.kind] ?? 'Не получилось',
        detail: publicApiErrorMessage(error, 'Попробуйте ещё раз.', { codes: refusalWords }),
        tone: 'error',
      }),
    });
  }

  function button(action: ReadingAction, line: ReadingLineWords, last: boolean) {
    const writes = changeOf(action) !== null;
    // The main button is the last one of the line, unless the place puts «Задать вопрос» after it; «Изменить сумму…» of a
    // reading that goes is quiet (RW1.1).
    const variant: ButtonVariant = last && !line.ask ? 'primary' : action.kind === 'sum' && line.tone === 'work' ? 'ghost' : 'outline';
    return (
      <Button
        disabledReason={writes && pending && pending !== action.label
          ? `Дождитесь, пока применится «${pending}»`
          : action.kind === 'retryUnread' ? action.disabledReason ?? null : null}
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

  const lines: PreparationLine[] = (words ?? []).map((line, index) => ({
    label: line.label,
    title: index === 0 && waiting && sample && live(sample.state) ? sampleLine(sample) : line.title,
    actions: line.actions.length > 0 ? <>{line.actions.map((action, at) => button(action, line, at === line.actions.length - 1))}</> : null,
    ask: line.ask,
    tone: line.tone,
    progress: line.progress,
    under: line.under,
  }));
  const windows = (
    <>
      {open === 'connect' ? <ConnectWindow navigate={navigate} onClose={closeWindow} /> : null}
      {open === 'consent' ? <ConsentWindow navigate={navigate} onClose={closeWindow} projectId={projectId} /> : null}
      {open === 'sum' && reading ? <StopSumWindow onClose={closeWindow} projectId={projectId} reading={reading} /> : null}
    </>
  );
  if (!active || (lines.length === 0 && open === null)) return null;
  return { lines, windows };
}
