import { Children, type ComponentPropsWithRef, type ReactNode } from 'react';
import { useDisabledReason, type DisabledReasonValue } from './DisabledReason';
import styles from './controls.module.css';

export type ButtonVariant = 'primary' | 'secondary' | 'outline' | 'ghost' | 'danger' | 'ink';
export type ControlSize = 'sm' | 'md' | 'touch';

export interface ButtonProps extends ComponentPropsWithRef<'button'> {
  /**
   * Why the button is closed now, with the way out (specs/2026-11-10-ux2-patterns-refusals.md PR1.1): it stays in its
   * place, dimmed, takes the press and the focus and says the reason beside itself; its action does not run.
   */
  disabledReason?: DisabledReasonValue;
  /** A press of the closed button: the page may answer it too — open the tab the reason names, light its bar. */
  onRefusedPress?: () => void;
  /**
   * The word under the icon instead of beside it, where a row has no room for both (`Button/Ghost Caption`, PR1.4): a
   * button of an icon alone is only ×, ←, ⋯.
   */
  caption?: boolean;
  loading?: boolean;
  /**
   * The size on a wide screen; below 900 px and under a finger every button is a target of 44 px whatever the page
   * chose (PR1.4, П-16).
   */
  size?: ControlSize;
  variant?: ButtonVariant;
}

export function Button({
  caption = false,
  children,
  className,
  disabled,
  disabledReason,
  loading = false,
  onBlur,
  onClick,
  onFocus,
  onKeyDown,
  onRefusedPress,
  ref,
  size = 'md',
  type = 'button',
  variant = 'secondary',
  ...props
}: ButtonProps) {
  const reason = useDisabledReason(disabledReason, onRefusedPress, { 'aria-describedby': props['aria-describedby'], onBlur, onClick, onFocus, onKeyDown }, ref);
  return (
    <>
      <button
        {...props}
        {...reason.props}
        aria-busy={loading || undefined}
        className={`${styles.button} ${styles[variant]}${className ? ` ${className}` : ''}`}
        data-caption={caption || undefined}
        data-size={size}
        data-variant={variant}
        disabled={disabledReason ? loading : disabled || loading}
        type={type}
      >
        {loading ? <span aria-hidden="true" className={styles.spinner} /> : null}
        {buttonContent(children)}
      </button>
      {reason.plate}
    </>
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
