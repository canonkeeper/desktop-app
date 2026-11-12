import { Trash2, X } from 'lucide-react';
import { useState } from 'react';
import { Button } from '../controls/Button';
import { Field } from '../controls/Field';
import { IconButton } from '../controls/IconButton';
import { Badge } from '../feedback/Badge';
import { deletionLines } from '../overlays/Confirm';
import { Dialog } from '../overlays/Dialog';
import styles from './SagaSettings.module.css';

export type SagaRole = 'owner' | 'admin' | 'editor' | 'viewer';

const roleWords: Record<SagaRole, string> = { owner: 'владелец', admin: 'администратор', editor: 'редактор', viewer: 'читатель' };

/** Who may change the saga, said to the one who may not (S3.2, П-22). */
const changeWords: Record<'editor' | 'viewer', string> = {
  editor: 'Менять имя саги и удалить её может владелец или администратор.',
  viewer: 'Менять имя, книги и главы может владелец.',
};

export interface SagaSettingsProps {
  /**
   * The author is alone in his saga (specs/2026-11-11-ux9-shelf-chat.md SC1.1): no role is said to him; undefined while
   * the members are read. An editor and a reader share the saga by being in it.
   */
  alone?: boolean | undefined;
  name: string;
  role: SagaRole;
  saving: boolean;
  /** Why the name was not given. */
  error: string | null;
  onSave: (name: string) => void;
  /** «Удалить сагу»: the window of the deletion (specs/2026-11-10-ux1-shelf.md UXS4.3). */
  onDelete: () => void;
  onClose: () => void;
}

/**
 * «Настроить сагу» (specs/2026-11-10-ux2-shell.md S3.2; `Pattern/Shell/Saga Settings`, board 14·11): the name of the saga,
 * the role of the author in a saga he shares (specs/2026-11-11-ux9-shelf-chat.md SC1.1), «Удалить сагу» last and apart. To a reader or an editor the window says the role and who changes
 * the saga, without actions.
 */
export function SagaSettings({ alone, error, name, onClose, onDelete, onSave, role, saving }: SagaSettingsProps) {
  const [draft, setDraft] = useState(name);
  const manager = role === 'owner' || role === 'admin';
  // The role is a word of a saga shared with others: an author alone hears none (SC1.1).
  const roleShown = !manager || alone === false;
  return (
    <Dialog label="Настроить сагу" onDismiss={onClose} width={460}>
      <form className={styles.settings} onSubmit={(event) => {
        event.preventDefault();
        if (manager) onSave(draft);
      }}>
        <header className={styles.head}>
          <h2>Настроить сагу</h2>
          <IconButton aria-label="Закрыть" onClick={onClose}><X aria-hidden="true" size={18} /></IconButton>
        </header>
        {manager ? (
          <Field autoComplete="off" error={error ?? undefined} label="Имя саги" onChange={(event) => setDraft(event.target.value)} value={draft} />
        ) : null}
        {roleShown ? <p className={styles.role}><span>Ваша роль</span><Badge>{roleWords[role]}</Badge></p> : null}
        {manager ? (
          <div className={styles.danger}>
            <Button className={styles.delete} onClick={onDelete} size="touch" variant="ghost">
              <Trash2 aria-hidden="true" size={16} />Удалить сагу…
            </Button>
            <p>{deletionLines.returns}</p>
          </div>
        ) : <p className={styles.note}>{changeWords[role === 'editor' ? 'editor' : 'viewer']}</p>}
        <footer className={styles.actions}>
          <Button onClick={onClose} variant="outline">Закрыть</Button>
          {manager ? <Button loading={saving} type="submit" variant="primary">Сохранить</Button> : null}
        </footer>
      </form>
    </Dialog>
  );
}
