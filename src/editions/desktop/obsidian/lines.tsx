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

/** The line of a work under the fence (OW2.8, OW3.1, OW7.4): what goes, what it may take, or that the vault is read by another. */
export function WorkLine({ detail, failures, error, title }: { detail?: string | undefined; error: unknown; failures: number; title: string }) {
  const waiting = failures > 0 && problemCode(error) === 'obsidian.busy';
  return (
    <div role="status">
      <LongWork detail={waiting ? undefined : detail} label={title} title={waiting ? words.busy(retryAfterOf(error)) : title} />
    </div>
  );
}

/** The ways out of a refusal as buttons (Data). */
export function RefusalExits({ exits, on }: { exits: readonly RefusalExit[]; on: Partial<Record<RefusalExit, () => void>> }) {
  const labels: Record<RefusalExit, string> = {
    retry: words.retry, change: words.change, checkAgain: words.checkAgain, rescan: words.rescan, showAgain: words.showAgain,
    toBooks: words.toBooks, loadBooksAgain: words.loadBooksAgain,
  };
  const shown = exits.filter((exit) => on[exit]);
  if (!shown.length) return null;
  return <div className={styles.row}>{shown.map((exit) => <Button key={exit} onClick={on[exit]} size="sm" variant="outline">{labels[exit]}</Button>)}</div>;
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
