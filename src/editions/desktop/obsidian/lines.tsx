// SPDX-License-Identifier: AGPL-3.0-only
import { Button } from '../../../ui/controls/Button';
import { LongWork } from '../../../ui/feedback/LongWork';
import { problemCode, retryAfterOf, type ObsidianTree } from './obsidian-api';
import type { RefusalExit } from './refusals';
import { skippedTitle, skipWords, words } from './words';
import styles from './Obsidian.module.css';

/*
 * The lines the steps and the windows of the bridge share (specs/2026-11-15-desk-obsidian-web.md, specs/2026-11-15-desk-obsidian-book-web.md):
 * the line of a work under the fence, the ways out of a refusal, what was skipped at a reading.
 */

/**
 * The line of a work under the fence (OW2.8, OW3.1, OW7.4): what goes, what it may take, or that the vault is read by another.
 * `announced` — the line is its own status; false where the place around it is the status (the part of the export, XW2.2).
 */
export function WorkLine({ announced = true, detail, failures, error, title }: { announced?: boolean; detail?: string | undefined; error: unknown; failures: number; title: string }) {
  const waiting = failures > 0 && problemCode(error) === 'obsidian.busy';
  return (
    <div role={announced ? 'status' : undefined}>
      <LongWork detail={waiting ? undefined : detail} label={title} title={waiting ? words.busy(retryAfterOf(error)) : title} />
    </div>
  );
}

/**
 * The ways out of a refusal as buttons (Data). `reason` — why the ways that start a work of the bridge wait now: the export of
 * the saga holds the vault (specs/2026-11-16-desk-obsidian-export-web.md XW2.3); «К книгам» only leads away and never waits.
 */
export function RefusalExits({ exits, on, reason = null }: { exits: readonly RefusalExit[]; on: Partial<Record<RefusalExit, () => void>>; reason?: string | null }) {
  const labels: Record<RefusalExit, string> = {
    retry: words.retry, change: words.change, checkAgain: words.checkAgain, rescan: words.rescan, showAgain: words.showAgain,
    toBooks: words.toBooks, loadBooksAgain: words.loadBooksAgain,
  };
  const shown = exits.filter((exit) => on[exit]);
  if (!shown.length) return null;
  return (
    <div className={styles.row}>
      {shown.map((exit) => <Button disabledReason={exit === 'toBooks' ? null : reason} key={exit} onClick={on[exit]} size="sm" variant="outline">{labels[exit]}</Button>)}
    </div>
  );
}

/** «Пропущено при чтении: …» — opens into the lines «<причина> — N» (OW2.8, OW3.2). */
export function Skipped({ of, skipped }: { of: 'files' | 'notes'; skipped: ObsidianTree['skipped'] }) {
  const total = skipped.reduce((sum, skip) => sum + skip.count, 0);
  if (!total) return null;
  return (
    <details className={styles.skipped}>
      <summary>{skippedTitle(total, of)}</summary>
      <ul>{skipped.map((skip) => <li key={skip.reason}>{`${skipWords[skip.reason]} — ${skip.count.toLocaleString('ru-RU')}`}</li>)}</ul>
    </details>
  );
}
