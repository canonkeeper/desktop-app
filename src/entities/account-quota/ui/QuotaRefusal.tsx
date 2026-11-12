import { Gauge } from 'lucide-react';
import { useEffect, useRef, type ReactNode } from 'react';
import type { ReturnPlace } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { WriteToUs } from '../../../ui/feedback/SupportReference';
import { useQuotaNote } from '../quota-note';
import type { RefusalWords } from '../refusal-words';
import styles from './quota-refusal.module.css';

interface QuotaRefusalProps {
  /** The code of the refusal: the build names what the next tier gives by it. */
  code: string;
  words: RefusalWords;
  /** Where the author met the refusal: «Тариф» leads back here (PR2.4). */
  place?: ReturnPlace | undefined;
  /**
   * «Тариф» of a place that lives over the pages — the call of the voice opens the page of the tier in its own way: the
   * block shows this button instead of the build's.
   */
  onTariff?: (() => void) | undefined;
  /** The server has just refused: the block is announced. Standing in advance, it is a quiet note. */
  announce?: boolean;
  /** What became of what the author typed, in the place's own words: «Ваш вопрос остался в поле». */
  children?: ReactNode;
  /** The ways on of the place: «Только описание», «Нарисовать без помощника». */
  actions?: ReactNode;
  id?: string | undefined;
  /** The name of the block where the place has named it already: «Почему сейчас нельзя рисовать». By default — what ended. */
  label?: string | undefined;
  /** Each refused press of a closed button lights the block anew. */
  pressed?: number;
  className?: string | undefined;
}

/**
 * The one block of a refusal by a limit in every section (specs/2026-11-10-ux2-patterns-refusals.md PR2.1, PR2.2; board
 * 15·03): what ended · when it comes back · what the next tier gives, with «Тариф» and «Написать нам». It stands where the
 * author met the limit and counts nothing itself: the words come from one table, the numbers from the counter beside it.
 */
export function QuotaRefusal({ actions, announce = false, children, className, code, id, label, onTariff, place, pressed = 0, words }: QuotaRefusalProps) {
  const note = useQuotaNote()(code, place);
  const block = useRef<HTMLElement>(null);
  useEffect(() => {
    if (pressed > 0) block.current?.scrollIntoView?.({ block: 'nearest' });
  }, [pressed]);
  return (
    <section aria-label={label ?? words.ended} className={`${styles.block}${className ? ` ${className}` : ''}`} data-pressed={pressed > 0 || undefined}
      id={id} ref={block} role={announce ? 'alert' : 'note'}>
      {pressed > 0 ? <span aria-hidden="true" className={styles.flash} key={pressed} /> : null}
      <strong className={styles.ended}>{words.ended}</strong>
      <p className={styles.returns}>{words.returns}</p>
      {children}
      {note?.offer}
      <div className={styles.actions}>
        {actions}
        {onTariff ? <Button onClick={onTariff} size="sm" variant="outline"><Gauge aria-hidden="true" size={14} />Тариф</Button> : note?.tariff}
        {/* «Написать нам» of the contact slice: the window with the topic of the tier and what ended. A refusal that passes by
            itself has none: there is nothing to write about. */}
        {words.passes ? null : <WriteToUs draft={`${words.ended}.`} topic="tariff" />}
      </div>
      {pressed > 0 ? <span className={styles.spoken} role="status"><span key={pressed}>{`${words.ended}. ${words.returns}`}</span></span> : null}
    </section>
  );
}
