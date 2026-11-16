// SPDX-License-Identifier: AGPL-3.0-only
import { useId, type ReactNode } from 'react';
import { Button } from '../../../ui/controls/Button';
import styles from './SagaFile.module.css';

/**
 * A button of the file with the reason it is closed standing under it, as the board draws it (`Disabled Reason`); the main one
 * of a card is `primary` («Перенести с сайта…», board 44·77).
 */
export function FileButton({ icon, label, loading = false, onClick, reason, variant = 'outline' }: {
  icon: ReactNode; label: string; loading?: boolean; onClick: () => void; reason: string | null; variant?: 'primary' | 'outline';
}) {
  const why = useId();
  return (
    <div className={styles.action}>
      <Button data-voice-consent="no" disabledReason={reason ? { text: reason, at: why } : null} loading={loading} onClick={onClick} variant={variant}>
        {icon}{label}
      </Button>
      {reason ? <p className={styles.why} id={why}>{reason}</p> : null}
    </div>
  );
}
