import { Trash2, X } from 'lucide-react';
import type { Book, Project } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { Dialog } from '../../../ui/overlays/Dialog';
import { Overlay } from '../../../ui/overlays/Overlay';
import { ProjectSidebar, type ProjectSidebarProps } from '../../../ui/ProjectSidebar';
import { Portal } from '../../../ui/overlays/Portal';
import type { PresenceStatus } from '../../../ui/overlays/use-presence';
import styles from './ShelfLayers.module.css';

export function ConfirmDelete({
  book,
  isDeleting,
  onClose,
  onDelete,
  state,
}: {
  book: Book;
  isDeleting: boolean;
  onClose: () => void;
  onDelete: () => void;
  state: PresenceStatus;
}) {
  return (
    <Dialog surface="content" label="Удалить книгу" onDismiss={isDeleting ? () => undefined : onClose} scrimClassName={styles.deleteDialogScrim} state={state}>
      <section className={styles.modal}>
        <div aria-hidden="true" className={styles.sheetHandle} />
        <div className={styles.modalHeader}>
          <h2>Удалить книгу?</h2>
        </div>
        <div className={styles.modalBody}>
          <p className={styles.modalCopy}>«{book.title}» уйдёт в «Недавно удалённые»: её можно вернуть в течение 7 дней.</p>
          <p className={styles.modalCopy}>Чаты и заметки останутся. Чтобы заменить текст книги новым файлом, выберите в меню книги «Обновить из файла».</p>
        </div>
        <div className={styles.modalActions}>
          <Button className={styles.cancelAction} disabled={isDeleting} onClick={onClose}>
            Отмена
          </Button>
          <Button className={styles.deleteAction} loading={isDeleting} onClick={onDelete} variant="danger">
            <Trash2 aria-hidden="true" className={styles.mobileOnly} size={16} />
            {isDeleting ? 'Удаляем...' : 'Удалить'}
          </Button>
        </div>
        <IconButton aria-label="Закрыть" className={styles.desktopClose} disabled={isDeleting} onClick={onClose}>
          <X aria-hidden="true" size={18} />
        </IconButton>
      </section>
    </Dialog>
  );
}

export function ConfirmProjectDelete({
  isDeleting,
  onClose,
  onDelete,
  project,
  state,
}: {
  isDeleting: boolean;
  onClose: () => void;
  onDelete: () => void;
  project: Project;
  state: PresenceStatus;
}) {
  return (
    <Dialog surface="content" label="Удалить сагу" onDismiss={isDeleting ? () => undefined : onClose} scrimClassName={styles.deleteDialogScrim} state={state}>
      <section className={styles.modal}>
        <div aria-hidden="true" className={styles.sheetHandle} />
        <div className={styles.modalHeader}>
          <h2>Удалить сагу?</h2>
        </div>
        <div className={styles.modalBody}>
          <p className={styles.modalCopy}>Сага «{project.title}» и все её книги будут перемещены в «Недавно удалённые», откуда сагу можно восстановить.</p>
          <p className={styles.modalCopy}>Чаты этой саги будут удалены вместе с ней.</p>
        </div>
        <div className={styles.modalActions}>
          <Button className={styles.cancelAction} disabled={isDeleting} onClick={onClose}>
            Отмена
          </Button>
          <Button className={styles.deleteAction} loading={isDeleting} onClick={onDelete} variant="danger">
            <Trash2 aria-hidden="true" className={styles.mobileOnly} size={16} />
            {isDeleting ? 'Удаляем...' : 'Удалить'}
          </Button>
        </div>
        <IconButton aria-label="Закрыть" className={styles.desktopClose} disabled={isDeleting} onClick={onClose}>
          <X aria-hidden="true" size={18} />
        </IconButton>
      </section>
    </Dialog>
  );
}

export function MobileMenu({ onClose, sidebar, state }: { onClose: () => void; sidebar: ProjectSidebarProps; state: PresenceStatus }) {
  return (
    <Portal>
      <div className={styles.drawerLayer} data-overlay-scrim="" data-state={state}>
        <Overlay kind="drawer" label="Меню" onDismiss={onClose} state={state}>
          <aside className={styles.mobileDrawer}>
            <div className={styles.drawerHeader}>
              <h2>Canon Keeper</h2>
              <IconButton aria-label="Закрыть меню" onClick={onClose}>
                <X aria-hidden="true" size={18} />
              </IconButton>
            </div>
            <ProjectSidebar {...sidebar} variant="drawer"
              onOpenChat={() => { onClose(); sidebar.onOpenChat(); }}
              onOpenManuscript={() => { onClose(); sidebar.onOpenManuscript(); }}
              onOpenReader={sidebar.onOpenReader ? () => { onClose(); sidebar.onOpenReader?.(); } : undefined}
              onOpenNotes={sidebar.onOpenNotes ? () => { onClose(); sidebar.onOpenNotes?.(); } : undefined}
              onOpenSaga={sidebar.onOpenSaga ? () => { onClose(); sidebar.onOpenSaga?.(); } : undefined}
              onOpenWriting={sidebar.onOpenWriting ? () => { onClose(); sidebar.onOpenWriting?.(); } : undefined}
              onOpenDrawing={sidebar.onOpenDrawing ? () => { onClose(); sidebar.onOpenDrawing?.(); } : undefined}
              onOpenGallery={sidebar.onOpenGallery ? () => { onClose(); sidebar.onOpenGallery?.(); } : undefined}
              onCreateNote={sidebar.onCreateNote ? () => { onClose(); sidebar.onCreateNote?.(); } : undefined} />
          </aside>
        </Overlay>
      </div>
    </Portal>
  );
}
