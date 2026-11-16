// SPDX-License-Identifier: AGPL-3.0-only
import { RefreshCw, RotateCw, Upload } from 'lucide-react';
import { Fragment, useEffect, useRef, useState, type ReactNode } from 'react';
import { loadFailureWords } from '../../../entities/api-errors';
import { useIndexingStatusQuery } from '../../../entities/manuscript/api';
import { mapReadingLine } from '../../../entities/manuscript/preparation-copy';
import { usePreparationView } from '../../../entities/manuscript/preparation-slot';
import { useSagaMapQuery } from '../../../entities/saga/api';
import type { CanonKeeperApiClient } from '../../../shared/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { Alert } from '../../../ui/feedback/Alert';
import { LongWork } from '../../../ui/feedback/LongWork';
import { Confirm } from '../../../ui/overlays/Confirm';
import { checkedWhen } from '../updates/update-words';
import { WorkLine } from './lines';
import type { ObsidianExport, ObsidianVault } from './obsidian-api';
import { refusalOf } from './refusals';
import { useExport, useExportOf, type ExportState } from './use-export';
import { exportName, exportSkipWords, exportSummary, exportWords, namesWords, words } from './words';
import styles from './Obsidian.module.css';

export interface ExportPartProps {
  api: CanonKeeperApiClient;
  navigate: RouteNavigate;
  projectId: string;
  vault: ObsidianVault;
  /** The folder of the saga is not in its place: the button waits with the words of the page (XW1.1). */
  missing: boolean;
  /** A work of the part «Книги и записи из Obsidian» holds the vault of the saga now: why the button waits (XW2.3). */
  waits: string | null;
  /** The address opened the part (`part=export`): the focus goes to its heading once it stands (XW1.3). */
  focus: boolean;
  /**
   * The place of the part on the page stands: the step above it shows its first look, preview or refusal — a list of folders
   * that comes later would push the heading the focus is on out of sight.
   */
  settled: boolean;
}

/** Why the button of the part waits for the map, by the answer of the map (XW3.1); null — the server decides. */
function mapReason(map: { building?: boolean; coverage: { chapters: number; withCards: number } } | undefined) {
  if (!map) return null;
  if (map.coverage.chapters === 0) return exportWords.noBooks;
  if (map.coverage.withCards === 0) return exportWords.noCards;
  return map.building ? exportWords.updating : null;
}

/**
 * «Карта саги в Obsidian» (specs/2026-11-16-desk-obsidian-export-web.md XW1–XW3; board 44·59–44·65, `Pattern/Desktop/Obsidian
 * Export`): what the export writes and that the author's edits are his, the last export or the folder of the export, one button
 * — «Выгрузить в Obsidian» before the first, «Выгрузить снова» after — that waits, with its reason, for the folder, for the one
 * fence of the bridge and for the map; under it the run, the refusal or the result of the latest export of the saga, read from
 * the cache of the mutations, and the line of the reading of the edition when the helper has read no chapter.
 */
export function ExportPart({ api, focus, missing, navigate, projectId, settled, vault, waits }: ExportPartProps) {
  const latest = useExportOf(projectId);
  const going = latest?.status === 'pending';
  const exporting = useExport(api);
  const map = useSagaMapQuery(api, projectId, 'published').data;
  // The line of the reading of the edition, as the empty map says it (XW3.1): only when the helper has read no chapter of books.
  const unread = map !== undefined && map.coverage.chapters > 0 && map.coverage.withCards === 0;
  const status = useIndexingStatusQuery(api, projectId, { enabled: unread, follow: true }).data;
  const edition = usePreparationView({ navigate, projectId, status });
  const [asked, setAsked] = useState(false);
  const heading = useRef<HTMLHeadingElement>(null);
  const focused = useRef(false);
  useEffect(() => {
    if (!focus || !settled || focused.current || !heading.current) return;
    focused.current = true;
    // Unless the author moved the focus himself: the root puts it on the heading of the page first (XW1.3).
    const active = document.activeElement;
    if (!active || active === document.body || active.matches('[data-page-heading], [data-page-content]')) heading.current.focus();
  }, [focus, settled]);

  const reason = missing ? words.missing : going ? exportWords.going : waits ?? mapReason(map);
  const last = vault.exportDir && vault.lastExportAt ? exportWords.last(checkedWhen(vault.lastExportAt), vault.exportDir)
    : vault.exportDir ? exportWords.folder(vault.exportDir) : null;
  // The line of the reading as «Книги» draw it (board 44·64, as 44·3): the words of the edition and its buttons, its bar.
  const readingLines = unread ? (edition?.lines.length ? edition.lines.map((line, index) => (
    <LongWork action={line.actions} announcement={line.progress !== null ? line.label : line.title} key={line.label}
      label={index === 0 ? exportWords.reading : line.label} progress={line.progress} title={line.title} tone={line.tone} />
  )) : status?.preparation ? <LongWork label={exportWords.reading} title={mapReadingLine(status.preparation)} /> : null) : null;
  const answer = latest?.status === 'success' ? latest.answer : undefined;

  return (
    <section aria-labelledby="obsidian-export" className={styles.exportPart}>
      <h2 className={styles.partTitle} id="obsidian-export" ref={heading} tabIndex={-1}>{exportWords.title}</h2>
      {vault.exportDir ? null : <p className={styles.text}>{exportWords.text}</p>}
      <p className={styles.muted}>{exportWords.edits}</p>
      {last ? <p className={styles.text}>{last}</p> : null}
      <div className={styles.row}>
        <Button disabledReason={reason} onClick={() => exporting.start(projectId)} variant="primary">
          <Upload aria-hidden="true" size={16} />{vault.exportDir ? exportWords.again : exportWords.first}
        </Button>
      </div>
      {/* The state of the run, heard as it changes (XW2.2): the run, then the folder it went to or that it did not end. */}
      <div aria-label={exportWords.title} className={styles.exportState} role="status">
        {latest?.status === 'pending' ? (
          <WorkLine announced={false} detail={exportWords.leave} error={latest.failureReason} failures={latest.failures} title={exportWords.working} />
        ) : null}
        {answer ? (answer.complete ? <strong className={styles.text}>{exportWords.done(answer.folder)}</strong> : (
          <Alert role="note" tone="warning"><p className={styles.text}>{exportWords.partial}</p></Alert>
        )) : null}
      </div>
      {latest?.status === 'error' ? <ExportRefusal exportDir={vault.exportDir} latest={latest} onRetry={() => exporting.repeat(latest.write)} reason={reason} /> : null}
      {answer ? <ExportResult answer={answer} onReplace={() => setAsked(true)} reason={reason} /> : null}
      {readingLines}
      {unread ? edition?.windows : null}
      {asked && answer ? (
        <Confirm action={exportWords.confirm} actionIcon={<RefreshCw aria-hidden="true" size={16} />} actionReason={reason} onCancel={() => setAsked(false)}
          onConfirm={() => { setAsked(false); exporting.start(projectId, true); }} title={exportWords.questionTitle}>
          {exportWords.question(answer.kept.count, answer.folder)}
        </Confirm>
      ) : null}
    </section>
  );
}

/**
 * A refusal of the export (XW3.2; board 44·65): a line of an error under the button by the words of its code — the way out is the
 * button of the part —; only `busy` after its tries and a failure give «Повторить», with the key and the word of the export, and
 * it waits for what the button of the part waits for. What leads elsewhere says nothing here: an unlinked folder leads the page.
 */
function ExportRefusal({ exportDir, latest, onRetry, reason }: { exportDir: string | undefined; latest: ExportState; onRetry: () => void; reason: string | null }) {
  const refusal = refusalOf(latest.error, 'export', undefined, exportDir);
  const text = refusal.failure ? loadFailureWords(latest.error).cause || words.failure : refusal.text;
  if (!text) return null;
  return (
    <Alert tone="error">
      <p className={styles.text}>{text}</p>
      {refusal.exits.includes('retry') ? (
        <div className={styles.row}><Button disabledReason={reason} onClick={onRetry} size="sm" variant="outline"><RotateCw aria-hidden="true" size={14} />{words.retry}</Button></div>
      ) : null}
    </Alert>
  );
}

/**
 * A group of the result with its files by their paths, the first 50 and the rest by their count (XW2.4); `under` stands under the
 * group folded or not — the note of «_Удалено» (board 44·61, 44·63).
 */
function Group({ children, group, label, open = false, under }: { children?: ReactNode; group: { count: number; names: string[] }; label: string; open?: boolean; under?: ReactNode }) {
  if (!group.count) return null;
  return (
    <>
      <details className={styles.exportGroup} open={open}>
        <summary>{exportWords.group(label, group.count)}</summary>
        <div className={styles.exportNames}>
          {group.names.length ? <p className={styles.text}>{namesWords(group.names.map(exportName), group.count)}</p> : null}
          {children}
        </div>
      </details>
      {under}
    </>
  );
}

/** The files that did not read in the order of the table of their reasons (Data, board 44·63), not of the answer. */
function skippedInOrder(skipped: ObsidianExport['skipped']) {
  const order = Object.keys(exportSkipWords);
  return [...skipped].sort((left, right) => order.indexOf(left.reason) - order.indexOf(right.reason));
}

/**
 * The result of an export (XW2.4–XW2.7; board 44·61–44·63) under the line of its state («Выгружено в папку …» or the warning of a
 * run that did not end): the summary; the groups — the created, updated and moved folded, the note of «_Удалено» under its group,
 * those the author changed and those that did not read open —; «Заменить изменённые…» asks first and waits for what the button
 * of the part waits for (XW2.5).
 */
function ExportResult({ answer, onReplace, reason }: { answer: ObsidianExport; onReplace: () => void; reason: string | null }) {
  const summary = exportSummary(answer);
  const skipped = answer.skipped.reduce((sum, skip) => sum + skip.count, 0);
  return (
    <div className={styles.exportResult}>
      {summary ? <p className={styles.text}>{summary}</p> : null}
      <Group group={answer.created} label={exportWords.created} />
      <Group group={answer.updated} label={exportWords.updated} />
      <Group group={answer.moved} label={exportWords.moved} under={<p className={styles.exportNote}>{exportWords.movedNote}</p>} />
      <Group group={answer.kept} label={exportWords.kept} open>
        <div className={styles.row}><Button disabledReason={reason} onClick={onReplace} size="sm" variant="outline">{exportWords.replace}</Button></div>
      </Group>
      {skipped ? (
        <details className={styles.exportGroup} open>
          <summary>{exportWords.group(exportWords.skipped, skipped)}</summary>
          <div className={styles.exportNames}>
            {skippedInOrder(answer.skipped).map((skip) => (
              <Fragment key={skip.reason}>
                <p className={styles.text}>{exportSkipWords[skip.reason]}</p>
                <p className={styles.muted}>{namesWords(skip.names.map(exportName), skip.count)}</p>
              </Fragment>
            ))}
          </div>
        </details>
      ) : null}
    </div>
  );
}
