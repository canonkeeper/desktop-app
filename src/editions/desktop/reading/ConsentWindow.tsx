// SPDX-License-Identifier: AGPL-3.0-only
import { Settings, Square, X } from 'lucide-react';
import { useId, useRef, useState, type FormEvent } from 'react';
import { loadFailureWords, publicApiErrorMessage } from '../../../entities/api-errors';
import { createIdempotencyKey } from '../../../shared/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { Field } from '../../../ui/controls/Field';
import { IconButton } from '../../../ui/controls/IconButton';
import { LinkAction } from '../../../ui/controls/LinkAction';
import { ErrorBlock } from '../../../ui/feedback/ErrorBlock';
import { LongWork } from '../../../ui/feedback/LongWork';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Confirm } from '../../../ui/overlays/Confirm';
import { Dialog } from '../../../ui/overlays/Dialog';
import { helperPartKey } from '../settings/part-props';
import { amountWords, fieldAmountOf, fieldUnitWords, fieldValueOf } from './amounts';
import {
  useCancelSample, useReadingEstimate, useReadingSample, useSetConsent, useStartSample, type ReadingEstimate, type ReadingSample,
} from './reading-api';
import {
  consentMessage, consentWords, estimateLine, noSumMessage, refusalWords, sampleFailureLine, sampleFailures, sampleGoingLine, sampleResult,
} from './reading-words';
import { refusedPath } from './StopSumWindow';
import styles from './Reading.module.css';

export interface ConsentWindowProps {
  projectId: string;
  navigate: RouteNavigate;
  onClose: () => void;
}

const live = (sample: ReadingSample | undefined) => sample?.state === 'queued' || sample?.state === 'running';

/**
 * «Прочитать книги своим ИИ?» (specs/2026-11-13-desk-consent-web.md RW2; board 44·27): what the helper does and where the text
 * of the books goes, the estimate of the server — tokens, the sum with prices, the time, whether it is rough — the field of the
 * stop sum with the sum the server offers, and the sample of three chapters: its work with «Остановить», its chapters and the
 * estimate after it, its reason when it did not read. «Прочитать всё» and «Прочитать остальное» give the consent with the sum
 * of the field — an empty one after a question — and close the window once the server says the reading goes. The window may
 * be closed while a sample runs: the sample goes on, and the window opened again shows how far.
 */
export function ConsentWindow({ navigate, onClose, projectId }: ConsentWindowProps) {
  const estimate = useReadingEstimate(projectId, true);
  const sample = useReadingSample(projectId, true);
  // A write of the window runs: the window does not close under it.
  const [busy, setBusy] = useState(false);
  return (
    <Dialog canDismiss={!busy} label={consentWords.title} onDismiss={onClose} width={720}>
      <div className={styles.window}>
        <div className={styles.head}>
          <h2 data-overlay-heading="">{consentWords.title}</h2>
          <IconButton aria-label={consentWords.close} disabled={busy} onClick={onClose} size="sm"><X aria-hidden="true" size={16} /></IconButton>
        </div>
        {estimate.isPending ? <SkeletonArea count={4} shape="lines" /> : null}
        {estimate.isError ? (
          <ErrorBlock {...loadFailureWords(estimate.error)} onRetry={() => void estimate.refetch()} retrying={estimate.isRefetching} title={consentWords.loadFailed} />
        ) : null}
        {estimate.data ? (
          <Consent estimate={estimate.data} navigate={navigate} onBusy={setBusy} onClose={onClose} projectId={projectId} sample={sample.data} />
        ) : null}
      </div>
    </Dialog>
  );
}

function Consent({ estimate, navigate, onBusy, onClose, projectId, sample }: {
  estimate: ReadingEstimate;
  sample: ReadingSample | undefined;
  navigate: RouteNavigate;
  projectId: string;
  onBusy: (busy: boolean) => void;
  onClose: () => void;
}) {
  const toasts = useToasts();
  const consent = useSetConsent(projectId);
  const start = useStartSample(projectId);
  const cancel = useCancelSample(projectId);
  // The form of the window: what the author wrote in the field (null — the sum the server offers), and its refusals.
  const [text, setText] = useState<string | null>(null);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [windowError, setWindowError] = useState<string | null>(null);
  const [askNoSum, setAskNoSum] = useState(false);
  const field = useRef<HTMLInputElement>(null);
  const unitId = useId();
  const unit = estimate.unit;
  const value = text ?? fieldValueOf(estimate.suggestedStop, unit);
  const going = live(sample);
  const sampled = sample?.state === 'succeeded';
  const namesOnly = estimate.chapters === 0 && (estimate.namesWait || estimate.threadsWait);
  const nothing = estimate.chapters === 0 && !namesOnly;
  const readLabel = namesOnly ? consentWords.namesOnly(estimate.namesWait, estimate.threadsWait) : sampled ? consentWords.readRest : consentWords.readAll;
  const openSettings = () => {
    onClose();
    navigate('/settings', { part: helperPartKey });
  };

  function give(stopAt: number | null) {
    setAskNoSum(false);
    setFieldError(null);
    setWindowError(null);
    onBusy(true);
    consent.mutate({ change: { state: 'allowed', stopAt }, label: readLabel }, {
      onSettled: () => onBusy(false),
      onSuccess: () => {
        onClose();
        toasts.push({
          message: consentWords.started,
          detail: stopAt === null ? consentWords.startedWithout : consentWords.startedWith(amountWords(stopAt, unit)),
          tone: 'success',
        });
      },
      onError: (error) => {
        if (refusedPath(error) === '/stopAt') {
          setFieldError(consentWords.notANumber(unit));
          field.current?.focus();
          return;
        }
        setWindowError(publicApiErrorMessage(error, 'Чтение не началось. Попробуйте ещё раз.', { codes: refusalWords }));
      },
    });
  }

  function read() {
    if (consent.isPending) return;
    const amount = fieldAmountOf(value, unit);
    if (amount === undefined) {
      setFieldError(consentWords.notANumber(unit));
      field.current?.focus();
      return;
    }
    // An empty field is a consent without a stop: it covers the books added later too — the author says «да» to it (RW2.3).
    if (amount === null) setAskNoSum(true);
    else give(amount);
  }

  function tryIt() {
    setWindowError(null);
    start.mutate(createIdempotencyKey('startReadingSample'), {
      onError: (error) => setWindowError(publicApiErrorMessage(error, 'Проба не началась. Попробуйте ещё раз.', { codes: refusalWords })),
    });
  }

  const failure = sample?.state === 'failed' ? sample.failure : null;
  const canSample = estimate.sampleChapters > 0 && !sampled;
  // The main button is the last one: the sample while one is possible and none was made, then the reading. It is the one
  // submit button of the form, so Enter in the field presses it — never the paid reading of all while the sample is the main.
  const readMain = !canSample || sample?.state === 'canceled';
  const sampleButton = canSample
    ? <Button loading={start.isPending} onClick={readMain ? tryIt : undefined} type={readMain ? 'button' : 'submit'} variant={readMain ? 'outline' : 'primary'}>
        {sample?.state === 'failed' || sample?.state === 'canceled' ? consentWords.again : consentWords.sample(estimate.sampleChapters)}
      </Button>
    : null;

  function submit(event: FormEvent) {
    event.preventDefault();
    // While the sample runs nothing is pressed; then the main button: the sample, or the reading.
    if (going) return;
    if (readMain) read();
    else tryIt();
  }

  const estimateBlock = nothing ? null : (
    <div className={styles.estimate}>
      <p className={styles.line}>{estimateLine(estimate, sampled)}</p>
      {estimate.kind === 'rough' && estimate.sampleChapters > 0 && !sampled ? <p className={styles.note}>{consentWords.rough(estimate.sampleChapters)}</p> : null}
      {unit === 'tokens' && !estimate.local ? (
        <LinkAction className={styles.link} onClick={openSettings}>{consentWords.noPrices}</LinkAction>
      ) : null}
    </div>
  );

  return (
    <form className={styles.consent} noValidate onSubmit={submit}>
      <p className={styles.message}>{consentMessage(estimate)}</p>
      {failure !== null ? (
        <div className={styles.failure} role="alert">
          <p className={styles.failureLine}>{sampleFailureLine(failure)}</p>
          {sampleFailures[failure]?.settings ? (
            <Button onClick={openSettings} size="sm" variant="outline"><Settings aria-hidden="true" size={14} />{consentWords.settings}</Button>
          ) : null}
        </div>
      ) : null}
      {sampled ? null : estimateBlock}
      {going && sample ? (
        <LongWork
          action={<Button loading={cancel.isPending} onClick={() => cancel.mutate()} size="sm" variant="outline"><Square aria-hidden="true" size={14} />{consentWords.stopSample}</Button>}
          announcement={sampleGoingLine(sample)}
          detail={consentWords.sampleLeave}
          label="Проба чтения"
          progress={sample.total > 0 ? sample.done / sample.total : null}
          title={sampleGoingLine(sample)}
        />
      ) : null}
      {sample && (sampled || sample.state === 'canceled') ? <SampleChapters sample={sample} /> : null}
      {/* Board 44·27 «Проба готова»: the estimate after the sample stands under its chapters. */}
      {sampled ? estimateBlock : null}
      {nothing ? null : (
        <div className={styles.sum}>
          <Field
            action={<span className={styles.unit} id={unitId}>{fieldUnitWords(unit)}</span>}
            aria-describedby={unitId}
            autoComplete="off"
            disabled={consent.isPending}
            error={fieldError}
            hint={consentWords.stopHint}
            inputMode="decimal"
            label={consentWords.stop}
            onChange={(event) => { setText(event.target.value); setFieldError(null); }}
            ref={field}
            value={value}
          />
        </div>
      )}
      {windowError ? <p className={styles.refusal} role="alert">{windowError}</p> : null}
      {going ? null : (
        <div className={styles.footer}>
          <Button disabled={consent.isPending} onClick={onClose} variant="outline">{sampled ? consentWords.notRead : nothing ? consentWords.close : consentWords.notNow}</Button>
          {nothing ? null : (
            <>
              {readMain ? sampleButton : null}
              <Button loading={consent.isPending} onClick={readMain ? undefined : read} type={readMain ? 'submit' : 'button'} variant={readMain ? 'primary' : 'secondary'}>{readLabel}</Button>
              {readMain ? null : sampleButton}
            </>
          )}
        </div>
      )}
      {askNoSum ? (
        <Confirm
          action={consentWords.noSumAction}
          busy={consent.isPending}
          onCancel={() => { setAskNoSum(false); field.current?.focus(); }}
          onConfirm={() => give(null)}
          title={consentWords.noSumTitle}
        >
          {noSumMessage(estimate)}
        </Confirm>
      ) : null}
    </form>
  );
}

/** The chapters a sample read, as the map shows a chapter: its place, the retelling, the events, who is in it (RW2.2). */
function SampleChapters({ sample }: { sample: ReadingSample }) {
  return (
    <section aria-label={sampleResult(sample)} className={styles.sample}>
      <p className={styles.result}>{sampleResult(sample)}</p>
      {sample.chapters.length > 0 ? (
        <ul className={styles.cards}>
          {sample.chapters.map((chapter) => (
            <li className={styles.card} key={chapter.chapterId}>
              <h3 className={styles.place}>{chapter.place}</h3>
              <dl className={styles.facts}>
                <dt>{consentWords.summary}</dt><dd>{chapter.summary}</dd>
                {chapter.events.length > 0 ? <><dt>{consentWords.events}</dt><dd>{chapter.events.join(' · ')}</dd></> : null}
                {chapter.people.length > 0 ? <><dt>{consentWords.people}</dt><dd>{chapter.people.join(', ')}</dd></> : null}
              </dl>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}
