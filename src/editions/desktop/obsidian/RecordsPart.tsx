// SPDX-License-Identifier: AGPL-3.0-only
import { useId, useState } from 'react';
import { authorMaterialKindLabel } from '../../../entities/author-materials/api';
import { Button } from '../../../ui/controls/Button';
import { SearchField } from '../../../ui/controls/SearchField';
import { Badge } from '../../../ui/feedback/Badge';
import { deadlocked, decidedState, decisionOf, type DecidedState, type Decision, type Decisions } from './load';
import type { ObsidianRecordPreview } from './obsidian-api';
import { noteName, recordCountsWords, words } from './words';
import styles from './Obsidian.module.css';

/** Records shown at once and by «Показать ещё»; a search from this many (OW3.3). */
const portion = 50;
const searchFrom = 30;

/** The words of the state of a record (OW3.3). */
function stateWords(record: ObsidianRecordPreview) {
  switch (record.state) {
    case 'new': return words.rNew;
    case 'in_book':
    case 'in_book_by_alias': return record.bookName ? words.rNewIn(record.bookName) : words.rNew;
    case 'same_note': return record.changed ? words.rUpd : words.rSame;
    case 'edited_in_app': return words.rEdited;
    default: return words.rTaken(record.name);
  }
}

function recordWarnings(record: ObsidianRecordPreview) {
  return [
    ...record.warnings.map((warning) => (warning === 'list_note' ? words.rwList : words.rwSections)),
    ...(record.cut > 0 ? [words.rwCut] : []),
    ...(record.left > 0 ? [words.rwProps(record.left)] : []),
  ];
}

/** «Заменить заметкой» · «Не менять» on one choice (OW3.3). */
function DecisionChoice({ label, onChoose, value }: { label: string; onChoose: (decision: Decision) => void; value: Decision }) {
  const name = useId();
  return (
    <fieldset aria-label={label} className={styles.decide}>
      {(['replace', 'keep'] as const).map((decision) => (
        <label key={decision}>
          <input checked={value === decision} name={name} onChange={() => onChoose(decision)} type="radio" value={decision} />
          {decision === 'replace' ? words.replace : words.keep}
        </label>
      ))}
    </fieldset>
  );
}

/**
 * The records of «что получится» (specs/2026-11-15-desk-obsidian-web.md OW3.3; board 44·51, `Obsidian Record Row`): the count by
 * states in one line, a search from 30 records, the list by portions; each record — its name, kind and state in words, what of
 * it does not go in, «Не брать». A decision only where the server takes one: a record edited in the app and a name a record of
 * «Ваших материалов» keeps; «Для всех …» from two such records; a decision of a record is older than the rule.
 */
export function RecordsPart({ decisions, onDecisions, onSkip, records }: {
  decisions: Decisions;
  onDecisions: (decisions: Decisions) => void;
  onSkip: (path: string) => void;
  records: readonly ObsidianRecordPreview[];
}) {
  const [query, setQuery] = useState('');
  const [shown, setShown] = useState(portion);
  const found = query.trim() ? records.filter((record) => record.name.toLowerCase().includes(query.trim().toLowerCase())) : records;
  const counts = {
    created: records.filter((record) => record.state === 'new' || record.state === 'in_book' || record.state === 'in_book_by_alias').length,
    updated: records.filter((record) => record.state === 'same_note' && record.changed).length,
    same: records.filter((record) => record.state === 'same_note' && !record.changed).length,
    edited: records.filter((record) => record.state === 'edited_in_app').length,
    taken: records.filter((record) => record.state === 'other_source').length,
  };
  const ofState = (state: DecidedState) => records.filter((record) => decidedState(record) === state).length;
  // A rule a state; the decision of a record stays older than it (OW3.3).
  const rule = (state: DecidedState, decision: Decision) => onDecisions({ ...decisions, rule: { ...decisions.rule, [state]: decision } });
  return (
    <div className={styles.part}>
      <p className={styles.muted}>{recordCountsWords(counts)}</p>
      {records.length >= searchFrom ? (
        <SearchField label={words.find} labelHidden onChange={(event) => { setQuery(event.target.value); setShown(portion); }} onClear={() => setQuery('')}
          placeholder={words.find} value={query} />
      ) : null}
      {(['edited_in_app', 'other_source'] as const).map((state) => {
        const count = ofState(state);
        if (count < 2) return null;
        const label = state === 'edited_in_app' ? words.forEdited(count) : words.forTaken(count);
        return (
          <div className={styles.forAll} key={state}>
            <span>{label}</span>
            {(['replace', 'keep'] as const).map((decision) => (
              <Button aria-pressed={(decisions.rule[state] ?? 'keep') === decision} key={decision} onClick={() => rule(state, decision)} size="sm" variant="ghost">
                {decision === 'replace' ? words.replace : words.keep}
              </Button>
            ))}
          </div>
        );
      })}
      <ul aria-label={words.records} className={styles.records}>
        {found.slice(0, shown).map((record) => {
          const other = deadlocked(record, records);
          const held = record.heldBy && !other ? words.rHeld(record.name, noteName(record.heldBy)) : null;
          const deadlock = other ? words.deadlock(noteName(record.path), noteName(other.path)) : null;
          return (
            <li className={styles.record} key={record.path}>
              <div className={styles.recordLine}>
                <strong>{record.name}</strong>
                <Badge tone="neutral">{authorMaterialKindLabel(record.kind)}</Badge>
                <span className={held || deadlock ? styles.warning : styles.muted}>{deadlock ?? held ?? stateWords(record)}</span>
                <Button aria-label={`${words.skip} «${record.name}»`} onClick={() => onSkip(record.path)} size="sm" variant="ghost">{words.skip}</Button>
              </div>
              {decidedState(record) ? (
                <DecisionChoice label={record.name} onChoose={(decision) => onDecisions({ ...decisions, byPath: { ...decisions.byPath, [record.path]: decision } })}
                  value={decisionOf(record, decisions)} />
              ) : null}
              {recordWarnings(record).map((warning) => <p className={styles.warning} key={warning}>{warning}</p>)}
            </li>
          );
        })}
      </ul>
      {found.length > shown ? <Button onClick={() => setShown(shown + portion)} size="sm" variant="ghost">{words.more}</Button> : null}
    </div>
  );
}
