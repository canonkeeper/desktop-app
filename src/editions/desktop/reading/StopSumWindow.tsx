// SPDX-License-Identifier: AGPL-3.0-only
import { X } from 'lucide-react';
import { useId, useRef, useState, type FormEvent } from 'react';
import { loadFailureWords, publicApiErrorMessage } from '../../../entities/api-errors';
import { Button } from '../../../ui/controls/Button';
import { Checkbox } from '../../../ui/controls/Checkbox';
import { Field } from '../../../ui/controls/Field';
import { IconButton } from '../../../ui/controls/IconButton';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Dialog } from '../../../ui/overlays/Dialog';
import { amountWords, approximately, fieldAmountOf, fieldUnitWords, fieldValueOf, type ReadingUnit } from './amounts';
import { useReadingEstimate, useSetConsent } from './reading-api';
import type { Reading } from './reading-line';
import { consentWords, refusalWords, sumWords } from './reading-words';
import styles from './Reading.module.css';

/** The JSON pointer of the field a refusal of the server names (400 `reading.invalid`). */
export function refusedPath(error: unknown) {
  return (error as { problem?: { errors?: { path?: string }[] } } | null)?.problem?.errors?.[0]?.path ?? null;
}

/**
 * What the field offers first, as board 44·42 draws it at the pause and while the reading goes: the stop of the server and its
 * step after it — a draft the author edits, not a number the page says. No stop, or a stop named in the prices of before, —
 * an empty field.
 */
function firstText(reading: Reading, unit: ReadingUnit) {
  if (reading.pause === 'prices' || reading.stopAt === null) return '';
  return fieldValueOf(reading.stopAt + (reading.step ?? reading.stopAt), unit);
}

export interface StopSumWindowProps {
  projectId: string;
  /** The state of the reading of the status of the server: what was spent, the sum and its unit, the pause. */
  reading: Reading;
  onClose: () => void;
}

/**
 * «Сумма остановки» (specs/2026-11-13-desk-consent-web.md RW1.4; board 44·42): what was spent, the field of the new sum in the
 * unit of the consent with its hint and, under the hint, its refusal (the shared `Field`), and «Без суммы — читать до конца»
 * as a choice of its own. At the pause «сумма» and «цены изменились» saving goes on with the reading; while it goes, it only
 * changes the sum. A sum not over what was spent is refused by the server at the field; the window stays and keeps what was
 * written. After a change of the prices the new sum is in the unit of the settings now, which the estimate of the server says.
 */
export function StopSumWindow({ onClose, projectId, reading }: StopSumWindowProps) {
  const toasts = useToasts();
  const priced = reading.pause === 'prices';
  const estimate = useReadingEstimate(projectId, priced);
  const unit = priced ? estimate.data?.unit : reading.unit;
  const going = reading.state !== 'paused';
  const [text, setText] = useState<string | null>(null);
  const [noSum, setNoSum] = useState(reading.stopAt === null && !priced);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [windowError, setWindowError] = useState<string | null>(null);
  const field = useRef<HTMLInputElement>(null);
  const unitId = useId();
  const save = useSetConsent(projectId);
  const value = text ?? (unit ? firstText(reading, unit) : '');
  const spent = approximately(amountWords(reading.spent, reading.unit), reading.approximate);

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!unit || save.isPending) return;
    setWindowError(null);
    let stopAt: number | null = null;
    if (!noSum) {
      const amount = fieldAmountOf(value, unit);
      if (amount === undefined || amount === null) {
        setFieldError(amount === null ? sumWords.empty : consentWords.notANumber(unit));
        field.current?.focus();
        return;
      }
      stopAt = amount;
    }
    setFieldError(null);
    const label = going ? sumWords.save : sumWords.saveAndGo;
    save.mutate({ change: { state: 'allowed', stopAt }, label }, {
      onSuccess: () => {
        onClose();
        toasts.push({
          message: going ? sumWords.saved : sumWords.continued,
          detail: stopAt === null ? sumWords.goesWithout : sumWords.goes(amountWords(stopAt, unit)),
          tone: 'success',
        });
      },
      onError: (error) => {
        if (refusedPath(error) === '/stopAt') {
          setFieldError(sumWords.small(spent));
          field.current?.focus();
          return;
        }
        setWindowError(publicApiErrorMessage(error, 'Сумма не сохранилась. Попробуйте ещё раз.', { codes: refusalWords }));
      },
    });
  }

  return (
    <Dialog canDismiss={!save.isPending} label={sumWords.title} onDismiss={onClose} width={480}>
      <form className={styles.window} noValidate onSubmit={submit}>
        <div className={styles.head}>
          <h2 data-overlay-heading="">{sumWords.title}</h2>
          <IconButton aria-label="Закрыть" disabled={save.isPending} onClick={onClose} size="sm"><X aria-hidden="true" size={16} /></IconButton>
        </div>
        <p className={styles.message}>{sumWords.spent(spent)}</p>
        {/* The unit of the sum after a change of the prices is the estimate's: when it does not come, the window says so. */}
        {priced && estimate.isError ? (
          <ErrorBlock {...loadFailureWords(estimate.error)} onRetry={() => void estimate.refetch()} retrying={estimate.isRefetching} title={consentWords.loadFailed} />
        ) : null}
        <div className={styles.sum}>
          <Field
            action={unit ? <span className={styles.unit} id={unitId}>{fieldUnitWords(unit)}</span> : undefined}
            aria-describedby={unit ? unitId : undefined}
            autoComplete="off"
            disabled={noSum || !unit}
            error={fieldError}
            hint={consentWords.stopHint}
            inputMode="decimal"
            label={consentWords.stop}
            onChange={(event) => { setText(event.target.value); setFieldError(null); }}
            ref={field}
            value={value}
          />
        </div>
        <Checkbox checked={noSum} label={sumWords.noSum} onChange={(event) => { setNoSum(event.target.checked); setFieldError(null); }} />
        {windowError ? <p className={styles.refusal} role="alert">{windowError}</p> : null}
        <div className={styles.footer}>
          <Button disabled={save.isPending} onClick={onClose} variant="outline">{sumWords.cancel}</Button>
          <Button disabledReason={unit ? null : estimate.isError ? consentWords.loadFailed : sumWords.unitPending} loading={save.isPending} type="submit" variant="primary">
            {going ? sumWords.save : sumWords.saveAndGo}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
