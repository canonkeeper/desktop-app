import { Library, Plus } from 'lucide-react';
import { Button } from '../controls/Button';
import { EmptyState } from './EmptyState';
import styles from './EmptyState.module.css';

export interface SagaMissingProps {
  /** The sagas of the author, in the order of his menu. */
  sagas: readonly { id: string; title: string }[];
  /** Opens the same section of the saga chosen. */
  onChoose: (projectId: string) => void;
  /** «Создать сагу» of an author who has none left. */
  onCreate?: (() => void) | undefined;
  /** The words of the block under a heading that already says «Сага не найдена». */
  title?: string;
  why?: string;
}

/**
 * A saga the address names and the author does not have (specs/2026-11-11-ux9-shelf-chat.md SC1.9, W-7G): no alarm — the
 * author did nothing wrong. A calm block says why it is not there and offers his own sagas to open, each by its name;
 * without one, «Создать сагу».
 */
export function SagaMissing({
  onChoose, onCreate, sagas, title = 'Сага не найдена', why = 'Её нет среди ваших саг: она удалена, доступ к ней закрыт или ссылка устарела.',
}: SagaMissingProps) {
  const action = sagas.length ? (
    <div className={styles.choice}>
      <p className={styles.why}>Откройте свою сагу:</p>
      <ul aria-label="Ваши саги" className={styles.choiceList}>
        {sagas.map((saga) => (
          <li key={saga.id}><Button onClick={() => onChoose(saga.id)} variant="outline">{saga.title}</Button></li>
        ))}
      </ul>
    </div>
  ) : onCreate ? <Button onClick={onCreate} variant="primary"><Plus aria-hidden="true" size={16} />Создать сагу</Button> : undefined;
  return <EmptyState action={action} aria-label="Сага не найдена" icon={<Library size={28} />} role="region" title={title} why={why} />;
}
