import { supportMailHref } from '../../shared/support/support-contact';
import styles from './feedback.module.css';

interface SupportMailLinkProps {
  className?: string;
  code?: string | null;
}

/** «Написать в поддержку»: a letter with the code and the current page path (R4.2.1, R4.2.2). */
export function SupportMailLink({ className, code }: SupportMailLinkProps) {
  return (
    <a
      className={`${styles.supportLink}${className ? ` ${className}` : ''}`}
      href={supportMailHref(code, typeof window === 'undefined' ? '/' : window.location.pathname)}
    >
      Написать в поддержку
    </a>
  );
}

/** «Код для поддержки: …» (selectable) and «Написать в поддержку» under an error (R4.2.1). */
export function SupportReference({ className, code }: SupportMailLinkProps) {
  const supportCode = code?.trim();
  return (
    <span className={`${styles.supportReference}${className ? ` ${className}` : ''}`}>
      {supportCode ? <span className={styles.supportCode}>Код для поддержки: <code>{supportCode}</code></span> : null}
      <SupportMailLink code={supportCode} />
    </span>
  );
}
