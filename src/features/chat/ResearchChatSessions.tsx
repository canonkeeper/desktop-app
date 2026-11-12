import { loadFailureWords } from '../../entities/api-errors';
import { ErrorBlock } from '../../ui/feedback/ErrorBlock';
import { useQueryClient } from '@tanstack/react-query';
import { MessageCircle, MoreHorizontal, Plus } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type FormEvent, type MouseEvent } from 'react';
import { createPortal } from 'react-dom';
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
import { SearchField } from '../../ui/controls/SearchField';
import { EmptyFilter } from '../../ui/feedback/EmptyState';
import { Skeleton } from '../../ui/feedback/Skeleton';
import { useWriteToUsAction } from '../../ui/feedback/SupportReference';
import type { ToastInput } from '../../ui/feedback/useToastQueue';
import { chatErrorRequestId, chatIsWorking, useRenameChatSessionMutation } from './api';
import { chatActionsAnchor, ChatActionsMenu, useTouchActions, type ChatActionsAnchor } from './ChatActionsMenu';
import { askedAt, chatDate, chatDayWords, chatsByDay } from './chat-days';
import styles from './ResearchChatSessions.module.css';

interface ResearchChatSessionsProps {
  api: ChatApiClient;
  canCreate: boolean;
  error: unknown;
  errorRequestId?: string;
  loading: boolean;
  /** The head of the column the page keeps, beside its heading: «Новый чат» stands there (specs/2026-11-11-ux2-polish.md PL2.1). */
  newChatIn?: HTMLElement | null;
  onAuthorityLost: () => void;
  /** «Удалить чат…» of a row: the chat of that row, open or not (AA3.3). */
  onDelete: (chatId: string) => void;
  onNotify: (toast: ToastInput) => void;
  /** The row took the rename asked for by `renameRequest`. */
  onRenameStarted?: () => void;
  onRetry: () => void;
  onSelect: (chatId: string) => void;
  onSessionGone: (chatId: string) => void;
  onStartNew: () => void;
  /** The chat whose name «Переименовать» of the actions over the feed asks to change here (specs/2026-11-10-ux1-chat.md CH2.2). */
  renameRequest?: string;
  scope: ChatScope;
  selectedChatId: string;
  sessions: ChatSession[];
  /** A question is being sent or a chat deleted: a new chat waits (R1.2). */
  startBusy: boolean;
}

type SessionMenu = ChatActionsAnchor & { chatId: string };

/** The list finds a chat by its name from this many chats on (AA3.2). */
const searchFrom = 10;

/** A name as the author may type it: the case and «ё» do not matter. */
const searchable = (text: string) => text.toLocaleLowerCase('ru-RU').replaceAll('ё', 'е');

export function ResearchChatSessions({
  api,
  canCreate,
  error,
  errorRequestId,
  loading,
  newChatIn = null,
  onAuthorityLost,
  onDelete,
  onNotify,
  onRenameStarted,
  onRetry,
  onSelect,
  onSessionGone,
  onStartNew,
  renameRequest = '',
  scope,
  selectedChatId,
  sessions,
  startBusy,
}: ResearchChatSessionsProps) {
  const coarseActions = useTouchActions();
  const [editingChatId, setEditingChatId] = useState('');
  const [draft, setDraft] = useState('');
  const [renameError, setRenameError] = useState('');
  const [menu, setMenu] = useState<SessionMenu | null>(null);
  const [sheetChatId, setSheetChatId] = useState('');
  const [search, setSearch] = useState('');
  const writeToUs = useWriteToUsAction();
  const renameSubmitting = useRef(false);
  const queryClient = useQueryClient();
  const renameMutation = useRenameChatSessionMutation(api, scope);

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

  // «Переименовать» of the actions over the feed: the name of that chat is changed in its row here.
  useEffect(() => {
    const session = renameRequest ? sessions.find((item) => item.id === renameRequest) : undefined;
    if (!session) return;
    beginRename(session);
    onRenameStarted?.();
  }, [renameRequest, sessions]);

  const actionSession = sessions.find((session) => session.id === (menu?.chatId || sheetChatId));
  // The days of the list by the last question of each chat, by the clock of the author (AA3.1); a name found on the page (AA3.2).
  const searching = sessions.length >= searchFrom ? search.trim() : '';
  const found = useMemo(() => searching ? sessions.filter((session) => searchable(session.title).includes(searchable(searching))) : sessions, [searching, sessions]);
  const groups = useMemo(() => chatsByDay(found, new Date()), [found]);
  const newChatButton = canCreate && !error ? (
    <button
      aria-current={!selectedChatId ? 'page' : undefined}
      className={styles.newChatButton}
      data-in-row={newChatIn ? '' : undefined}
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

  /** Leaving the field keeps the name typed, as Enter does; an unchanged or empty name leaves the old one (AA3.4, Ч-61). */
  function leaveRename() {
    const editing = sessions.find((session) => session.id === editingChatId);
    if (!editing || !draft.trim() || draft.trim() === editing.title) {
      cancelRename();
      return;
    }
    void submitRename();
  }

  async function submitRename(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
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
      // The notice offers «Написать нам» with the code as its action (R4.2.1): the window of the site, as everywhere — the
      // area of the lines lies above the account of the page (specs/2026-11-12-ux12-shell.md SH2.3).
      onNotify({ action: requestId ? writeToUs({ code: requestId, topic: 'bug' }) : undefined, message, tone: 'error' });
    } finally {
      renameSubmitting.current = false;
    }
  }

  function openActions(event: MouseEvent<HTMLButtonElement>, session: ChatSession) {
    if (coarseActions) {
      setSheetChatId(session.id);
      return;
    }
    setMenu({ chatId: session.id, ...chatActionsAnchor(event) });
  }

  function selectFromActions(chatId: string) {
    setMenu(null);
    setSheetChatId('');
    onSelect(chatId);
  }

  return (
    <aside aria-label="Сохранённые чаты" className={styles.sessions}>
      {newChatIn ? (newChatButton ? createPortal(newChatButton, newChatIn) : null) : newChatButton}
      {loading ? <div aria-label="Загрузка чатов" className={styles.loading} role="status">
        {[232, 232, 200, 220].map((width, index) => (
          <Skeleton key={index} style={{ width, maxWidth: '100%', height: 32 }} />
        ))}
      </div> : null}
      {!error && sessions.length >= searchFrom ? (
        <SearchField className={styles.search} label="Найти в чатах" labelHidden onChange={(event) => setSearch(event.target.value)}
          onClear={() => setSearch('')} placeholder="Найти в чатах" value={search} />
      ) : null}
      {!error && searching && found.length === 0 ? (
        <EmptyFilter onReset={() => setSearch('')} reset="Сбросить поиск" words={`Ничего не нашлось по «${searching}»`} />
      ) : null}
      {!error ? groups.map((group) => (
        <section aria-label={chatDayWords[group.day]} className={styles.group} key={group.day}>
          <p aria-hidden="true" className={styles.groupLabel}>{chatDayWords[group.day]}</p>
          {group.chats.map((session) => editingChatId === session.id ? (
            <form
              className={styles.renameRow}
              key={session.id}
              onBlur={(event) => {
                if (!event.currentTarget.contains(event.relatedTarget as Node | null)) leaveRename();
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
                    event.stopPropagation();
                    cancelRename();
                  } else if (event.key === 'Enter') {
                    event.preventDefault();
                    event.currentTarget.form?.requestSubmit();
                  }
                }}
                value={draft}
              />
              <div className={styles.renameActions}>
                <Button aria-label="Сохранить название" disabled={renameMutation.isPending} size="sm" type="submit" variant="primary">Сохранить</Button>
                <Button aria-label="Отменить переименование" disabled={renameMutation.isPending} onClick={cancelRename} size="sm" variant="ghost">Отменить</Button>
              </div>
            </form>
          ) : (
            <div className={styles.sessionRow} data-selected={session.id === selectedChatId || undefined} key={session.id}>
              <button
                aria-current={session.id === selectedChatId ? 'page' : undefined}
                aria-label={chatIsWorking(session) ? `${session.title}, готовится` : session.title}
                className={styles.sessionSelect}
                data-working={chatIsWorking(session) || undefined}
                onClick={() => onSelect(session.id)}
                // The whole name where the row cuts it (AA3.1).
                title={session.title}
                type="button"
              >
                <MessageCircle aria-hidden="true" className={styles.sessionIcon} size={16} />
                <span className={styles.sessionTitle}>{session.title}</span>
                {chatIsWorking(session) ? <span aria-hidden="true" className={styles.working} /> : null}
              </button>
              {/* The second line of the row: the date of its last question and its state. It stands over the button and lets
                  its press through, so the name of the button is the name of the chat the screen shows. */}
              <span aria-hidden="true" className={styles.sessionMeta} data-chat-date="">
                {chatDate(new Date(askedAt(session)), new Date())}{chatIsWorking(session) ? ' · готовится' : ''}
              </span>
              {canCreate || coarseActions ? (
                <IconButton
                  aria-label={`Действия чата «${session.title}»`}
                  className={styles.sessionActions}
                  data-touch={coarseActions || undefined}
                  disabled={renameMutation.isPending}
                  onClick={(event) => openActions(event, session)}
                  size={coarseActions ? 'touch' : 'sm'}
                >
                  <MoreHorizontal aria-hidden="true" size={17} />
                </IconButton>
              ) : null}
            </div>
          ))}
        </section>
      )) : null}
      {error ? (
        <ErrorBlock className={styles.sessionsError} cause={loadFailureWords(error).cause} code={errorRequestId} onRetry={onRetry} title="Не открылся список чатов" />
      ) : null}

      {actionSession ? (
        <ChatActionsMenu
          anchor={menu ? { left: menu.left, top: menu.top } : null}
          busy={renameMutation.isPending}
          canEdit={canCreate}
          // A chat whose question is being answered is deleted once the answer is ready or stopped (AA3.3).
          deleteUnavailable={chatIsWorking(actionSession) ? 'Удалить чат можно, когда ответ будет готов или остановлен.' : null}
          onDelete={() => { setMenu(null); setSheetChatId(''); onDelete(actionSession.id); }}
          onDismiss={() => { setMenu(null); setSheetChatId(''); }}
          onOpen={() => selectFromActions(actionSession.id)}
          onRename={() => beginRename(actionSession)}
          title={actionSession.title}
        />
      ) : null}

    </aside>
  );
}

function renameErrorMessage(code?: string) {
  return code === 'title_empty'
    ? 'Название не может быть пустым.'
    : 'Название не может быть длиннее 120 символов.';
}
