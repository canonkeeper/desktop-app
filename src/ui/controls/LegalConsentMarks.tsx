import { useId } from 'react';
import { fromRegistration, legalDocumentPaths } from '../../shared/navigation/legal-paths';
import { Checkbox } from './Checkbox';
import styles from './LegalConsentMarks.module.css';

interface LegalConsentMarksProps {
  acceptedTerms: boolean;
  consented: boolean;
  disabled?: boolean;
  onAcceptedTermsChange: (value: boolean) => void;
  onConsentedChange: (value: boolean) => void;
  /** Why the form waits for the marks (specs/2026-11-10-ux3-auth.md AU3.1): it stands under them and they point to it. */
  reason?: string;
  /** The marks of the registration form: a document opened from them offers the way back (specs/2026-11-11-ux9-account.md AC3.5). */
  registration?: boolean;
}

const external = { rel: 'noopener', target: '_blank' } as const;

/**
 * The two separate marks with links to the texts (legal R2.1). A mark and its first word are one target of a row of
 * 44 px, the link of the document is another, so pressing the link never changes the mark (specs/2026-11-10-ux3-auth.md
 * AU3.2); the mark is still named by its whole sentence.
 */
export function LegalConsentMarks({ acceptedTerms, consented, disabled, onAcceptedTermsChange, onConsentedChange, reason, registration = false }: LegalConsentMarksProps) {
  const id = useId();
  const of = (path: string) => (registration ? fromRegistration(path) : path);
  const reasonId = reason ? `${id}-reason` : undefined;
  const mark = (checked: boolean, lead: string, link: string, href: string, name: string, onChange: (value: boolean) => void) => (
    <div className={styles.row}>
      <Checkbox
        aria-describedby={reasonId}
        aria-invalid={reason && !checked ? true : undefined}
        aria-label={`${lead} ${link}`}
        checked={checked}
        disabled={disabled}
        label={lead}
        name={name}
        onChange={(event) => onChange(event.target.checked)}
      />
      <a className={styles.link} href={href} {...external}>{link}</a>
    </div>
  );
  return (
    <div className={styles.marks}>
      {mark(acceptedTerms, 'Принимаю', 'пользовательское соглашение', of(legalDocumentPaths.terms), 'acceptedTerms', onAcceptedTermsChange)}
      {mark(consented, 'Даю', 'согласие на обработку персональных данных', of(legalDocumentPaths.consent), 'acceptedPersonalDataConsent', onConsentedChange)}
      {reason ? <p className={styles.reason} id={reasonId} role="alert">{reason}</p> : null}
    </div>
  );
}

/** How the data is handled — smaller, under the button of the form, and nothing like a mark (AU3.2). */
export function LegalPolicyLine({ registration = false }: { registration?: boolean }) {
  return (
    <p className={styles.policy}>
      Как мы обращаемся с данными — в <a href={registration ? fromRegistration(legalDocumentPaths.privacy) : legalDocumentPaths.privacy} {...external}>политике обработки персональных данных</a>.
    </p>
  );
}
