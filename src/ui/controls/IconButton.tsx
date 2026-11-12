import type { AnchorHTMLAttributes, ComponentPropsWithRef } from 'react';
import type { ControlSize } from './Button';
import { useDisabledReason, type DisabledReasonValue } from './DisabledReason';
import styles from './controls.module.css';

/**
 * A button of one sign: only the signs everyone knows — ×, ←, ⋯, the player and the search of a field
 * (specs/2026-11-10-ux2-patterns-refusals.md PR1.4, П-15). Any other action is a `Button` with its word.
 */
export interface IconButtonProps extends Omit<ComponentPropsWithRef<'button'>, 'aria-label'> {
  'aria-label': string;
  /** Why it is closed now: it stays in place and says it (PR1.1). */
  disabledReason?: DisabledReasonValue;
  onRefusedPress?: () => void;
  size?: ControlSize;
  variant?: 'default' | 'primary';
  loading?: boolean;
}

export function IconLink({ className, size = 'md', ...props }: AnchorHTMLAttributes<HTMLAnchorElement> & {
  'aria-label': string;
  size?: ControlSize;
}) {
  return <a {...props} className={`${styles.iconButton}${className ? ` ${className}` : ''}`} data-size={size} data-variant="default" />;
}

export function IconButton({
  className, children, disabled, disabledReason, loading = false, onBlur, onClick, onFocus, onKeyDown, onRefusedPress, ref,
  variant = 'default', size = 'md', type = 'button', ...props
}: IconButtonProps) {
  const reason = useDisabledReason(disabledReason, onRefusedPress, { 'aria-describedby': props['aria-describedby'], onBlur, onClick, onFocus, onKeyDown }, ref);
  return (
    <>
      <button
        {...props}
        {...reason.props}
        className={`${styles.iconButton}${className ? ` ${className}` : ''}`}
        data-size={size}
        data-variant={variant}
        aria-busy={loading || undefined}
        disabled={disabledReason ? loading : disabled || loading}
        type={type}
      >{loading ? <span aria-hidden="true" className={styles.spinner} /> : children}</button>
      {reason.plate}
    </>
  );
}
