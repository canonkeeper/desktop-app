import { loadFailureWords } from '../../entities/api-errors';
import { ErrorBlock } from '../../ui/feedback/ErrorBlock';
import { SagaMissing } from '../../ui/feedback/SagaMissing';
import { useQuery } from '@tanstack/react-query';
import { Library } from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { chatSessionsQueryOptions } from '../../entities/chat/api';
import {
  type ManuscriptApiClient,
  textSourceView,
  useIndexingStatusQuery,
  useManuscriptShelfQuery,
} from '../../entities/manuscript/api';
import type { SagaApiClient } from '../../entities/saga/api';
import { toHref, type AppRoute, type RouteNavigate } from '../../shared/navigation/app-route';
import { EmptyState } from '../../ui/feedback/EmptyState';
import { SkeletonArea } from '../../ui/feedback/Skeleton';
import { WorkplaceHeading } from '../../ui/shell/PageHead';
import { useVoicePlace } from '../../shared/navigation/voice-place';
import { waitingLine } from '../../entities/manuscript/preparation-copy';
import { PreparationNotice } from './PreparationNotice';
import { ResearchChatPanel, type ChatSurfaceApi } from './ResearchChatPanel';
import { useChatSourceNavigation } from './use-chat-source-navigation';
import { useChatImageNavigation } from './use-chat-image-navigation';
import { WhatNext } from './WhatNext';
import { latestChat } from './chat-days';
import styles from './ChatPage.module.css';

type ChatPageApiClient = ChatSurfaceApi & ManuscriptApiClient & SagaApiClient;

interface ChatPageProps {
  api: ChatPageApiClient;
  navigate: RouteNavigate;
  route: AppRoute;
}

/**
 * «Чат» (/chat): the chats of the saga of the address and the feed of the open one. The top bar, the sections, the saga and
 * its menu are the shell's (specs/2026-11-10-ux2-shell-core.md SC1.2); the list of chats and the feed are the content.
 */
export function ChatPage({ api, navigate, route }: ChatPageProps) {
  const sourceNavigation = useChatSourceNavigation(route, navigate);
  const imageNavigation = useChatImageNavigation(route, navigate);
  const requestedProjectId = route.query.get('projectId');
  const selectedChatId = route.query.get('chatId') ?? '';
  const workspaceQuery = useManuscriptShelfQuery(api, { projectId: requestedProjectId });
  const workspace = workspaceQuery.data;
  const project = workspace?.project ?? null;
  const projectId = project?.id ?? requestedProjectId ?? '';
  const sourceView = textSourceView;
  const invalidProject = Boolean(workspace && requestedProjectId && project?.id !== requestedProjectId);
  const routeInvalid = Boolean(route.invalidQuery);
  const routeBlocked = routeInvalid || invalidProject;
  const sidebarProject = invalidProject ? null : project;
  // The voice call knows the chat open on the page: a deep question goes into it (specs/2026-11-03-voice-assistant.md V2.2).
  useVoicePlace(sidebarProject?.id, { page: 'chat', ...(selectedChatId ? { chatId: selectedChatId } : {}) });
  const indexingStatusQuery = useIndexingStatusQuery(api, projectId, {
    enabled: Boolean(project && !routeBlocked), follow: true,
  });
  const indexingStatus = indexingStatusQuery.data;
  const sourceViewUnavailable = indexingStatusQuery.isSuccess && indexingStatus?.[sourceView]?.exists !== true;
  const sourceViewCheckPending = indexingStatusQuery.isPending;
  const sourceViewCheckError = indexingStatusQuery.error;
  const sourceViewAvailable = indexingStatusQuery.isSuccess && !sourceViewUnavailable;
  // The advice of the reading of the saga over the chat (specs/2026-11-10-ux3-saga-state.md SS3.1).
  const reading = indexingStatus && (indexingStatus.preparation || indexingStatus[sourceView]?.context?.state === 'partial')
    ? indexingStatus
    : null;
  // While the reading waits for the helper no question goes, for the reason the line says (specs/2026-11-12-desk-window.md WN3.3).
  const unavailable = waitingLine(indexingStatus?.preparation);

  // A chat the author picked, or the new chat he asked for: the address says which (AA2.1).
  const selectChatId = useCallback((chatId: string) => {
    navigate('/chat', chatId ? { projectId: projectId || undefined, chatId } : { projectId: projectId || undefined, new: '1' });
  }, [navigate, projectId]);

  function openManuscript() {
    navigate('/manuscript/books', {
      projectId: projectId || undefined,
    });
  }

  // «Что помнит помощник» of the saga, for what a turn remembered (specs/2026-11-10-ux1-chat.md CH4.2).
  const memoryProjectId = project?.id;
  const memoryLink = useMemo(() => memoryProjectId ? {
    href: toHref('/memory', { projectId: memoryProjectId }),
    onOpen: () => navigate('/memory', { projectId: memoryProjectId }),
  } : undefined, [memoryProjectId, navigate]);
  const showChat = Boolean(workspace && project && !routeBlocked && sourceViewAvailable);
  // /chat without a chat opens the latest conversation of the saga, by the time of its last question (AA2.1); a new chat
  // the author asked for and a question brought from another section stay a new chat.
  const wantsLatest = showChat && !selectedChatId && route.query.get('new') !== '1' && !route.chatQuestion;
  // A list that did not open leaves the page to the panel, which says so with its retry (PR3): no wait of retries here.
  const chats = useQuery({ ...chatSessionsQueryOptions(api, { projectId, sourceView }), enabled: wantsLatest, retry: false });
  const latest = wantsLatest && chats.data ? latestChat(chats.data.data) : undefined;
  useEffect(() => {
    if (latest) navigate('/chat', { projectId, chatId: latest.id }, { replace: true });
  }, [latest, navigate, projectId]);
  // While the list is read the page does not show a new chat it is about to leave — once: a list read again by the panel
  // (after a failure it reads it with its own retry) does not take the panel away.
  const [listSeen, setListSeen] = useState(false);
  if (wantsLatest && !chats.isPending && !listSeen) setListSeen(true);
  if (!wantsLatest && listSeen) setListSeen(false);
  const findingLatest = wantsLatest && chats.isPending && !listSeen;
  const fromChat = { section: 'chat' } as const;
  // The row of the panel with the heading in it (specs/2026-11-11-ux2-polish.md PL2.1): the panel puts «Новый чат», «Чаты»
  // and the actions of the open chat into it. The heading is one element from the loading on (PL2.4).
  const [row, setRow] = useState<HTMLDivElement | null>(null);
  const showPanel = Boolean(workspace && project && !routeBlocked && sourceViewAvailable && !findingLatest);

  return (
        <section className={styles.content} aria-labelledby="chat-page-title" data-state={showPanel ? 'chat' : 'notice'}>
          <div className={styles.frame} data-chat-panel={showPanel ? '' : undefined}>
          <div className={styles.row}>
            <WorkplaceHeading data-chat-heading="" id="chat-page-title" surface="chat" />
            <div className={styles.rowSlot} ref={setRow} />
          </div>

          {workspaceQuery.isPending ? <SkeletonArea shape="cards" /> : null}
          {workspaceQuery.error ? (
            <ErrorBlock {...loadFailureWords(workspaceQuery.error)} onRetry={() => void workspaceQuery.refetch()} retrying={workspaceQuery.isRefetching}
              title="Не открылся чат" />
          ) : null}
          {/* An address that is not there and a saga that is not there: the one block with one way on (PR3.5). */}
          {workspace && routeInvalid ? (
            <ErrorBlock action={{ label: 'К списку чатов', onClick: () => navigate('/chat', { projectId: projectId || undefined, chatId: undefined }) }}
              cause="Адрес устарел или в нём опечатка." title="Такого чата нет" />
          ) : null}
          {/* A saga that is not his: a calm block with his sagas to open (specs/2026-11-11-ux9-shelf-chat.md SC1.9). */}
          {workspace && invalidProject ? (
            <SagaMissing onChoose={(id) => navigate('/chat', { projectId: id })} sagas={workspace.projects} />
          ) : null}
          {workspace && project && !routeBlocked && sourceViewCheckPending ? <SkeletonArea shape="cards" /> : null}
          {workspace && project && !routeBlocked && sourceViewCheckError ? (
            <ErrorBlock {...loadFailureWords(sourceViewCheckError)} onRetry={() => void indexingStatusQuery.refetch()} retrying={indexingStatusQuery.isRefetching} title="Не открылся чат" />
          ) : null}
          {workspace && project && !routeBlocked && !sourceViewCheckPending && !sourceViewCheckError && sourceViewUnavailable ? (
            // Without a book the first question cannot be asked: one line of what is needed and the way to the books (PS2.1).
            <EmptyState
              icon={<Library size={28} />}
              need={<>{workspace.books.length ? 'Вопросы можно задавать, когда книга будет прочитана' : 'Сначала загрузите книгу'} — <a href={toHref('/manuscript/books', { projectId: projectId || undefined })}
                onClick={(event) => { event.preventDefault(); openManuscript(); }}>к книгам</a></>}
              title="Здесь будут вопросы о вашей саге"
              why="Ответы — по тексту ваших книг, со ссылками на главы."
            />
          ) : null}
          {findingLatest ? <SkeletonArea shape="cards" /> : null}
          {showPanel && workspace && project ? (
            <ResearchChatPanel
              api={api}
              authorId={project.currentMembership.userId}
              canCreate={workspace.canEditManuscript}
              demo={project.demo === true}
              illustrationRoutes={{
                imageNavigation,
                // A link of an answer to «Рисование» brings «← К чату» there (specs/2026-11-10-ux2-patterns-states.md PS4.1).
                onOpenDrawing: (drawingId) => navigate('/drawing', { projectId: project.id, drawing: drawingId }, { from: fromChat }),
                onOpenWorkshop: (target, idea) => target === 'new'
                  ? navigate('/drawing', { projectId: project.id, drawing: 'new' }, { from: fromChat, ...(idea ? { drawingIdea: idea } : {}) })
                  : navigate('/drawing', { projectId: project.id, image: String(target) }, { from: fromChat }),
                onOpenDrawings: () => navigate('/drawing', { projectId: project.id }),
              }}
              initialQuestion={selectedChatId ? undefined : route.chatQuestion}
              key={`${project.id}:${sourceView}`}
              onOpenSource={sourceNavigation.open}
              onOpenDraft={(draftId) => navigate('/writing', { projectId: project.id, draft: draftId, tab: 'check' }, { from: fromChat })}
              memoryLink={memoryLink}
              sourceNavigation={sourceNavigation}
              onSelectChatId={selectChatId}
              onStartIndexing={openManuscript}
              preparation={reading ? <PreparationNotice onOpenBooks={openManuscript} status={reading} /> : undefined}
              unavailable={unavailable}
              projectId={project.id}
              selectedChatId={selectedChatId}
              row={row}
              sourceView={sourceView}
              whatNext={<WhatNext api={api} canEdit={workspace.canEditManuscript} projectId={project.id} sourceView={sourceView} routes={{
                openMap: () => navigate('/saga', { projectId: project.id }),
                draw: (idea) => navigate('/drawing', { projectId: project.id, drawing: 'new' }, idea ? { drawingIdea: idea } : undefined),
                write: () => navigate('/writing', { projectId: project.id }),
              }} />}
            />
          ) : null}
          </div>
        </section>
  );
}
