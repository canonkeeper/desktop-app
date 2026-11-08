import { Children, type ComponentPropsWithRef, type ReactNode } from 'react';
import styles from './controls.module.css';

export type ButtonVariant = 'primary' | 'secondary' | 'outline' | 'ghost' | 'danger' | 'ink';
export type ControlSize = 'sm' | 'md' | 'touch';

export interface ButtonProps extends ComponentPropsWithRef<'button'> {
  loading?: boolean;
  size?: ControlSize;
  variant?: ButtonVariant;
}

export function Button({
  children,
  className,
  disabled,
  loading = false,
  size = 'md',
  type = 'button',
  variant = 'secondary',
  ...props
}: ButtonProps) {
  return (
    <button
      {...props}
      aria-busy={loading || undefined}
      className={`${styles.button} ${styles[variant]}${className ? ` ${className}` : ''}`}
      data-size={size}
      data-variant={variant}
      disabled={disabled || loading}
      type={type}
    >
      {loading ? <span aria-hidden="true" className={styles.spinner} /> : null}
      {buttonContent(children)}
    </button>
  );
}

export interface ButtonLinkProps extends ComponentPropsWithRef<'a'> {
  size?: ControlSize;
  variant?: ButtonVariant;
}

/** A link that looks like a button: navigation and letters stay real links. */
export function ButtonLink({ children, className, size = 'md', variant = 'secondary', ...props }: ButtonLinkProps) {
  return (
    <a {...props} className={`${styles.button} ${styles[variant]}${className ? ` ${className}` : ''}`} data-size={size} data-variant={variant}>
      {buttonContent(children)}
    </a>
  );
}

function buttonContent(children: ReactNode) {
  const content: ReactNode[] = [];
  for (const child of Children.toArray(children)) {
    const item = typeof child === 'number' ? String(child) : child;
    const previous = content.at(-1);
    if (typeof item === 'string' && typeof previous === 'string') {
      content[content.length - 1] = previous + item;
    } else {
      content.push(item);
    }
  }
  return content.map((child, index) => typeof child === 'string' && child.trim()
    ? <span className={styles.buttonLabel} key={`label-${index}`}>{child}</span>
    : child);
}
