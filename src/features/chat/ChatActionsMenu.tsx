import { MessageCircle, MoreHorizontal, Pencil } from 'lucide-react';
import { useEffect, useState, type MouseEvent } from 'react';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { ActionSheet } from '../../ui/overlays/ActionSheet';
import { DeleteMenuItem } from '../../ui/overlays/DeleteMenuItem';
import { Popover } from '../../ui/overlays/Popover';
import styles from './ResearchChatSessions.module.css';

/** Where the menu of the actions stands. */
export interface ChatActionsAnchor {
  left: number;
  top: number;
}

/** Under a button, by its right edge. */
export function chatActionsAnchor(event: MouseEvent<HTMLElement>): ChatActionsAnchor {
  const bounds = event.currentTarget.getBoundingClientRect();
  return { left: Math.max(8, bounds.right - 212), top: bounds.bottom + 4 };
}

/** A touch or a narrow screen: the actions of a chat are a sheet with targets of 44 px, not a menu. */
export function useTouchActions() {
  const [touch, setTouch] = useState(false);
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return undefined;
    const media = window.matchMedia('(pointer: coarse), (max-width: 760px)');
    const update = () => setTouch(media.matches);
    update();
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);
  return touch;
}

interface ChatActionsMenuProps {
  /** The place of the menu; none — the actions are a sheet. */
  anchor: ChatActionsAnchor | null;
  /** A change of the chat is on its way: the sheet waits. */
  busy?: boolean;
  /** An editor of the saga: renames and deletes. */
  canEdit: boolean;
  /** Why the chat cannot be deleted now: the item stays and says so (specs/2026-11-11-ux4-after-answer.md AA3.3). */
  deleteUnavailable?: string | null | undefined;
  onDelete: () => void;
  onDismiss: () => void;
  /** «Открыть чат» of the sheet of the chat list. */
  onOpen?: () => void;
  onRename: () => void;
  title: string;
}

/**
 * «Действия чата «…»» (specs/2026-11-10-ux1-chat.md CH2.2): «Переименовать» and «Удалить чат…» last and apart (PA3.2) —
 * the only way to delete a chat. One menu for the row of the chat in the list and for «⋯» over the feed while the list is hidden.
 */
export function ChatActionsMenu({ anchor, busy = false, canEdit, deleteUnavailable, onDelete, onDismiss, onOpen, onRename, title }: ChatActionsMenuProps) {
  const label = `Действия чата «${title}»`;
  const [refused, setRefused] = useState(false);
  const remove = () => {
    if (deleteUnavailable) setRefused(true);
    else onDelete();
  };
  const reason = deleteUnavailable && refused ? <p className={styles.menuReason} role="status">{deleteUnavailable}</p> : null;
  if (anchor) {
    return (
      <Popover className={styles.sessionMenu} label={label} onDismiss={onDismiss} open role="menu" style={anchor}>
        <button onClick={onRename} role="menuitem" type="button">
          <Pencil aria-hidden="true" size={16} />
          Переименовать
        </button>
        <DeleteMenuItem aria-disabled={deleteUnavailable ? true : undefined} label="Удалить чат…" onClick={remove} title={deleteUnavailable ?? undefined} />
        {reason}
      </Popover>
    );
  }
  return (
    <ActionSheet canDismiss={!busy} className={styles.sessionSheet} label={label} onDismiss={onDismiss}>
      <header><strong>Чат «{title}»</strong></header>
      {canEdit ? (
        <Button onClick={onRename} variant="ghost">
          <Pencil aria-hidden="true" size={16} />
          Переименовать
        </Button>
      ) : null}
      {onOpen ? (
        <Button onClick={onOpen} variant="ghost">
          <MessageCircle aria-hidden="true" size={16} />
          Открыть чат
        </Button>
      ) : null}
      {canEdit ? (
        <DeleteMenuItem aria-disabled={deleteUnavailable ? true : undefined} label="Удалить чат…" onClick={remove} sheet />
      ) : null}
      {reason}
    </ActionSheet>
  );
}

interface ChatActionsButtonProps {
  disabled: boolean;
  deleteUnavailable?: string | null | undefined;
  onDelete: () => void;
  onRename: () => void;
  title: string;
}

/**
 * «⋯» beside «Чаты» over the feed (CH2.2): the actions of the open chat while the chat list is hidden, so a phone reaches
 * them from the chat itself. For an editor of the saga.
 */
export function ChatActionsButton({ deleteUnavailable, disabled, onDelete, onRename, title }: ChatActionsButtonProps) {
  const touch = useTouchActions();
  const [open, setOpen] = useState<ChatActionsAnchor | 'sheet' | null>(null);
  const act = (action: () => void) => () => {
    setOpen(null);
    action();
  };
  return (
    <>
      <IconButton aria-label={`Действия чата «${title}»`} disabled={disabled} onClick={(event) => setOpen(touch ? 'sheet' : chatActionsAnchor(event))} size="touch">
        <MoreHorizontal aria-hidden="true" size={18} />
      </IconButton>
      {open ? <ChatActionsMenu anchor={open === 'sheet' ? null : open} canEdit deleteUnavailable={deleteUnavailable} onDelete={act(onDelete)} onDismiss={() => setOpen(null)} onRename={act(onRename)} title={title} /> : null}
    </>
  );
}
