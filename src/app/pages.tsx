import { lazy } from 'react';
import type { EditionPage } from './edition';

// Ленивые продуктовые маршруты не увеличивают начальный auth-бандл.
const importManuscriptPage = () => import('../features/manuscript/ManuscriptPage');
const ManuscriptPage = lazy(() => importManuscriptPage().then((module) => ({ default: module.ManuscriptPage })));
const importChatPage = () => import('../features/chat/ChatPage');
const ChatPage = lazy(() => importChatPage().then((module) => ({ default: module.ChatPage })));
const importNotesPage = () => import('../pages/NotesPage');
const NotesPage = lazy(() => importNotesPage().then((module) => ({ default: module.NotesPage })));
const importSagaPage = () => import('../pages/SagaPage');
const SagaPage = lazy(() => importSagaPage().then((module) => ({ default: module.SagaPage })));
const importAuthorMemoryPage = () => import('../pages/AuthorMemoryPage');
const AuthorMemoryPage = lazy(() => importAuthorMemoryPage().then((module) => ({ default: module.AuthorMemoryPage })));

/** The pages of the open sections, the same in every edition (specs/2026-11-07-desktop-app.md DA2.8). */
export const openPages = {
  manuscript: {
    render: ({ api, navigate, onLogout, route, user }) => (
      <ManuscriptPage api={api} navigate={navigate} onLogout={onLogout} route={route} userName={user.displayName} />
    ),
    preload: importManuscriptPage,
  },
  chat: {
    render: ({ api, navigate, onLogout, route, user }) => (
      <ChatPage api={api} navigate={navigate} onLogout={onLogout} route={route} userName={user.displayName} />
    ),
    preload: importChatPage,
  },
  notes: {
    render: ({ api, navigate, onLogout, route, user }) => <NotesPage apiClient={api} user={user} route={route} navigate={navigate} onLogout={onLogout} />,
    preload: importNotesPage,
  },
  saga: {
    render: ({ api, navigate, onLogout, route, user }) => <SagaPage api={api} navigate={navigate} onLogout={onLogout} route={route} userName={user.displayName} />,
    preload: importSagaPage,
  },
  memory: {
    render: ({ api, navigate, onLogout, route, user }) => <AuthorMemoryPage api={api} navigate={navigate} onLogout={onLogout} route={route} userName={user.displayName} />,
    preload: importAuthorMemoryPage,
  },
} satisfies Record<string, EditionPage>;
