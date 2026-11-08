import type { Chapter, Project, ReaderReferenceLocator, Schema, SourceView } from '../../../shared/api';
import { now, type ProjectInvitation, type ProjectMembership, type User } from './common';

type InvitationStatus = ProjectInvitation['status'];
type CanonicalSourceLocator = Schema<'CanonicalChapterSourceLocatorV1'>;

export function createSourceAnchor(locator: CanonicalSourceLocator, chapter: Pick<Chapter, 'navigation' | 'paragraphs'>, paragraphIndex = -1): Schema<'SourceNavigationAnchor'> {
  const readerLocator = {
    projectId: locator.projectId, bookId: locator.bookId, chapterId: locator.chapterId,
    targetView: locator.sourceView, revision: locator.sourceRevision,
  };
  const paragraph = chapter.paragraphs[paragraphIndex];
  const version = locator.sourceView === 'draft' ? 'черновик' : 'опубликованная версия';
  const label = `Глава ${chapter.navigation.displayNumber}`;
  return paragraph
    ? { kind: 'paragraph', label: `${label} · абзац ${paragraphIndex + 1} · ${version}`, locator: { ...readerLocator, paragraphId: paragraph.id } }
    : { kind: 'chapter', label: `${label} · ${version}`, locator: readerLocator };
}

const syntheticReferenceQuote = ['Мара первой заметила дым', 'с северной пристани.'].join(' ');

export const syntheticChapterExcerpt = ['Белый порт впервые', 'показался...'].join(' ');
export const projectId = 'project-white-port';
export const foreignProjectId = 'project-foreign-archive';
export const bookId = 'book-02';
export const draftBookId = 'book-03';
export const chapterId = 'chapter-12';
export const draftChapterId = 'chapter-16';
export const paragraphId = 'p-12-03';
export const mockPassword = 'white-port-12';

function createProjectInvitationFixture(
  invitationProjectId: string,
  invitationId: string,
  status: InvitationStatus,
  email = 'collaborator@example.test',
  role: ProjectInvitation['role'] = 'editor',
  projectTitle = 'Белый порт',
  inviter: Pick<User, 'displayName' | 'email'> = { displayName: 'Мира Волкова', email: 'mira@example.com' },
): ProjectInvitation {
  return {
    id: invitationId,
    projectId: invitationProjectId,
    project: {
      id: invitationProjectId,
      title: projectTitle,
    },
    inviter: {
      displayName: inviter.displayName,
      email: inviter.email,
    },
    email,
    role,
    status,
    expiresAt: '2026-06-30T05:00:00.000Z',
    acceptedAt: status === 'accepted' ? now : null,
    createdAt: now,
    updatedAt: now,
  };
}
function createProjectMembershipFixture(
  project: Pick<Project, 'id'>,
  user: User,
  memberId: string,
  role: ProjectMembership['role'],
  email = 'collaborator@example.test',
  displayName = 'Соавтор',
): ProjectMembership {
  const isCurrentUser = memberId === 'member-owner' || memberId === 'member-current';
  return {
    id: memberId,
    projectId: project.id,
    userId: isCurrentUser ? user.id : `user-${memberId}`,
    email: isCurrentUser ? user.email : email,
    displayName: isCurrentUser ? user.displayName : displayName,
    role,
    status: 'active',
    createdAt: now,
    updatedAt: now,
  };
}

function createProjectMembershipForUser(project: Pick<Project, 'id'>, user: User, memberId: string, role: ProjectMembership['role']): ProjectMembership {
  return {
    id: memberId,
    projectId: project.id,
    userId: user.id,
    email: user.email,
    displayName: user.displayName,
    role,
    status: 'active',
    createdAt: now,
    updatedAt: now,
  };
}

function createReaderLocator(sourceView: SourceView = 'published'): ReaderReferenceLocator {
  return {
    projectId,
    bookId,
    chapterId,
    paragraphId,
    targetView: sourceView,
    revision: sourceView === 'draft' ? 8 : 7,
    range: { startOffset: 0, endOffset: syntheticReferenceQuote.length, quote: syntheticReferenceQuote },
  };
}

function createCanonicalSourceLocator(sourceView: SourceView): CanonicalSourceLocator {
  return {
    projectId,
    bookId,
    chapterId,
    sourceRef: chapterId,
    sourceView,
    sourceRevision: sourceView === 'draft' ? 8 : 7,
    sourceGeneration: '1111111111111111111111111111111111111111111111111111111111111111',
    contentHash: '2222222222222222222222222222222222222222222222222222222222222222',
  };
}

export {
  createCanonicalSourceLocator,
  createProjectInvitationFixture,
  createProjectMembershipFixture,
  createProjectMembershipForUser,
  createReaderLocator,
};
