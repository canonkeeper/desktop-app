import type { AnchorHTMLAttributes, ComponentPropsWithRef } from 'react';
import type { ControlSize } from './Button';
import styles from './controls.module.css';

export interface IconButtonProps extends Omit<ComponentPropsWithRef<'button'>, 'aria-label'> {
  'aria-label': string;
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

export function IconButton({ className, children, disabled, loading = false, variant = 'default', size = 'md', type = 'button', ...props }: IconButtonProps) {
  return (
    <button
      {...props}
      className={`${styles.iconButton}${className ? ` ${className}` : ''}`}
      data-size={size}
      data-variant={variant}
      aria-busy={loading || undefined}
      disabled={disabled || loading}
      type={type}
    >{loading ? <span aria-hidden="true" className={styles.spinner} /> : children}</button>
  );
}
