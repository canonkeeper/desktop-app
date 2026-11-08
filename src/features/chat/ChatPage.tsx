import { BookOpen, ChevronDown, Library, LogOut, Menu, X } from 'lucide-react';
import { useCallback, useRef, useState } from 'react';
import { publicApiErrorMessage } from '../../entities/api-errors';
import {
  type ManuscriptApiClient,
  textSourceView,
  useIndexingStatusQuery,
  useManuscriptShelfQuery,
  useSelectManuscriptProject,
} from '../../entities/manuscript/api';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';
import { Button } from '../../ui/controls/Button';
import { TopBarStrip, TopBarTalk } from '../../ui/navigation/TopBarStrip';
import { IconButton } from '../../ui/controls/IconButton';
import { Select } from '../../ui/controls/Select';
import { formatProjectMeta } from '../../ui/copy';
import { Alert } from '../../ui/feedback/Alert';
import { EmptyState } from '../../ui/feedback/EmptyState';
import { Drawer } from '../../ui/overlays/Drawer';
import { Popover } from '../../ui/overlays/Popover';
import { ProjectNavigationItems, ProjectSidebar } from '../../ui/ProjectSidebar';
import { AccountActingMark, AccountSessionItems, AccountUnreadMark, useAccountButtonLabel } from '../../ui/navigation/AccountSession';
import { useBookNavigation } from '../../entities/manuscript/useBookNavigation';
import { NotesButton } from '../../ui/NotesButton';
import { useNotes } from '../../shared/notes/notes-context';
import { useVoicePlace } from '../../shared/navigation/voice-place';
import { PreparationNotice } from './PreparationNotice';
import { ResearchChatPanel, type ChatSurfaceApi } from './ResearchChatPanel';
import { useChatSourceNavigation } from './use-chat-source-navigation';
import { useChatImageNavigation } from './use-chat-image-navigation';
import styles from './ChatPage.module.css';

type ChatPageApiClient = ChatSurfaceApi & ManuscriptApiClient;

interface ChatPageProps {
  api: ChatPageApiClient;
  navigate: RouteNavigate;
  onLogout?: () => void;
  route: AppRoute;
  userName: string;
}

export function ChatPage({ api, navigate, onLogout, route, userName }: ChatPageProps) {
  const sourceNavigation = useChatSourceNavigation(route, navigate);
  const imageNavigation = useChatImageNavigation(route, navigate);
  const notes = useNotes();
  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const accountButtonRef = useRef<HTMLButtonElement>(null);
  const accountLabel = useAccountButtonLabel();
  const [projectDrawerOpen, setProjectDrawerOpen] = useState(false);
  const requestedProjectId = route.query.get('projectId');
  const selectedChatId = route.query.get('chatId') ?? '';
  const workspaceQuery = useManuscriptShelfQuery(api, { projectId: requestedProjectId });
  const selectProjectMutation = useSelectManuscriptProject(api);
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
  const bookNavigation = useBookNavigation({
    api, navigate, projectId: sidebarProject?.id,
    bookId: !routeBlocked && workspace?.books.some((book) => book.id === route.query.get('bookId'))
      ? route.query.get('bookId') ?? undefined : undefined,
  });
  const shortcuts = {
    onOpenReader: () => { setProjectDrawerOpen(false); bookNavigation.onOpenReader(); },
    onOpenNotes: () => { setProjectDrawerOpen(false); navigate('/notes'); },
    onOpenSaga: () => { setProjectDrawerOpen(false); navigate('/saga', { projectId: sidebarProject?.id }); },
    onOpenWriting: () => { setProjectDrawerOpen(false); navigate('/writing', { projectId: sidebarProject?.id }); },
    onOpenDrawing: () => { setProjectDrawerOpen(false); navigate('/drawing', { projectId: sidebarProject?.id }); },
    onOpenGallery: () => { setProjectDrawerOpen(false); navigate('/gallery', { projectId: sidebarProject?.id }); },
    onCreateNote: notes ? () => { setProjectDrawerOpen(false); notes.capture({ body: '' }); } : undefined,
  };
  const indexingStatusQuery = useIndexingStatusQuery(api, projectId, {
    enabled: Boolean(project && !routeBlocked), follow: true,
  });
  const indexingStatus = indexingStatusQuery.data;
  const sourceViewUnavailable = indexingStatusQuery.isSuccess && indexingStatus?.[sourceView]?.exists !== true;
  const sourceViewCheckPending = indexingStatusQuery.isPending;
  const sourceViewCheckError = indexingStatusQuery.error;
  const sourceViewAvailable = indexingStatusQuery.isSuccess && !sourceViewUnavailable;
  const preparation = indexingStatus?.[sourceView]?.context;

  const selectChatId = useCallback((chatId: string) => {
    navigate('/chat', {
      projectId: projectId || undefined,
      chatId: chatId || undefined,
    });
  }, [navigate, projectId]);

  function openManuscript() {
    setProjectDrawerOpen(false);
    navigate('/manuscript/books', {
      projectId: projectId || undefined,
    });
  }

  function selectProject(nextProjectId: string) {
    selectProjectMutation.mutate(nextProjectId, { onSuccess: () => {
      setProjectDrawerOpen(false);
      navigate('/chat', { projectId: nextProjectId });
    } });
  }

  const projectSelector = workspace && sidebarProject ? <><Select label="Сага чата" labelHidden loading={selectProjectMutation.isPending}
    onValueChange={selectProject} value={sidebarProject.id}
    options={workspace.projects.map((item) => ({ label: item.title, value: item.id }))} />
    {selectProjectMutation.isError ? <Alert tone="error">Не удалось сменить сагу.</Alert> : null}</> : undefined;

  const showChat = Boolean(workspace && project && !routeBlocked && sourceViewAvailable);
  const heading = <div className={styles.heading}>
    <div>
      <p>Память саги</p>
      <h1 id="chat-page-title" data-chat-heading="" tabIndex={-1}>Чат</h1>
    </div>
  </div>;

  return (
    <main className={styles.workspace}>
      <header className={styles.topBar}>
        <strong>Canon Keeper</strong>
        <div className={styles.compactNavigation}>
          {project ? (
            <IconButton aria-label="Открыть меню" className={styles.menuButton} onClick={() => setProjectDrawerOpen(true)} size="touch">
              <Menu aria-hidden="true" size={20} />
            </IconButton>
          ) : null}
          <IconButton aria-label="Мои книги" onClick={openManuscript} size="touch">
            <Library aria-hidden="true" size={16} />
          </IconButton>
          {project ? (
            <Button
              aria-label={`Выбрать сагу: ${project.title}`}
              className={styles.scopeButton}
              onClick={() => setProjectDrawerOpen(true)}
              size="touch"
              variant="ghost"
            >
              <span className={styles.scopePill}>
                <BookOpen aria-hidden="true" size={13} />
                <span className={styles.scopeProject}>{project.title}</span>
                <ChevronDown aria-hidden="true" size={13} />
              </span>
            </Button>
          ) : null}
        </div>
        <div className={styles.account}>
          <TopBarTalk />
          <NotesButton />
          {onLogout ? (
            <button
              aria-label={accountLabel}
              className={styles.avatarButton}
              onClick={() => setAccountMenuOpen((open) => !open)}
              ref={accountButtonRef}
              type="button"
            >
              <span className={styles.avatar}>{userName.slice(0, 1)}</span>
              <AccountActingMark />
              <AccountUnreadMark />
            </button>
          ) : null}
        </div>
        {onLogout ? (
          <Popover
            className={styles.accountMenu}
            label="Меню аккаунта"
            onDismiss={() => setAccountMenuOpen(false)}
            open={accountMenuOpen}
          >
            <p className={styles.accountName}>{userName}</p>
            <AccountSessionItems onDone={() => { setAccountMenuOpen(false); accountButtonRef.current?.focus(); }} />
            <Button className={styles.menuItem} onClick={onLogout} size="touch" variant="ghost">
              <LogOut aria-hidden="true" size={17} />
              Выйти
            </Button>
          </Popover>
        ) : null}
      </header>
      <TopBarStrip />

      {projectDrawerOpen && workspace && project ? (
        <Drawer
          canDismiss
          className={styles.projectDrawer}
          label="Навигация саги"
          onDismiss={() => setProjectDrawerOpen(false)}
        >
          <header>
            <h2>Сага</h2>
            <IconButton aria-label="Закрыть навигацию" onClick={() => setProjectDrawerOpen(false)} size="touch">
              <X aria-hidden="true" size={18} />
            </IconButton>
          </header>
          <ProjectSidebar
            {...shortcuts}
            activeSection="chat"
            onOpenChat={() => {
              setProjectDrawerOpen(false);
              selectChatId(selectedChatId);
            }}
            onOpenManuscript={openManuscript}
            projectMeta={formatProjectMeta(project)}
            projectTitle={project.title}
            projectSelector={projectSelector}
            variant="drawer"
          />
        </Drawer>
      ) : null}

      {bookNavigation.picker}
      <div className={styles.body}>
        <div className={styles.desktopSidebar}><ProjectSidebar
          {...shortcuts}
          activeSection="chat"
          onOpenChat={() => selectChatId(selectedChatId)}
          onOpenManuscript={openManuscript}
          projectMeta={sidebarProject ? formatProjectMeta(sidebarProject) : 'Выберите сагу на полке'}
          projectTitle={sidebarProject?.title ?? 'Сага'}
          projectSelector={projectSelector}
        /></div>

        <nav className={styles.tabletRail} aria-label="Планшетная навигация">
          <ProjectNavigationItems {...shortcuts} activeSection="chat" compact
            onOpenChat={() => selectChatId(selectedChatId)} onOpenManuscript={openManuscript} />
        </nav>
        <section className={styles.content} aria-labelledby="chat-page-title" data-state={showChat ? 'chat' : 'notice'}>
          {!showChat ? heading : null}

          {workspaceQuery.isPending ? <p className={styles.status}>Загружаем сагу…</p> : null}
          {workspaceQuery.error ? (
            <Alert tone="error">
              <p>{publicApiErrorMessage(workspaceQuery.error, 'Не удалось загрузить сагу.')}</p>
              <Button onClick={() => void workspaceQuery.refetch()} variant="outline">Повторить</Button>
            </Alert>
          ) : null}
          {workspace && !project && !requestedProjectId ? (
            <EmptyState
              action={<Button onClick={openManuscript} variant="primary">Создать сагу</Button>}
              description="Создайте сагу и загрузите книгу — после этого здесь можно задавать вопросы."
              icon={<BookOpen size={22} />}
              title="Пока нет саги"
            />
          ) : null}
          {workspace && routeInvalid ? (
            <Alert tone="error">
              <p>Неверный маршрут. Сбросьте параметры и попробуйте снова.</p>
              <Button onClick={() => selectChatId('')} variant="outline">К списку чатов</Button>
            </Alert>
          ) : null}
          {workspace && invalidProject ? (
            <Alert tone="error">
              <p>Сага недоступна: доступ к ней мог быть отозван.</p>
              <Button onClick={openManuscript} variant="outline">Мои книги</Button>
            </Alert>
          ) : null}
          {workspace && project && !routeBlocked && sourceViewCheckPending ? <p className={styles.status}>Проверяем книги саги…</p> : null}
          {workspace && project && !routeBlocked && sourceViewCheckError ? (
            <Alert tone="error">
              <p>{publicApiErrorMessage(sourceViewCheckError, 'Не удалось проверить доступность текста для чата.')}</p>
              <Button onClick={() => void indexingStatusQuery.refetch()} variant="outline">Повторить</Button>
            </Alert>
          ) : null}
          {workspace && project && !routeBlocked && !sourceViewCheckPending && !sourceViewCheckError && sourceViewUnavailable ? (
            <EmptyState
              action={<Button onClick={openManuscript} variant="primary">К книгам</Button>}
              description="Загрузите книгу — вопросы можно задавать через минуту-две после загрузки."
              icon={<Library size={22} />}
              title="Книги ещё не загружены или не подготовлены"
            />
          ) : null}
          {workspace && project && !routeBlocked && sourceViewAvailable ? (
            <ResearchChatPanel
              api={api}
              canCreate={workspace.canEditManuscript}
              illustrationRoutes={{
                imageNavigation,
                onOpenDrawing: (drawingId) => navigate('/drawing', { projectId: project.id, drawing: drawingId }),
                onOpenWorkshop: (target, idea) => target === 'new'
                  ? navigate('/drawing', { projectId: project.id, drawing: 'new' }, idea ? { drawingIdea: idea } : undefined)
                  : navigate('/drawing', { projectId: project.id, image: String(target) }),
                onOpenFeaturePage: () => navigate('/gallery', { projectId: project.id }),
                onOpenGallery: () => navigate('/gallery', { projectId: project.id }),
              }}
              initialQuestion={selectedChatId ? undefined : route.chatQuestion}
              key={`${project.id}:${sourceView}`}
              onOpenSource={sourceNavigation.open}
              onOpenDraft={(draftId) => navigate('/writing', { projectId: project.id, draft: draftId, tab: 'check' })}
              sourceNavigation={sourceNavigation}
              onSelectChatId={selectChatId}
              onStartIndexing={openManuscript}
              preparation={preparation ? <PreparationNotice api={api} context={preparation} onOpenBooks={openManuscript} projectId={project.id} /> : undefined}
              projectId={project.id}
              selectedChatId={selectedChatId}
              sourceView={sourceView}
              toolbar={heading}
            />
          ) : null}
        </section>
      </div>
    </main>
  );
}
