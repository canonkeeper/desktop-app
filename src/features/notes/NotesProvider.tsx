import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import type { NotesApiClient, NotesListEntry, NoteResolvedSource } from '../../entities/notes/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { manuscriptSourceDestination } from '../../shared/navigation/manuscript-route';
import { Drawer } from '../../ui/overlays/Drawer';
import { ToastRegion } from '../../ui/feedback/Toast';
import { useToastQueue } from '../../ui/feedback/useToastQueue';
import { NotesContext, type NoteCapture } from '../../shared/notes/notes-context';
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
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const sourceNavigateRef = useRef<RouteNavigate | undefined>(undefined);
  const lastSelection = useRef<string | undefined>(undefined);
  const toasts = useToastQueue();
  const navigationQuery = new URLSearchParams(route.query);
  navigationQuery.delete('menu');
  navigationQuery.delete('bookMenu');
  navigationQuery.sort();
  const signature = `${route.path}?${navigationQuery.toString()}`;
  useEffect(() => { setPanel(null); }, [signature]);
  const actions = useMemo(() => ({
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
  function openSource(source: NoteResolvedSource, id: string) {
    setPanel(null);
    if (source.kind === 'chat') {
      (sourceNavigateRef.current ?? navigate)('/chat', { projectId: source.projectId, chatId: source.chatId, noteId: id });
    } else {
      const destination = manuscriptSourceDestination(source.locator);
      (sourceNavigateRef.current ?? navigate)(destination.path, destination.query);
    }
  }
  function dismiss() {
    if (editorRef.current) editorRef.current.dismiss();
    else setPanel(null);
  }
  const back = () => setPanel({ kind: 'list' });
  const close = () => setPanel(null);
  return <NotesContext.Provider value={actions}>
    {children}
    {route.surface === 'chat' && route.query.get('noteId') ? <ChatNoteLanding api={api} accountId={accountId} route={route} navigate={navigate} /> : null}
    {panel ? <Drawer canDismiss className={styles.panel} label="Заметки" onDismiss={dismiss} returnFocusRef={returnFocusRef} side="right" size="reading">
      <div className={styles.layout} hidden={panel.kind !== 'list'} style={panel.kind === 'list' ? undefined : { display: 'none' }}>
        <NotesList api={api} accountId={accountId} visible={panel.kind === 'list'} selectedKey={lastSelection.current} onClose={close} onNew={() => setPanel({ kind: 'create', capture: { body: '' } })} onSelect={select} onAll={() => { close(); (sourceNavigateRef.current ?? navigate)('/notes'); }} />
      </div>
      {panel.kind === 'edit' ? <NoteDetails api={api} accountId={accountId} noteId={panel.noteId} editorRef={editorRef} key={panel.noteId} onBack={back} onClose={close} onSaved={(outcome) => { if (outcome === 'deleted') lastSelection.current = undefined; back(); toasts.push({ message: outcome === 'saved' ? 'Заметка сохранена' : 'Заметка удалена', tone: 'success' }); }} onSource={openSource} />
          : panel.kind === 'create' ? <NoteEditor api={api} accountId={accountId} capture={panel.capture} dismissRef={editorRef} onBack={back} onClose={close} onSaved={() => { back(); toasts.push({ message: 'Заметка сохранена', tone: 'success' }); }} onSource={openSource} /> : null}
    </Drawer> : null}
    {toasts.queue.length ? <ToastRegion onDismiss={toasts.dismiss} queue={toasts.queue} /> : null}
  </NotesContext.Provider>;
}
