import type { OperationId, OperationRequest, OperationResponse, RequiredIdempotencyOptions, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone } from './common';
import type { MockRepositoryContext } from './context';
import { mockImagePNG } from './gallery-state';
import { mockNewAuthorWriterId, mockPlatformAdminWriterId } from './admin-writers';
import { lifetime, mockPromoAnswerLink, mockPromoRevokeLink, type MockPromoPermission } from './promo-state';

export { mockPromoAnswerLink, mockPromoRevokeLink } from './promo-state';

type PromoPlace = Schema<'PromoPlace'>;
type PromoPermission = Schema<'PromoPermission'>;
type AdminPromoPermission = Schema<'AdminPromoPermission'>;

const placeNames: Record<PromoPlace, string> = {
  blog_author_today: 'блог CanonKeeper на author.today',
  canonkeeper_home: 'главная страница canonkeeper.ru',
  canonkeeper_social: 'соцсети CanonKeeper',
};
const months = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня', 'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря'];

function moscowDay(at: string) {
  const day = new Date(Date.parse(at) + 3 * 60 * 60 * 1000);
  return `${day.getUTCDate()} ${months[day.getUTCMonth()]} ${day.getUTCFullYear()} года`;
}

/** The text of a permission as the backend writes it (domain/promo.Terms), shortened to its parts. */
function termsOf(permission: MockPromoPermission, state: Schema<'PromoPermissionState'>) {
  const attribution = permission.status === 'pending'
    ? 'Подпись автора — та, которую вы выберете при ответе: имя или псевдоним либо без имени.'
    : permission.attribution ? `Материалы публикуются с подписью «${permission.attribution}».` : 'Материалы публикуются без имени автора.';
  return [
    'Разрешение на использование материалов для продвижения CanonKeeper. Редакция от 31 октября 2026 года.',
    `Вы, автор саги «${permission.sagaName}», разрешаете CanonKeeper — сайту canonkeeper.ru и его оператору — использовать рисунки и фрагменты текста саги, чтобы рассказывать о сервисе CanonKeeper. Где: ${permission.places.map((place) => placeNames[place]).join('; ')}.`,
    'Рисунки: все рисунки галереи саги — и те, что уже есть, и те, что появятся, пока разрешение действует; спрашивать о каждом новом рисунке CanonKeeper не будет. Изображения, загруженные в галерею, и эскизы стиля разрешение не охватывает. Рисунок, удалённый из галереи, CanonKeeper для новых публикаций не возьмёт.',
    'Фрагменты текста: CanonKeeper выбирает их сам, каждый не длиннее 3 500 знаков, все вместе — не больше 10 % текста саги. Каждый взятый фрагмент вы видите на странице этого разрешения. Текст целиком разрешение не охватывает.',
    'Разрешение неисключительное и безвозмездное: все права на текст и рисунки остаются у вас, вы можете использовать их сами и разрешать другим; CanonKeeper ничего за это не платит и ничего не берёт.',
    attribution,
    'Фрагменты публикуются дословно: их можно сократить с пометкой пропуска […], но нельзя дописать или изменить смысл. Рисунки публикуются целиком или с обрезкой краёв для оформления.',
    'Отозвать разрешение можно в любой момент: в CanonKeeper — «Разрешения на продвижение» в меню аккаунта, без аккаунта — по ссылке из письма. После отзыва CanonKeeper не публикует эти материалы заново и в течение 14 дней убирает уже опубликованные со своих страниц: главной страницы, блога и соцсетей CanonKeeper. Копии, которые сделали другие люди (например, репосты), CanonKeeper убрать не может.',
    ...(state === 'pending' || state === 'expired'
      ? [`Запрос действует до ${moscowDay(permission.expiresAt)}. Без ответа до этого дня разрешения нет и ничего не публикуется.`] : []),
    'Имя или псевдоним автора CanonKeeper публикует только по этому разрешению. Как CanonKeeper обрабатывает персональные данные, сказано в Политике обработки персональных данных (canonkeeper.ru/legal/privacy).',
  ];
}

function revocationOf(permission: MockPromoPermission) {
  return [
    'Отзыв разрешения на использование материалов для продвижения CanonKeeper. Редакция от 31 октября 2026 года.',
    `Вы отзываете разрешение${permission.answeredAt ? ` от ${moscowDay(permission.answeredAt)}` : ''} на использование материалов саги «${permission.sagaName}» для продвижения CanonKeeper.`,
    'С момента отзыва CanonKeeper не публикует эти материалы заново и в течение 14 дней убирает уже опубликованные со своих страниц: главной страницы, блога и соцсетей CanonKeeper. Копии, которые сделали другие люди, CanonKeeper убрать не может.',
    'Отзыв окончательный: чтобы снова разрешить, дождитесь нового запроса CanonKeeper.',
  ];
}

function stateOf(permission: MockPromoPermission): Schema<'PromoPermissionState'> {
  return permission.status === 'pending' && Date.parse(permission.expiresAt) <= Date.now() ? 'expired' : permission.status;
}

/** The permissions for the promotion of CanonKeeper (specs/2026-10-30-promo-permissions.md). */
export function createPromoPermissionOperations(context: MockRepositoryContext) {
  const permissions = () => context.state.promoPermissions;

  function covered(image: (typeof context.state.gallery.images)[number]) {
    return image.status === 'ready' && image.kind !== 'upload' && !image.sketch;
  }

  /** The gallery tiles a permission covers, the newest first (R1.4). */
  function tiles(projectId: string) {
    const branches = new Map<number, number>();
    for (const image of context.state.gallery.images) {
      if (image.projectId === projectId && covered(image)) branches.set(image.branch, Math.max(branches.get(image.branch) ?? 0, image.number));
    }
    return [...branches.values()].sort((left, right) => right - left);
  }

  function view(permission: MockPromoPermission, image: (number: number) => string | null, hideText = false, shown = 24): PromoPermission {
    const state = stateOf(permission);
    const numbers = state === 'pending' || state === 'granted' ? tiles(permission.projectId) : [];
    return {
      id: permission.id, projectId: permission.projectId, sagaName: permission.sagaName, state, illustrationCount: numbers.length,
      illustrations: numbers.slice(0, shown).map((number) => ({ number, image: image(number), original: null })),
      fragments: permission.fragments.map((fragment) => ({
        key: fragment.key, label: fragment.label, from: fragment.from, to: fragment.to,
        characters: [...fragment.text].length, text: hideText ? null : fragment.text,
      })),
      places: [...permission.places], proposedAttribution: permission.proposedAttribution, attribution: permission.attribution,
      message: permission.message, termsVersion: '2026-10-31', terms: termsOf(permission, state),
      revocation: state === 'granted' ? revocationOf(permission) : [],
      createdAt: permission.createdAt, expiresAt: permission.expiresAt, answeredAt: permission.answeredAt, revokedAt: permission.revokedAt,
    };
  }

  function adminView(permission: MockPromoPermission): AdminPromoPermission {
    const granted = stateOf(permission) === 'granted';
    const original = (number: number) => `/api/v1/admin/promo-permissions/${permission.id}/illustrations/${number}`;
    const seen = view(permission, (number) => `${original(number)}?size=thumb`, !granted, granted ? Number.MAX_SAFE_INTEGER : 0);
    return {
      ...seen,
      illustrations: seen.illustrations.map((drawing) => ({ ...drawing, original: original(drawing.number) })),
      ownerUserId: permission.ownerUserId, sagaDeleted: false, recipient: clone(permission.recipient),
    };
  }

  function ownView(permission: MockPromoPermission) {
    return view(permission, (number) => `/api/v1/projects/${permission.projectId}/images/${number}/file?size=thumb&v=0`);
  }

  function adminOperation(operationId: OperationId) {
    context.applyOperation(operationId);
    if (!context.state.session || context.state.session.user.impersonation) {
      throw new MockApiError(403, 'Forbidden', operationId, 'admin.forbidden');
    }
  }

  function writerOf(userId: string, operationId: OperationId) {
    const asked = permissions().find((permission) => permission.ownerUserId === userId && permission.recipient.kind === 'writer');
    if (asked) return { id: userId, admin: false, email: asked.recipient.email };
    if (userId !== mockPlatformAdminWriterId && userId !== mockNewAuthorWriterId) {
      throw new MockApiError(404, 'Resource not found', operationId, 'not_found');
    }
    return { id: userId, admin: userId === mockPlatformAdminWriterId, email: userId === mockPlatformAdminWriterId ? 'mira@example.com' : 'new-author@example.test' };
  }

  function chaptersOf(projectId: string) {
    const books = new Map(context.state.books.filter((book) => book.projectId === projectId).map((book) => [book.id, book]));
    return context.state.chapters
      .filter((chapter) => books.has(chapter.bookId) && chapter.contentVariant === 'published')
      .map((chapter) => ({ chapter, book: books.get(chapter.bookId)!.order, key: `b${books.get(chapter.bookId)!.order}c${chapter.order}` }));
  }

  /** The characters of the fragments the granted permission of a saga holds (R1.5). */
  function takenCharacters(projectId: string) {
    return permissions().filter((permission) => permission.projectId === projectId && permission.status === 'granted')
      .flatMap((permission) => permission.fragments).reduce((total, fragment) => total + [...fragment.text].length, 0);
  }

  // The mock saga is short: a tenth of a whole saga of 1.2 million words is far above any fragment.
  function charactersLeft(projectId: string) {
    const characters = chaptersOf(projectId).reduce((total, { chapter }) => total + chapter.paragraphs.reduce((sum, paragraph) => sum + [...paragraph.text].length, 0), 0);
    return Math.max(0, Math.max(35_000, Math.floor(characters / 10)) - takenCharacters(projectId));
  }

  async function getAdminPromoSagas(userId: string): Promise<OperationResponse<'getAdminPromoSagas', '200'>> {
    adminOperation('getAdminPromoSagas');
    const writer = writerOf(userId, 'getAdminPromoSagas');
    return clone({
      data: context.state.projects.map((project) => {
        const chapters = chaptersOf(project.id);
        return {
          projectId: project.id, name: project.title, recipient: writer.admin ? 'email' as const : 'writer' as const, textReady: chapters.length > 0,
          chapters: chapters.map(({ chapter, book, key }) => ({ key, title: chapter.title, book, paragraphs: chapter.paragraphs.length })),
          charactersLeft: charactersLeft(project.id),
        };
      }),
    });
  }

  function invalid(operationId: OperationId, detail: string): never {
    throw new MockApiError(400, detail, operationId, 'promo.invalid');
  }

  async function createAdminPromoPermission(
    userId: string, body: OperationRequest<'createAdminPromoPermission'>, options: RequiredIdempotencyOptions,
  ): Promise<OperationResponse<'createAdminPromoPermission', '201'>> {
    const operationId = 'createAdminPromoPermission';
    adminOperation(operationId);
    const writer = writerOf(userId, operationId);
    return context.idempotentCommand(operationId, userId, options, body, () => {
      const project = context.state.projects.find((candidate) => candidate.id === body.projectId);
      if (!project) throw new MockApiError(404, 'Resource not found', operationId, 'not_found');
      const email = body.recipientEmail?.trim().toLowerCase() ?? '';
      if (writer.admin && !email) invalid(operationId, 'о своей саге спросите автора книг по адресу почты');
      if (!writer.admin && email) invalid(operationId, 'по адресу почты можно спросить только о саге администратора');
      if (email && !/^[^@\s]+@[^@\s]+$/.test(email)) invalid(operationId, 'неверный адрес почты автора');
      if (body.places.length === 0) invalid(operationId, 'не выбрано, где показывать материалы');
      // In the mock every account has the same sagas: a saga is the same one only under the same account.
      if (permissions().some((permission) => permission.projectId === project.id && permission.ownerUserId === userId && ['pending', 'granted'].includes(stateOf(permission)))) {
        invalid(operationId, 'об этой саге уже есть запрос или разрешение — нового не нужно');
      }
      const permission: MockPromoPermission = {
        id: `promo-${permissions().length + 1}`, projectId: project.id, sagaName: project.title, ownerUserId: userId,
        recipient: email ? { kind: 'email', userId: null, email } : { kind: 'writer', userId: writer.id, email: writer.email },
        status: 'pending', fragments: [],
        places: [...body.places], proposedAttribution: body.attribution?.trim() || null, attribution: null,
        message: body.message?.trim() || null, createdAt: new Date().toISOString(),
        expiresAt: new Date(Date.now() + lifetime).toISOString(), answeredAt: null, revokedAt: null,
        answerLink: email ? mockPromoAnswerLink : null, revokeLink: null,
      };
      permissions().unshift(permission);
      return adminView(permission);
    });
  }

  async function listAdminPromoPermissions(): Promise<OperationResponse<'listAdminPromoPermissions', '200'>> {
    adminOperation('listAdminPromoPermissions');
    return { data: permissions().map(adminView) };
  }

  async function takeAdminPromoFragment(permissionId: string, body: OperationRequest<'takeAdminPromoFragment'>): Promise<OperationResponse<'takeAdminPromoFragment', '200'>> {
    const operationId = 'takeAdminPromoFragment';
    adminOperation(operationId);
    const permission = permissions().find((candidate) => candidate.id === permissionId);
    if (!permission) throw new MockApiError(404, 'Resource not found', operationId, 'not_found');
    if (stateOf(permission) !== 'granted') throw new MockApiError(409, 'the permission is not granted', operationId, 'promo.not_granted');
    const found = chaptersOf(permission.projectId).find(({ key }) => key === body.chapter.trim());
    if (!found) invalid(operationId, `нет главы «${body.chapter}»`);
    if (body.from < 1 || body.to < body.from || body.to > found.chapter.paragraphs.length) {
      invalid(operationId, `в главе «${found.chapter.title}» абзацев ${found.chapter.paragraphs.length}`);
    }
    if (!permission.fragments.some((fragment) => fragment.key === found.key && fragment.from === body.from && fragment.to === body.to)) {
      const text = found.chapter.paragraphs.slice(body.from - 1, body.to).map((paragraph) => paragraph.text.trim()).join('\n\n');
      if ([...text].length > 3500) invalid(operationId, `фрагмент длиннее 3500 знаков (${[...text].length})`);
      if ([...text].length > charactersLeft(permission.projectId)) invalid(operationId, 'фрагменты саги — не больше 10 % её текста');
      permission.fragments.push({ key: found.key, label: `Том ${found.book}, ${found.chapter.title}`, from: body.from, to: body.to, text });
    }
    return adminView(permission);
  }

  async function getAdminPromoIllustration(permissionId: string, number: number) {
    adminOperation('getAdminPromoIllustration');
    const permission = permissions().find((candidate) => candidate.id === permissionId);
    const image = context.state.gallery.images.find((candidate) => candidate.projectId === permission?.projectId && candidate.number === number);
    if (!permission || stateOf(permission) !== 'granted' || !image || !covered(image)) {
      throw new MockApiError(404, 'Resource not found', 'getAdminPromoIllustration', 'not_found');
    }
    return { content: mockImagePNG, mimeType: 'image/png', cacheControl: 'no-store', fileName: `canonkeeper-${number}.png` };
  }

  function session(operationId: OperationId) {
    context.applyOperation(operationId);
    const user = context.state.session?.user;
    if (!user) throw new MockApiError(401, 'authentication required', operationId, 'auth.session_required');
    return user;
  }

  function own(operationId: OperationId, permissionId: string) {
    const user = session(operationId);
    if (user.impersonation) throw new MockApiError(403, 'only the author himself answers', operationId, 'promo.own_session_required');
    const permission = permissions().find((candidate) => candidate.id === permissionId && candidate.recipient.userId === user.id);
    if (!permission) throw new MockApiError(404, 'Resource not found', operationId, 'not_found');
    return permission;
  }

  function answer(operationId: OperationId, permission: MockPromoPermission, body: { decision: 'allow' | 'decline'; attribution: string | null }) {
    if (stateOf(permission) !== 'pending') {
      throw new MockApiError(409, 'the request is not waiting for an answer', operationId, 'promo.not_pending');
    }
    const attribution = body.attribution?.trim() ?? null;
    if (body.decision === 'allow' && attribution !== null && (attribution.length === 0 || attribution.length > 100)) {
      invalid(operationId, 'подпись — от 1 до 100 знаков в одну строку');
    }
    permission.status = body.decision === 'allow' ? 'granted' : 'declined';
    permission.attribution = body.decision === 'allow' ? attribution : null;
    permission.answeredAt = new Date().toISOString();
    permission.answerLink = null;
    permission.revokeLink = body.decision === 'allow' && permission.recipient.kind === 'email' ? mockPromoRevokeLink : null;
  }

  function revoke(operationId: OperationId, permission: MockPromoPermission) {
    if (permission.status !== 'granted') throw new MockApiError(409, 'the permission is not granted', operationId, 'promo.not_granted');
    permission.status = 'revoked';
    permission.revokedAt = new Date().toISOString();
    permission.revokeLink = null;
  }

  async function listPromoPermissions(): Promise<OperationResponse<'listPromoPermissions', '200'>> {
    const user = session('listPromoPermissions');
    return { data: permissions().filter((permission) => permission.recipient.userId === user.id).map(ownView) };
  }

  async function answerPromoPermission(
    permissionId: string, body: OperationRequest<'answerPromoPermission'>,
  ): Promise<OperationResponse<'answerPromoPermission', '200'>> {
    const permission = own('answerPromoPermission', permissionId);
    answer('answerPromoPermission', permission, body);
    return ownView(permission);
  }

  async function revokePromoPermission(permissionId: string): Promise<OperationResponse<'revokePromoPermission', '200'>> {
    const permission = own('revokePromoPermission', permissionId);
    revoke('revokePromoPermission', permission);
    return ownView(permission);
  }

  function byLink(operationId: OperationId, link: string) {
    context.applyOperation(operationId);
    const found = permissions().find((permission) =>
      (permission.answerLink === link && stateOf(permission) === 'pending') || permission.revokeLink === link);
    if (!found || !link) throw new MockApiError(400, 'link is invalid or expired', operationId, 'promo.link_invalid');
    return found;
  }

  const linkImage = () => `data:image/png;base64,${btoa(String.fromCharCode(...mockImagePNG))}`;

  async function readPromoLink(body: OperationRequest<'readPromoLink'>): Promise<OperationResponse<'readPromoLink', '200'>> {
    const permission = byLink('readPromoLink', body.token);
    return { purpose: permission.answerLink === body.token ? 'answer' : 'revoke', permission: view(permission, linkImage) };
  }

  async function answerPromoLink(body: OperationRequest<'answerPromoLink'>): Promise<OperationResponse<'answerPromoLink', '200'>> {
    const permission = byLink('answerPromoLink', body.token);
    if (permission.answerLink !== body.token) throw new MockApiError(400, 'link is invalid or expired', 'answerPromoLink', 'promo.link_invalid');
    answer('answerPromoLink', permission, body);
    return view(permission, () => null);
  }

  async function revokePromoLink(body: OperationRequest<'revokePromoLink'>): Promise<OperationResponse<'revokePromoLink', '200'>> {
    const permission = byLink('revokePromoLink', body.token);
    if (permission.revokeLink !== body.token) throw new MockApiError(400, 'link is invalid or expired', 'revokePromoLink', 'promo.link_invalid');
    revoke('revokePromoLink', permission);
    return view(permission, () => null);
  }

  return {
    getAdminPromoSagas, createAdminPromoPermission, listAdminPromoPermissions, takeAdminPromoFragment, getAdminPromoIllustration,
    listPromoPermissions, answerPromoPermission, revokePromoPermission, readPromoLink, answerPromoLink, revokePromoLink,
  };
}
