import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import type { NotesApiClient, NotesListEntry, NoteResolvedSource, PersonalNote } from '../../entities/notes/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { manuscriptSourceDestination } from '../../shared/navigation/manuscript-route';
import { Button } from '../../ui/controls/Button';
import { Drawer } from '../../ui/overlays/Drawer';
import { useToasts } from '../../ui/feedback/useToastQueue';
import { NotesContext, type NoteBeside, type NoteCapture } from '../../shared/notes/notes-context';
import { NoteEditor, type NoteEditorHandle } from './NoteEditor';
import { NoteDetails } from './NoteDetails';
import { NotesList } from './NotesList';
import { ChatNoteLanding } from './ChatNoteLanding';
import styles from './NotesPanel.module.css';

type Panel = { kind: 'list' } | { kind: 'create'; capture: NoteCapture } | { kind: 'edit'; noteId: string };

export function NotesProvider({ api, accountId, children, navigate, route }: {
  api: NotesApiClient;
  accountId: string;
  children: ReactNode;
  navigate: RouteNavigate;
  route: AppRoute;
}) {
  const [panel, setPanel] = useState<Panel | null>(null);
  const editorRef = useRef<NoteEditorHandle>(null);
  const thoughtRef = useRef<HTMLTextAreaElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const sourceNavigateRef = useRef<RouteNavigate | undefined>(undefined);
  const lastSelection = useRef<string | undefined>(undefined);
  const toasts = useToasts();
  const navigationQuery = new URLSearchParams(route.query);
  navigationQuery.delete('menu');
  navigationQuery.sort();
  const signature = `${route.path}?${navigationQuery.toString()}`;
  useEffect(() => { setPanel(null); }, [signature]);
  // The window beside the text is drawn by the page that asks for it, with the account and the toasts of now.
  const beside = useRef({ api, accountId, toasts });
  beside.current = { api, accountId, toasts };
  const actions = useMemo(() => ({
    renderNote(note: NoteBeside) {
      return <NoteBesideText key={note.noteId} {...beside.current} {...note} />;
    },
    open() {
      sourceNavigateRef.current = undefined;
      returnFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      setPanel({ kind: 'list' });
    },
    openWithNavigation(navigate: RouteNavigate) {
      sourceNavigateRef.current = navigate;
      returnFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      setPanel({ kind: 'list' });
    },
    capture(capture: NoteCapture, navigate?: RouteNavigate) {
      sourceNavigateRef.current = navigate;
      returnFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      setPanel({ kind: 'create', capture });
    },
  }), []);

  function select(entry: NotesListEntry) {
    lastSelection.current = entry.id;
    setPanel({ kind: 'edit', noteId: entry.id });
  }
  /** «К месту в книге» of the panel: the reader at the paragraph with this note beside the text (NT2.2). */
  function openSource(source: NoteResolvedSource, id: string) {
    setPanel(null);
    if (source.kind === 'chat') {
      (sourceNavigateRef.current ?? navigate)('/chat', { projectId: source.projectId, chatId: source.chatId, noteId: id });
    } else {
      const destination = manuscriptSourceDestination(source.locator);
      (sourceNavigateRef.current ?? navigate)(destination.path, { ...destination.query, contents: 'notes', note: id });
    }
  }
  function dismiss() {
    if (editorRef.current) editorRef.current.dismiss();
    else setPanel(null);
  }
  const back = () => setPanel({ kind: 'list' });
  const close = () => setPanel(null);
  /** The line of the root after a save opens the note in «Заметки» (specs/2026-11-11-ux7-notes.md NT1.4). */
  function saved(note?: PersonalNote) {
    back();
    const id = toasts.push({ message: 'Заметка сохранена', tone: 'success', action: note ? <Button onClick={() => {
      toasts.dismiss(id);
      (sourceNavigateRef.current ?? navigate)('/notes', { note: note.id });
    }} size="sm" variant="ghost">Открыть</Button> : undefined });
  }
  return <NotesContext.Provider value={actions}>
    {children}
    {route.surface === 'chat' && route.query.get('noteId') ? <ChatNoteLanding api={api} accountId={accountId} route={route} navigate={navigate} /> : null}
    {panel ? <Drawer canDismiss className={styles.panel} initialFocusRef={panel.kind === 'create' ? thoughtRef : undefined} label="Заметки" onDismiss={dismiss} returnFocusRef={returnFocusRef} side="right" size="reading">
      <div className={styles.layout} hidden={panel.kind !== 'list'} style={panel.kind === 'list' ? undefined : { display: 'none' }}>
        <NotesList api={api} accountId={accountId} visible={panel.kind === 'list'} selectedKey={lastSelection.current} onClose={close} onNew={() => setPanel({ kind: 'create', capture: { body: '' } })} onSelect={select} onAll={() => { close(); (sourceNavigateRef.current ?? navigate)('/notes'); }} />
      </div>
      {panel.kind === 'edit' ? <NoteDetails api={api} accountId={accountId} noteId={panel.noteId} editorRef={editorRef} key={panel.noteId} onBack={back} onClose={close} onSaved={(outcome, note) => {
        if (outcome === 'saved') { saved(note); return; }
        lastSelection.current = undefined;
        back();
        toasts.push({ message: 'Заметка удалена', tone: 'success' });
      }} onSource={openSource} />
          : panel.kind === 'create' ? <NoteEditor api={api} accountId={accountId} capture={panel.capture} dismissRef={editorRef} thoughtRef={thoughtRef} onBack={back} onClose={close} onSaved={(_outcome, note) => saved(note)} onSource={openSource} /> : null}
    </Drawer> : null}
  </NotesContext.Provider>;
}

/**
 * A note beside the text of the reader (specs/2026-11-11-ux7-notes.md NT2.2, NT2.3; board 24·21): the thought is written
 * while the text is in sight; ← goes back to the notes of the chapter; the place of the note leads to its paragraph on
 * the same page.
 */
function NoteBesideText({ api, accountId, navigate, noteId, onBack, toasts }: NoteBeside & {
  api: NotesApiClient; accountId: string; toasts: ReturnType<typeof useToasts>;
}) {
  return <NoteDetails api={api} accountId={accountId} beside noteId={noteId} onBack={onBack} onClose={onBack}
    onSaved={(outcome) => {
      if (outcome === 'deleted') onBack();
      toasts.push({ message: outcome === 'saved' ? 'Заметка сохранена' : 'Заметка удалена', tone: 'success' });
    }}
    onSource={(source, id) => {
      if (source.kind === 'chat') {
        navigate('/chat', { projectId: source.projectId, chatId: source.chatId, noteId: id });
        return;
      }
      const destination = manuscriptSourceDestination(source.locator);
      navigate(destination.path, { ...destination.query, contents: 'notes', note: id }, { replace: true });
    }} />;
}
