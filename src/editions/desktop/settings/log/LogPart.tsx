// SPDX-License-Identifier: AGPL-3.0-only
import { ScrollText, Trash2 } from 'lucide-react';
import { useMemo, useState } from 'react';
import { loadFailureWords } from '../../../../entities/api-errors';
import type { Schema } from '../../../../shared/api';
import { formatFileSize } from '../../../../ui/copy';
import { Button } from '../../../../ui/controls/Button';
import { Select } from '../../../../ui/controls/Select';
import { Badge } from '../../../../ui/feedback/Badge';
import { EmptyFilter, EmptyState } from '../../../../ui/feedback/EmptyState';
import { ErrorBlock } from '../../../../ui/feedback/ErrorBlock';
import { SkeletonArea } from '../../../../ui/feedback/Skeleton';
import { useToasts } from '../../../../ui/feedback/useToastQueue';
import { Confirm, deletionLines } from '../../../../ui/overlays/Confirm';
import type { SettingsPartProps } from '../part-props';
import styles from '../Settings.module.css';
import { useClearSendLog, useSaveSendLog, useSendLog, useSendLogSendings, zoneOfTheComputer } from './log-api';
import { clearedWords, dayOf, dayWords, logWords, ownWords, sendingsWords, timeWords, type SendLogKind } from './log-words';
import log from './Log.module.css';

type Row = Schema<'SendLogRow'>;
type LogClient = SettingsPartProps['api'];

const kinds: readonly SendLogKind[] = ['question', 'reading', 'materials', 'check', 'program'];

/** The kind of the address, or none when it names none or one there is not (JR3.1). */
export function sendLogKindOf(value: string | null | undefined): SendLogKind | null {
  return kinds.find((kind) => kind === value) ?? null;
}

/**
 * «Настройки → Журнал» (specs/2026-11-14-desk-journal.md JR3; board 44·10, `Pattern/Desktop/Settings Log`): what the app sent the
 * helper from this computer by days — the time, the kind, what went, to whom and how much —, the choice of a kind in the
 * address, the details of a row, «Сохранить в файл…» and «Очистить журнал…». The words of what went are the server's.
 */
export function LogPart({ api, choose, kind }: SettingsPartProps) {
  const zone = useMemo(zoneOfTheComputer, []);
  const chosen = sendLogKindOf(kind);
  const rows = useSendLog(api, chosen, zone);
  const save = useSaveSendLog(api, chosen, zone);
  const clear = useClearSendLog(api);
  const toasts = useToasts();
  const [asking, setAsking] = useState(false);
  const pages = rows.data?.pages ?? [];
  const all = pages.flatMap((page) => page.rows);
  const first = pages[0];
  const clearedAt = first?.clearedAt ?? null;
  const pick = (value: SendLogKind | 'all') => choose?.(value === 'all' ? null : value);
  const toolbar = (
    <div className={log.toolbar}>
      <div className={log.choice}>
        <Select<SendLogKind | 'all'>
          label={logWords.choice}
          labelHidden
          onValueChange={pick}
          options={(['all', ...kinds] as const).map((value) => ({ value, label: logWords.kinds[value] }))}
          value={chosen ?? 'all'}
        />
      </div>
      <Button
        loading={save.isPending}
        onClick={() => save.mutate(undefined, {
          onSuccess: (fileName) => toasts.push({ message: logWords.saved(fileName), tone: 'success' }),
          onError: () => toasts.push({ message: logWords.notSaved, tone: 'error' }),
        })}
        variant="outline"
      >
        {logWords.save}
      </Button>
      <Button onClick={() => setAsking(true)} variant="outline"><Trash2 aria-hidden="true" size={16} />{logWords.clear}</Button>
    </div>
  );
  return (
    <>
      <div className={styles.block}>
        <p className={styles.text}>{logWords.about}</p>
        <p className={styles.note}>{logWords.places}</p>
      </div>
      {rows.isPending ? <SkeletonArea count={3} shape="lines" /> : rows.isError && !rows.data ? (
        <ErrorBlock {...loadFailureWords(rows.error)} onRetry={() => void rows.refetch()} retrying={rows.isRefetching} title={logWords.failed} />
      ) : all.length === 0 && !chosen ? (
        clearedAt ? (
          <EmptyState icon={<ScrollText size={20} />} title={clearedWords(clearedAt)} why={logWords.clearedEmpty} />
        ) : (
          <EmptyState icon={<ScrollText size={20} />} title={logWords.empty.title} why={logWords.empty.why} />
        )
      ) : (
        <>
          {toolbar}
          {all.length === 0 ? (
            <EmptyFilter onReset={() => pick('all')} reset={logWords.noKind.reset} words={logWords.noKind.title} />
          ) : (
            <Days api={api} rows={all} sagas={first?.sagas ?? 0} zone={zone} />
          )}
          {rows.hasNextPage ? (
            <div><Button loading={rows.isFetchingNextPage} onClick={() => void rows.fetchNextPage()} variant="ghost">{logWords.next}</Button></div>
          ) : null}
          {clearedAt && all.length > 0 && !rows.hasNextPage ? <p className={styles.note}>{`${clearedWords(clearedAt)} — раньше записей нет`}</p> : null}
        </>
      )}
      {asking ? (
        <Confirm
          action={logWords.question.action}
          also={logWords.question.also}
          busy={clear.isPending}
          error={clear.isError ? logWords.notCleared : undefined}
          irreversible
          onCancel={() => {
            setAsking(false);
            clear.reset();
          }}
          onConfirm={() => clear.mutate(undefined, {
            onSuccess: () => {
              setAsking(false);
              toasts.push({ message: logWords.cleared, tone: 'success' });
            },
          })}
          title={logWords.question.title}
        >
          {deletionLines.gone}
        </Confirm>
      ) : null}
    </>
  );
}

/** The rows by the days of the computer of the author, each day under its heading. */
function Days({ api, rows, sagas, zone }: { api: LogClient; rows: readonly Row[]; sagas: number; zone: string }) {
  const days: Array<{ day: string; rows: Row[] }> = [];
  for (const row of rows) {
    const day = dayOf(row.startedAt);
    const last = days.at(-1);
    if (last?.day === day) last.rows.push(row);
    else days.push({ day, rows: [row] });
  }
  return (
    <div className={log.days}>
      {days.map((group) => (
        <section aria-label={dayWords(group.rows[0]!.startedAt)} className={log.day} key={group.day}>
          <h3 className={log.dayTitle}>{dayWords(group.rows[0]!.startedAt)}</h3>
          <ul className={log.rows}>
            {group.rows.map((row) => <LogRow api={api} key={row.key} row={row} sagas={sagas} zone={zone} />)}
          </ul>
        </section>
      ))}
    </div>
  );
}

/**
 * A row (board 44·10): «время · вид · что ушло · получатель · размер», under the time what it was about — the chat, the saga
 * of several, «идёт», the first time — and under the size «Подробно»; an open row has «Свернуть» at the end of its details.
 */
function LogRow({ api, row, sagas, zone }: { api: LogClient; row: Row; sagas: number; zone: string }) {
  const [open, setOpen] = useState(false);
  const about: string[] = [];
  if (row.chat) about.push(row.chat.deleted || !row.chat.title ? logWords.chatGone : `«${row.chat.title}»`);
  if (row.saga && sagas > 1) about.push(row.saga.deleted ? `«${row.saga.title}» (в «Недавно удалённых»)` : `«${row.saga.title}»`);
  if (row.running) about.push(logWords.running);
  if (row.firstTime) about.push(logWords.firstTime);
  if (row.kind === 'program') about.push(logWords.given);
  return (
    <li className={log.row}>
      <div className={log.line}>
        <span className={log.time}>{timeWords(row.startedAt)}</span>
        <Badge className={log.kind}>{logWords.kinds[row.kind]}</Badge>
        <span className={log.what}>
          <span className={log.sent}>{row.whatWent}</span>
          <span className={log.to}>{row.recipients.join(', ')}</span>
        </span>
        <span className={log.size}>{formatFileSize(row.bytes)}</span>
      </div>
      <div className={log.foot}>
        <p className={log.about}>{about.join(' · ')}</p>
        {open ? null : <Button aria-expanded={false} onClick={() => setOpen(true)} size="sm" variant="ghost">{logWords.more}</Button>}
      </div>
      {open ? <Details api={api} onClose={() => setOpen(false)} row={row} zone={zone} /> : null}
    </li>
  );
}

/**
 * The details of a row (JR3.2): how many sendings and how much, the line of the conversation of a question, the numbers of
 * «Свой ИИ», and the sendings — «время · что ушло · размер · исход», to whom only when the row went to several.
 */
function Details({ api, onClose, row, zone }: { api: LogClient; onClose: () => void; row: Row; zone: string }) {
  const sendings = useSendLogSendings(api, row, zone);
  const several = row.recipients.length > 1;
  const lines = sendings.data?.pages.flatMap((page) => page.sendings) ?? [];
  return (
    <div className={log.details}>
      <p className={styles.note}>{`${sendingsWords(row.sendings, row.kind)} · всего ${formatFileSize(row.bytes)}`}</p>
      {row.kind === 'question' ? <p className={styles.note}>{logWords.conversation}</p> : null}
      {row.own ? <p className={styles.note}>{ownWords(row.own)}</p> : null}
      {sendings.isPending ? <SkeletonArea count={2} shape="lines" /> : sendings.isError && !sendings.data ? (
        <ErrorBlock {...loadFailureWords(sendings.error)} onRetry={() => void sendings.refetch()} retrying={sendings.isRefetching} title={logWords.failed} />
      ) : (
        <ul className={log.sendings}>
          {lines.map((line, index) => (
            <li className={log.sending} key={`${line.at}-${index}`}>
              {[timeWords(line.at), line.whatWent, ...(line.firstTime ? [logWords.firstTime] : []), ...(several ? [line.recipient] : []),
                formatFileSize(line.bytes), ...(line.outcome ? [line.outcome] : []), ...(line.repeat ? [logWords.repeat] : [])].join(' · ')}
            </li>
          ))}
        </ul>
      )}
      {sendings.hasNextPage ? (
        <div><Button loading={sendings.isFetchingNextPage} onClick={() => void sendings.fetchNextPage()} size="sm" variant="ghost">{logWords.next}</Button></div>
      ) : null}
      <div><Button aria-expanded onClick={onClose} size="sm" variant="ghost">{logWords.less}</Button></div>
    </div>
  );
}
