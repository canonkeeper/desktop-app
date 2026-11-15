// SPDX-License-Identifier: AGPL-3.0-only
import { CircleCheck, CircleX, LoaderCircle, RotateCw, Square } from 'lucide-react';
import { useId } from 'react';
import { Button } from '../../../../ui/controls/Button';
import { Alert } from '../../../../ui/feedback/Alert';
import { LongWork } from '../../../../ui/feedback/LongWork';
import { SupportReference } from '../../../../ui/feedback/SupportReference';
import { checkGroups, checkWords, verdictWords, type CheckGroup } from './check-words';
import type { AiCheck, AiSettingsState } from './helper-api';
import { helperWords, type Preset } from './helper-words';
import styles from './Helper.module.css';

export interface CheckBlockProps {
  preset: Preset;
  /** The check of this run, as getAiCheck says it. */
  check: AiCheck | undefined;
  /** The last check kept, with its verdicts now (getAiSettings). */
  report: AiSettingsState['check'];
  /** The form changes what the check checked and is not saved yet: what was checked is not what the author sees. */
  changed: boolean;
  onAgain: () => void;
  onStop: () => void;
  stopping: boolean;
}

function Lines({ groups }: { groups: readonly CheckGroup[] }) {
  return groups.map((group, index) => (
    <div key={group.role ?? `all-${index}`}>
      {group.role ? <p className={styles.group}>{`${group.role === 'chat' ? helperWords.chatModel : helperWords.cardsModel} · ${group.model}`}</p> : null}
      <ul className={styles.lines}>
        {group.lines.map((line, at) => (
          <li className={styles.line} data-passed={line.running ? undefined : line.passed} data-running={line.running || undefined} key={at}>
            {/* The words say whether it passed or runs; the mark is for the eye. */}
            {line.running ? <LoaderCircle aria-hidden="true" className={`${styles.mark} ${styles.going}`} size={16} />
              : line.passed ? <CircleCheck aria-hidden="true" className={styles.mark} size={16} /> : <CircleX aria-hidden="true" className={styles.mark} size={16} />}
            <span>{line.words}</span>
            {line.advice ? <span className={styles.advice}>{line.advice}</span> : null}
            {line.code ? <span className={styles.advice}><SupportReference code={line.code} /></span> : null}
          </li>
        ))}
      </ul>
    </div>
  ));
}

/**
 * «Проверить связь» and its verdict (specs/2026-11-13-desk-helper-web.md HW2; board 44·26): while the check runs — «Проверяю
 * связь… До трёх минут» with «Остановить» and the probes as they go; then the lines with their marks and the verdicts «для
 * чата» and «для чтения книг». A report made before a change of what it checked says so and offers to check again. The
 * check is not required: the helper works without it.
 */
export function CheckBlock({ changed, check, onAgain, onStop, preset, report, stopping }: CheckBlockProps) {
  const id = useId();
  if (check?.state === 'running') {
    return (
      <div aria-labelledby={id} className={styles.check} role="group">
        <span hidden id={id}>{checkWords.label}</span>
        <LongWork
          action={<Button loading={stopping} onClick={onStop} size="sm" variant="outline"><Square aria-hidden="true" size={14} />{checkWords.stop}</Button>}
          announcement={checkWords.running}
          detail={checkWords.term}
          label={checkWords.label}
          title={checkWords.running}
        />
        <Lines groups={checkGroups(check.probes, preset, check.current)} />
      </div>
    );
  }
  if (!report) return null;
  const verdict = verdictWords(report.chat, report.books);
  return (
    <div aria-labelledby={id} className={styles.check} role="group">
      <span hidden id={id}>{checkWords.label}</span>
      {report.stale || changed ? (
        <div className={styles.stale}>
          <p className={styles.note}>{checkWords.stale}</p>
          <Button onClick={onAgain} size="sm" variant="ghost"><RotateCw aria-hidden="true" size={14} />{checkWords.again}</Button>
        </div>
      ) : null}
      <Lines groups={checkGroups(report.probes, preset)} />
      {verdict ? <Alert role="note" tone={verdict.tone}>{verdict.words}</Alert> : null}
    </div>
  );
}
