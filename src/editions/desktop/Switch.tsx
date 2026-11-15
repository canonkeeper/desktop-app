// SPDX-License-Identifier: AGPL-3.0-only
import { useId } from 'react';
import styles from './Switch.module.css';

interface SwitchProps {
  checked: boolean;
  /** The words of the switch beside it: its name for a screen reader too. */
  label?: string;
  onChange: (checked: boolean) => void;
  /** A request goes: the switch keeps its place and its focus (`aria-busy`, not `disabled`) and takes no click. */
  busy?: boolean;
  /** The words stand elsewhere, in the item of the window «Добро пожаловать»: the switch is named by them. */
  labelledBy?: string;
  className?: string;
}

/**
 * The switch of the edition (boards 44·1, 44·14, 44·46, `Switch/On`, `Switch/Off`): a button with the role of a switch, named
 * by its words, with a target of 44 px around its track — the daily check of a new version and «Разрешить читать мои саги».
 */
export function Switch({ busy = false, checked, className, label, labelledBy, onChange }: SwitchProps) {
  const own = useId();
  return (
    <div className={className ? `${styles.switchRow} ${className}` : styles.switchRow}>
      <button
        aria-busy={busy || undefined}
        aria-checked={checked}
        aria-labelledby={labelledBy ?? own}
        className={styles.switch}
        onClick={() => {
          if (!busy) onChange(!checked);
        }}
        role="switch"
        type="button"
      >
        <span aria-hidden="true" className={styles.knob} />
      </button>
      {labelledBy ? null : <span className={styles.switchLabel} id={own}>{label}</span>}
    </div>
  );
}
