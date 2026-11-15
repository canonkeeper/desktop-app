// SPDX-License-Identifier: AGPL-3.0-only
import { ChevronDown, ChevronRight } from 'lucide-react';
import { useId } from 'react';
import { Button } from '../../../../ui/controls/Button';
import { Field } from '../../../../ui/controls/Field';
import { Select } from '../../../../ui/controls/Select';
import { SegmentedControl } from '../../../../ui/navigation/SegmentedControl';
import type { AdvancedDraft } from './helper-draft';
import { advancedWords, currencyOptions, fieldWords, helperWords, reasoningOptions, type Currency } from './helper-words';
import styles from './Helper.module.css';

/** The paths of «Дополнительно» in the answers of the server: a refusal of one opens it (HW1.6). */
export const advancedPaths: Record<keyof AdvancedDraft, string> = {
  contextWindow: '/own/advanced/contextWindow',
  maxOutput: '/own/advanced/maxOutput',
  reasoning: '/own/advanced/reasoning',
  parallel: '/own/advanced/parallel',
  priceIn: '/own/advanced/priceInPerMillion',
  priceCache: '/own/advanced/priceCachedInPerMillion',
  priceOut: '/own/advanced/priceOutPerMillion',
  currency: '/own/advanced/currency',
};

export interface AdvancedFieldsProps {
  open: boolean;
  onToggle: () => void;
  values: AdvancedDraft;
  onChange: (field: keyof AdvancedDraft, value: string) => void;
  refused: string | null;
}

/**
 * «Дополнительно» (HW1.5; board 44·25 «„Дополнительно“ раскрыто»): folded until the author opens it; the window of the
 * context, the limit of an answer, the reasoning, the requests at a time, the prices of a million tokens in, cached in
 * (optional) and out, and their currency (specs/2026-11-13-desk-consent-web.md RW3.1) — each says in one line what it is for;
 * an empty price is «не указана».
 */
export function AdvancedFields({ onChange, onToggle, open, refused, values }: AdvancedFieldsProps) {
  const id = useId();
  const reasoningHint = useId();
  const error = (field: keyof AdvancedDraft) => (refused === advancedPaths[field] ? fieldWords[advancedPaths[field]] : undefined);
  const number = (field: 'contextWindow' | 'maxOutput' | 'parallel') => (
    <Field
      autoComplete="off"
      error={error(field)}
      hint={advancedWords[field].hint}
      inputMode="numeric"
      label={advancedWords[field].label}
      onChange={(event) => onChange(field, event.target.value)}
      value={values[field]}
    />
  );
  const price = (field: 'priceIn' | 'priceCache' | 'priceOut') => (
    <Field
      aria-describedby={`${id}-prices`}
      autoComplete="off"
      error={error(field)}
      hint={advancedWords[field].hint || undefined}
      inputMode="decimal"
      label={advancedWords[field].label}
      onChange={(event) => onChange(field, event.target.value)}
      placeholder={advancedWords.priceNone}
      value={values[field]}
    />
  );
  return (
    <div className={styles.advanced}>
      <div>
        <Button aria-controls={open ? id : undefined} aria-expanded={open} onClick={onToggle} size="sm" variant="ghost">
          {open ? <ChevronDown aria-hidden="true" size={14} /> : <ChevronRight aria-hidden="true" size={14} />}{helperWords.advanced}
        </Button>
      </div>
      {open ? (
        <div aria-label={helperWords.advanced} className={styles.advancedFields} id={id} role="group">
          {number('contextWindow')}
          {number('maxOutput')}
          <div className={styles.choice}>
            <span aria-hidden="true" className={styles.choiceLabel}>{advancedWords.reasoning.label}</span>
            <SegmentedControl
              aria-describedby={reasoningHint}
              label={advancedWords.reasoning.label}
              onValueChange={(value) => onChange('reasoning', value)}
              options={reasoningOptions}
              value={values.reasoning}
            />
            <span className={styles.note} id={reasoningHint}>{advancedWords.reasoning.hint}</span>
          </div>
          {number('parallel')}
          <div className={styles.prices}>
            {price('priceIn')}
            {price('priceCache')}
            {price('priceOut')}
            <div className={styles.currency}>
              <Select<Currency>
                label={advancedWords.currency.label}
                onValueChange={(value) => onChange('currency', value)}
                options={currencyOptions}
                value={values.currency}
              />
              {error('currency') ? <span className={styles.error} role="alert">{error('currency')}</span> : null}
            </div>
          </div>
          <p className={styles.note} id={`${id}-prices`}>{advancedWords.pricesHint}</p>
        </div>
      ) : null}
    </div>
  );
}
