import { Circle, CircleCheck, LoaderCircle } from 'lucide-react';
import type { IndexingStep } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { Progress } from '../../../ui/feedback/Progress';
import {
  activeIndexingStepText,
  indexingStepCompactFacts,
  indexingStepDetail,
  indexingStepLabels,
  indexingStepStatusLabels,
  measuredStepProgress,
  stepTimingLabel,
} from './indexing-card-format';
import styles from './IndexingChecklist.module.css';

export function IndexingChecklist({
  onRetry,
  outcomeVisible,
  retryKind,
  retryPending,
  steps,
}: {
  onRetry: () => void;
  outcomeVisible: boolean;
  retryKind?: IndexingStep['kind'];
  retryPending: boolean;
  steps: IndexingStep[];
}) {
  if (steps.length === 0) return null;
  return (
    <div className={styles.checklist} data-outcome={outcomeVisible}>
      <p className={styles.sectionLabel}>{outcomeVisible ? `Что произошло · ${steps.length} этапа` : <><span className={styles.desktopStatusCopy}>Этапы и фактические счётчики</span><span className={styles.mobileStatusCopy}>{steps.length} этапа · счётчики</span></>}</p>
      <ol aria-label="Этапы подготовки" className={styles.stepList}>
        {steps.map((step) => {
          const label = indexingStepLabels[step.kind];
          const activeText = outcomeVisible ? null : activeIndexingStepText(step);
          const progress = measuredStepProgress(step);
          const progressPercent = progress === null || step.status === 'pending' ? null : Math.round(progress * 100);
          const timing = stepTimingLabel(step);
          const status = activeText?.status ?? `${indexingStepStatusLabels[step.status]}${timing ? ` · ${timing}` : ''}`;
          const compactFacts = indexingStepCompactFacts(step);
          const detail = activeText?.detail ?? indexingStepDetail(step);
          const retry = !outcomeVisible && retryKind === step.kind;
          const mobileStatus = activeText?.mobileMeta ?? (compactFacts && step.status === 'succeeded'
            ? [compactFacts, timing].filter(Boolean).join(' · ')
            : [compactFacts, status].filter(Boolean).join(' · '));
          return (
            <li className={styles.step} data-kind={step.kind} data-status={step.status} key={step.kind}>
              <span aria-hidden="true" className={styles.stepMarker}>
                {!outcomeVisible && step.status === 'running' ? <LoaderCircle className={styles.stepSpinner} /> : null}
                {!outcomeVisible && step.status === 'succeeded' ? <CircleCheck className={styles.tabletCompletedMarker} /> : null}
                {!outcomeVisible && step.status === 'pending' ? <Circle className={styles.tabletPendingMarker} size={18} /> : null}
              </span>
              <span className={styles.stepName}>{label}</span>
              <span className={styles.stepStatus}>{status}</span>
              <span className={styles.stepMobileMeta}>{mobileStatus}</span>
              <span className={styles.stepDetail}>
                {activeText && activeText.tabletDetail !== detail
                  ? <><span className={styles.stepDesktopDetail}>{detail}</span><span className={styles.stepTabletDetail}>{activeText.tabletDetail}</span></>
                  : detail}
              </span>
              {progressPercent === null ? null : (
                <div className={styles.stepProgress}>
                  <Progress aria-label={`Ход этапа «${label}»`} value={progress ?? undefined} />
                  <span>{progressPercent}%</span>
                </div>
              )}
              {retry ? <Button className={styles.retryButton} loading={retryPending} onClick={onRetry}>{retryPending ? 'Запускаем повторно...' : 'Повторить'}</Button> : null}
            </li>
          );
        })}
      </ol>
    </div>
  );
}
