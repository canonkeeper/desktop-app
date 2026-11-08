import { legalDocumentPaths } from '../../shared/navigation/legal-paths';
import { Checkbox } from './Checkbox';
import styles from './LegalConsentMarks.module.css';

interface LegalConsentMarksProps {
  acceptedTerms: boolean;
  consented: boolean;
  disabled?: boolean;
  onAcceptedTermsChange: (value: boolean) => void;
  onConsentedChange: (value: boolean) => void;
}

const external = { rel: 'noopener', target: '_blank' } as const;

/** The two separate marks with links to the texts (legal R2.1). */
export function LegalConsentMarks({ acceptedTerms, consented, disabled, onAcceptedTermsChange, onConsentedChange }: LegalConsentMarksProps) {
  return (
    <div className={styles.marks}>
      <Checkbox
        checked={acceptedTerms}
        disabled={disabled}
        label={<>Принимаю <a href={legalDocumentPaths.terms} {...external}>пользовательское соглашение</a></>}
        onChange={(event) => onAcceptedTermsChange(event.target.checked)}
      />
      <Checkbox
        checked={consented}
        disabled={disabled}
        label={<>Даю <a href={legalDocumentPaths.consent} {...external}>согласие на обработку персональных данных</a></>}
        onChange={(event) => onConsentedChange(event.target.checked)}
      />
      <p className={styles.policy}>
        Как мы обращаемся с данными — в <a href={legalDocumentPaths.privacy} {...external}>политике обработки персональных данных</a>.
      </p>
    </div>
  );
}
