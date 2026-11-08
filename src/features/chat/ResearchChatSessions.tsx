import { useQueryClient } from '@tanstack/react-query';
import { Check, MessageCircle, MoreHorizontal, Pencil, Plus, Trash2, X } from 'lucide-react';
import { useEffect, useRef, useState, type FormEvent, type MouseEvent } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../entities/api-errors';
import {
  type ChatApiClient,
  chatQueryKeys,
  type ChatScope,
  type ChatSession,
} from '../../entities/chat/api';
import { Button } from '../../ui/controls/Button';
import { Field } from '../../ui/controls/Field';
import { IconButton } from '../../ui/controls/IconButton';
import { Alert } from '../../ui/feedback/Alert';
import { Skeleton } from '../../ui/feedback/Skeleton';
import { SupportMailLink, SupportReference } from '../../ui/feedback/SupportReference';
import type { ToastInput } from '../../ui/feedback/useToastQueue';
import { ActionSheet } from '../../ui/overlays/ActionSheet';
import { Popover } from '../../ui/overlays/Popover';
import { chatErrorRequestId, chatIsWorking, useRenameChatSessionMutation } from './api';
import styles from './ResearchChatSessions.module.css';

interface ResearchChatSessionsProps {
  api: ChatApiClient;
  /** The selected chat works or changes: its actions wait. */
  busy: boolean;
  canCreate: boolean;
  error: unknown;
  errorRequestId?: string;
  loading: boolean;
  onAuthorityLost: () => void;
  onDelete: () => void;
  onNotify: (toast: ToastInput) => void;
  onRetry: () => void;
  onSelect: (chatId: string) => void;
  onSessionGone: (chatId: string) => void;
  onStartNew: () => void;
  scope: ChatScope;
  selectedChatId: string;
  sessions: ChatSession[];
  /** A question is being sent or a chat deleted: a new chat waits (R1.2). */
  startBusy: boolean;
}

type SessionMenu = { chatId: string; left: number; top: number };

export function ResearchChatSessions({
  api,
  busy,
  canCreate,
  error,
  errorRequestId,
  loading,
  onAuthorityLost,
  onDelete,
  onNotify,
  onRetry,
  onSelect,
  onSessionGone,
  onStartNew,
  scope,
  selectedChatId,
  sessions,
  startBusy,
}: ResearchChatSessionsProps) {
  const [coarseActions, setCoarseActions] = useState(false);
  const [editingChatId, setEditingChatId] = useState('');
  const [draft, setDraft] = useState('');
  const [renameError, setRenameError] = useState('');
  const [menu, setMenu] = useState<SessionMenu | null>(null);
  const [sheetChatId, setSheetChatId] = useState('');
  const renameSubmitting = useRef(false);
  const queryClient = useQueryClient();
  const renameMutation = useRenameChatSessionMutation(api, scope);

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return undefined;
    const media = window.matchMedia('(pointer: coarse), (max-width: 760px)');
    const update = () => setCoarseActions(media.matches);
    update();
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);

  useEffect(() => {
    if (canCreate) return;
    setEditingChatId('');
    setMenu(null);
    setSheetChatId('');
  }, [canCreate]);

  useEffect(() => {
    setEditingChatId('');
    setMenu(null);
    setSheetChatId('');
  }, [selectedChatId]);

  const actionSession = sessions.find((session) => session.id === (menu?.chatId || sheetChatId));
  const newChatButton = canCreate && !error ? (
    <button
      aria-current={!selectedChatId ? 'page' : undefined}
      className={styles.newChatButton}
      disabled={startBusy || renameMutation.isPending}
      onClick={onStartNew}
      type="button"
    >
      <Plus aria-hidden="true" size={16} />
      <span>Новый чат</span>
    </button>
  ) : null;

  function beginRename(session: ChatSession) {
    if (!canCreate || renameMutation.isPending) return;
    setMenu(null);
    setSheetChatId('');
    setEditingChatId(session.id);
    setDraft(session.title);
    setRenameError('');
  }

  function cancelRename() {
    if (renameMutation.isPending) return;
    setEditingChatId('');
    setDraft('');
    setRenameError('');
  }

  async function submitRename(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!editingChatId || renameMutation.isPending || renameSubmitting.current) return;
    const normalized = draft.trim();
    const length = Array.from(normalized).length;
    const code = length === 0 ? 'title_empty' : length > 120 ? 'title_too_long' : null;
    if (code) {
      setRenameError(renameErrorMessage(code));
      return;
    }
    renameSubmitting.current = true;
    try {
      await renameMutation.mutateAsync({ chatId: editingChatId, title: normalized });
      cancelRename();
    } catch (renameFailure) {
      if (isApiStatusFailure(renameFailure) && renameFailure.status === 422) {
        setRenameError(renameErrorMessage(renameFailure.problem.code));
        return;
      }
      if (isApiStatusFailure(renameFailure) && renameFailure.status === 403) {
        cancelRename();
        onAuthorityLost();
        return;
      }
      if (isApiStatusFailure(renameFailure) && (renameFailure.status === 404 || renameFailure.status === 409)) {
        queryClient.setQueryData<Awaited<ReturnType<ChatApiClient['listChatSessions']>>>(
          chatQueryKeys.sessions(scope),
          (current) => current
            ? { ...current, data: current.data.filter((session) => session.id !== editingChatId) }
            : current,
        );
        queryClient.removeQueries({ queryKey: chatQueryKeys.session(scope, editingChatId), exact: true });
        void queryClient.invalidateQueries({ queryKey: chatQueryKeys.sessions(scope), exact: true });
        const missingChatId = editingChatId;
        cancelRename();
        onSessionGone(missingChatId);
        onNotify({ message: 'Чат больше недоступен. Список обновлён.', tone: 'neutral' });
        return;
      }
      const message = publicApiErrorMessage(renameFailure, 'Не удалось сохранить название.');
      const requestId = chatErrorRequestId(renameFailure);
      // The notice offers the letter with the code as its action (R4.2.1).
      onNotify({ action: requestId ? <SupportMailLink code={requestId} /> : undefined, message, tone: 'error' });
    } finally {
      renameSubmitting.current = false;
    }
  }

  function openActions(event: MouseEvent<HTMLButtonElement>, session: ChatSession) {
    if (coarseActions) {
      setSheetChatId(session.id);
      return;
    }
    const bounds = event.currentTarget.getBoundingClientRect();
    setMenu({ chatId: session.id, left: Math.max(8, bounds.right - 212), top: bounds.bottom + 4 });
  }

  function selectFromActions(chatId: string) {
    setMenu(null);
    setSheetChatId('');
    onSelect(chatId);
  }

  return (
    <aside aria-label="Сохранённые чаты" className={styles.sessions}>
      {newChatButton}
      {loading ? <div aria-label="Загрузка чатов" className={styles.loading} role="status">
        {[232, 232, 200, 220].map((width, index) => (
          <Skeleton key={index} style={{ width, maxWidth: '100%', height: 32 }} />
        ))}
      </div> : null}
      {!error ? sessions.map((session) => editingChatId === session.id ? (
        <form
          className={styles.renameRow}
          key={session.id}
          onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget as Node | null)) cancelRename();
          }}
          onSubmit={(event) => void submitRename(event)}
        >
          <Field
            autoFocus
            className={styles.renameInput}
            disabled={renameMutation.isPending}
            error={renameError}
            label={<span className={styles.srOnly}>Название чата</span>}
            onChange={(event) => {
              setDraft(event.target.value);
              setRenameError('');
            }}
            onKeyDown={(event) => {
              if (event.key === 'Escape') {
                event.preventDefault();
                cancelRename();
              } else if (event.key === 'Enter') {
                event.preventDefault();
                event.currentTarget.form?.requestSubmit();
              }
            }}
            value={draft}
          />
          <IconButton
            aria-label="Сохранить название"
            className={styles.renameAction}
            disabled={renameMutation.isPending}
            size="sm"
            type="submit"
          >
            <Check aria-hidden="true" size={15} />
          </IconButton>
          <IconButton
            aria-label="Отменить переименование"
            className={styles.renameAction}
            disabled={renameMutation.isPending}
            onClick={cancelRename}
            size="sm"
          >
            <X aria-hidden="true" size={15} />
          </IconButton>
        </form>
      ) : (
        <div className={styles.sessionRow} data-selected={session.id === selectedChatId || undefined} key={session.id}>
          <button
            aria-current={session.id === selectedChatId ? 'page' : undefined}
            aria-label={chatIsWorking(session) ? `${session.title}, идёт работа` : undefined}
            className={styles.sessionSelect}
            data-working={chatIsWorking(session) || undefined}
            onClick={() => onSelect(session.id)}
            type="button"
          >
            <MessageCircle aria-hidden="true" className={styles.sessionIcon} size={16} />
            <span>{session.title}</span>
            {chatIsWorking(session) ? <span aria-hidden="true" className={styles.working} /> : null}
          </button>
          {session.id === selectedChatId && (canCreate || coarseActions) ? (
            <IconButton
              aria-label={`Действия чата «${session.title}»`}
              className={styles.sessionActions}
              disabled={busy || renameMutation.isPending}
              onClick={(event) => openActions(event, session)}
              size={coarseActions ? 'touch' : 'sm'}
            >
              <MoreHorizontal aria-hidden="true" size={17} />
            </IconButton>
          ) : null}
        </div>
      )) : null}
      {error ? (
        <Alert className={styles.sessionsError} tone="error">
          <p>{publicApiErrorMessage(error, 'Не удалось загрузить чаты.')}</p>
          {errorRequestId ? <SupportReference code={errorRequestId} /> : null}
          <Button aria-label="Повторить загрузку чатов" onClick={onRetry} size="sm" variant="outline">Повторить</Button>
        </Alert>
      ) : null}

      {menu && actionSession ? (
        <Popover
          className={styles.sessionMenu}
          label={`Действия чата «${actionSession.title}»`}
          onDismiss={() => setMenu(null)}
          open
          role="menu"
          style={{ left: menu.left, top: menu.top }}
        >
          <button onClick={() => beginRename(actionSession)} role="menuitem" type="button">
            <Pencil aria-hidden="true" size={16} />
            Переименовать
          </button>
          <button className={styles.dangerAction} onClick={() => { setMenu(null); onDelete(); }} role="menuitem" type="button">
            <Trash2 aria-hidden="true" size={16} />
            Удалить чат
          </button>
        </Popover>
      ) : null}

      {sheetChatId && actionSession ? (
        <ActionSheet
          canDismiss={!renameMutation.isPending}
          className={styles.sessionSheet}
          label={`Действия чата «${actionSession.title}»`}
          onDismiss={() => setSheetChatId('')}
        >
          <header><strong>Чат «{actionSession.title}»</strong></header>
          {canCreate ? (
            <Button onClick={() => beginRename(actionSession)} variant="ghost">
              <Pencil aria-hidden="true" size={16} />
              Переименовать
            </Button>
          ) : null}
          <Button onClick={() => selectFromActions(actionSession.id)} variant="ghost">
            <MessageCircle aria-hidden="true" size={16} />
            Открыть чат
          </Button>
          {canCreate ? (
            <Button onClick={() => { setSheetChatId(''); onDelete(); }} variant="danger">
              <Trash2 aria-hidden="true" size={16} />
              Удалить чат
            </Button>
          ) : null}
        </ActionSheet>
      ) : null}

    </aside>
  );
}

function renameErrorMessage(code?: string) {
  return code === 'title_empty'
    ? 'Название не может быть пустым.'
    : 'Название не может быть длиннее 120 символов.';
}
