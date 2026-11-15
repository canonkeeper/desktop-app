import type { OperationId, OperationRequest, OperationResponse } from '../../../shared/api';
import { clone, normalizedEmail, now, rejectUnknownKeys } from './common';
import type { MockRepositoryContext } from './context';
import { mockPassword } from './fixtures';
import { desktopAuthorId } from './tiers';
import { MockApiError } from '../scenarios';
import { desktopLinked, mockSiteHost } from './desktop-account';

function validateRegisterRequest(body: OperationRequest<'registerUser'>) {
  rejectUnknownKeys('registerUser', body, ['acceptedPersonalDataConsent', 'acceptedTerms', 'displayName', 'email', 'password']);
  if (body.acceptedTerms !== true || body.acceptedPersonalDataConsent !== true) {
    throw new MockApiError(400, 'Registration requires accepted terms and personal data consent.', 'registerUser', 'validation_failed');
  }
  if (typeof body.displayName !== 'string' || body.displayName.trim().length === 0 || body.displayName.length > 120) {
    throw new MockApiError(400, 'Registration displayName is invalid.', 'registerUser', 'validation_failed');
  }
  if (typeof body.email !== 'string' || body.email.trim().length === 0) {
    throw new MockApiError(400, 'Registration email is invalid.', 'registerUser', 'validation_failed');
  }
  if (typeof body.password !== 'string' || body.password.length < 8) {
    throw new MockApiError(400, 'Registration password is invalid.', 'registerUser', 'validation_failed');
  }
}
const loginFailureLimit = 5;
const loginLockMinutes = 15;
/** The address of the writer of the fixtures: registering or moving to it is refused as taken. */
const takenEmail = 'mira@example.com';
/** The local author of the desktop app (backend/internal/services/desktop LocalAuthorEmail, LocalAuthorName). */
const desktopAuthor = {
  id: desktopAuthorId, email: 'local@desktop.invalid', emailVerified: true, documentsAccepted: true, displayName: 'Автор', avatarUrl: null, createdAt: now,
};

/** Mock links carry a deterministic value: `mock-<purpose>-<userId>`. */
export function mockAccountToken(purpose: 'email_verification' | 'password_reset', userId: string) {
  return `mock-${purpose}-${userId}`;
}

export function createSessionOperations(context: MockRepositoryContext) {
  function setUser(user: typeof context.state.user) {
    context.state.user = user;
    if (context.state.session && !context.state.session.user.impersonation) {
      context.state.session = { user };
    }
  }

  // A new address signs in at once, unverified; a taken one is 409 (UC1.1).
  async function register(body: OperationRequest<'registerUser'>): Promise<OperationResponse<'registerUser', '201'>> {
    context.applyOperation('registerUser');
    validateRegisterRequest(body);
    if (normalizedEmail(body.email) === normalizedEmail(context.state.user.email)) {
      throw new MockApiError(409, 'email is already registered', 'registerUser', 'auth.email_taken');
    }
    context.state.user = {
      ...context.state.user, displayName: body.displayName, email: normalizedEmail(body.email), emailVerified: false, documentsAccepted: true,
    };
    context.state.session = { user: context.state.user };
    return clone(context.state.session);
  }

  async function sendEmailVerification(): Promise<OperationResponse<'sendEmailVerification', '202'>> {
    context.applyOperation('sendEmailVerification');
    if (!context.state.session) throw new MockApiError(401, 'Сессия отсутствует.', 'sendEmailVerification');
    if (context.state.session.user.emailVerified) {
      throw new MockApiError(409, 'email address is already verified', 'sendEmailVerification', 'auth.email_already_verified');
    }
    return { status: 'accepted' };
  }

  // The same answer for every address (UC2.1).
  async function requestPasswordReset(body: OperationRequest<'requestPasswordReset'>): Promise<OperationResponse<'requestPasswordReset', '202'>> {
    context.applyOperation('requestPasswordReset');
    rejectUnknownKeys('requestPasswordReset', body, ['email']);
    if (typeof body.email !== 'string' || !body.email.includes('@')) {
      throw new MockApiError(400, 'validation failed', 'requestPasswordReset', 'request.validation');
    }
    return { status: 'accepted' };
  }

  // A reset link signs in, verifies the address and ends other sessions (UC2.2).
  async function resetPassword(body: OperationRequest<'resetPassword'>): Promise<OperationResponse<'resetPassword', '200'>> {
    context.applyOperation('resetPassword');
    rejectUnknownKeys('resetPassword', body, ['newPassword', 'token']);
    if (typeof body.newPassword !== 'string' || body.newPassword.length < 8) {
      throw new MockApiError(400, 'validation failed', 'resetPassword', 'request.validation');
    }
    if (body.token !== mockAccountToken('password_reset', context.state.user.id)) {
      throw new MockApiError(400, 'link is invalid or expired', 'resetPassword', 'auth.token_invalid');
    }
    context.state.user = { ...context.state.user, emailVerified: true };
    context.state.session = { user: context.state.user };
    // A new password opens the sign-in from any address (R2.2.3).
    context.state.loginFailures = {};
    return clone(context.state.session);
  }

  async function confirmEmailVerification(body: OperationRequest<'confirmEmailVerification'>): Promise<OperationResponse<'confirmEmailVerification', '200'>> {
    context.applyOperation('confirmEmailVerification');
    if (body.token !== mockAccountToken('email_verification', context.state.user.id)) {
      throw new MockApiError(400, 'link is invalid or expired', 'confirmEmailVerification', 'auth.token_invalid');
    }
    setUser({ ...context.state.user, emailVerified: true });
    return { status: 'verified' };
  }

  // The server's window of failed sign-ins (AU3.3): five per email, each refusal says what is left, then 429 for the window.
  async function login(body: OperationRequest<'loginUser'>): Promise<OperationResponse<'loginUser', '200'>> {
    context.applyOperation('loginUser');
    const email = normalizedEmail(body.email);
    const failures = context.state.loginFailures[email] ?? 0;
    if (failures >= loginFailureLimit) {
      throw new MockApiError(429, 'rate limit exceeded', 'loginUser', 'rate_limit.exceeded', { retryAfter: loginLockMinutes * 60 });
    }
    if (email !== normalizedEmail(context.state.user.email) || body.password !== mockPassword) {
      context.state.loginFailures[email] = failures + 1;
      throw new MockApiError(401, 'invalid credentials', 'loginUser', 'auth.invalid_credentials', {
        fields: { attemptsLeft: loginFailureLimit - failures - 1, lockMinutes: loginLockMinutes },
      });
    }
    delete context.state.loginFailures[email];
    context.state.session = { user: context.state.user };
    return clone(context.state.session);
  }

  // The fix of an unverified address (AU1.8): the session with the new address; the address of the fixture writer is taken.
  async function changeUnverifiedEmail(body: OperationRequest<'changeUnverifiedEmail'>): Promise<OperationResponse<'changeUnverifiedEmail', '200'>> {
    context.applyOperation('changeUnverifiedEmail');
    rejectUnknownKeys('changeUnverifiedEmail', body, ['email']);
    if (!context.state.session) throw new MockApiError(401, 'authentication required', 'changeUnverifiedEmail', 'auth.session_required');
    if (context.state.session.user.emailVerified) {
      throw new MockApiError(409, 'email address is already verified', 'changeUnverifiedEmail', 'auth.email_already_verified');
    }
    const email = normalizedEmail(body.email);
    if (!/^[^\s@]+@[^\s@]+$/u.test(email)) throw new MockApiError(400, 'validation failed', 'changeUnverifiedEmail', 'request.validation');
    if (email === takenEmail && email !== normalizedEmail(context.state.user.email)) {
      throw new MockApiError(409, 'email is already registered', 'changeUnverifiedEmail', 'auth.email_taken');
    }
    setUser({ ...context.state.user, email });
    return clone({ user: context.state.user });
  }

  // A live reset link names its address (AU4.1).
  async function checkPasswordResetLink(body: OperationRequest<'checkPasswordResetLink'>): Promise<OperationResponse<'checkPasswordResetLink', '200'>> {
    context.applyOperation('checkPasswordResetLink');
    rejectUnknownKeys('checkPasswordResetLink', body, ['token']);
    if (body.token !== mockAccountToken('password_reset', context.state.user.id)) {
      throw new MockApiError(400, 'link is invalid or expired', 'checkPasswordResetLink', 'auth.token_invalid');
    }
    return { email: context.state.user.email };
  }

  async function rotateSession(): Promise<OperationResponse<'rotateSession', '200'>> {
    context.applyOperation('rotateSession');
    if (!context.state.session) {
      throw new MockApiError(401, 'Сессия отсутствует.', 'rotateSession');
    }
    context.state.session = { user: context.state.user };
    return clone(context.state.session);
  }

  async function startImpersonation(body: OperationRequest<'startImpersonation'>): Promise<OperationResponse<'startImpersonation', '200'>> {
    context.applyOperation('startImpersonation');
    if (!context.state.session) {
      throw new MockApiError(401, 'Сессия отсутствует.', 'startImpersonation');
    }
    context.state.session = {
      user: {
        ...context.state.user,
        email: body.email,
        displayName: body.email,
        canImpersonate: undefined,
        impersonation: {
          adminDisplayName: context.state.user.displayName,
          expiresAt: '2026-08-17T00:00:00.000Z',
        },
      },
    };
    return clone(context.state.session);
  }

  async function stopImpersonation(): Promise<OperationResponse<'stopImpersonation', '200'>> {
    context.applyOperation('stopImpersonation');
    context.state.session = { user: context.state.user };
    return clone(context.state.session);
  }

  // Other sessions end; this one stays (UC3.1).
  async function changePassword(body: OperationRequest<'changePassword'>): Promise<void> {
    context.applyOperation('changePassword');
    rejectUnknownKeys('changePassword', body, ['currentPassword', 'newPassword']);
    if (typeof body.newPassword !== 'string' || body.newPassword.length < 8) {
      throw new MockApiError(400, 'validation failed', 'changePassword', 'request.validation');
    }
    if (body.currentPassword !== mockPassword) {
      throw new MockApiError(400, 'current password is invalid', 'changePassword', 'auth.current_password_invalid');
    }
  }

  // The current documents are accepted with the edition the server owns (legal R3.2).
  async function acceptLegalDocuments(body: OperationRequest<'acceptLegalDocuments'>): Promise<void> {
    context.applyOperation('acceptLegalDocuments');
    rejectUnknownKeys('acceptLegalDocuments', body, ['acceptedPersonalDataConsent', 'acceptedTerms']);
    if (body.acceptedTerms !== true || body.acceptedPersonalDataConsent !== true) {
      throw new MockApiError(400, 'validation failed', 'acceptLegalDocuments', 'request.validation');
    }
    setUser({ ...context.state.user, documentsAccepted: true });
  }

  /** The name of «Аккаунт» by the rule of the registration (specs/2026-11-10-ux2-tariff-account.md TA5.1). */
  async function setAccountName(body: OperationRequest<'setAccountName'>): Promise<OperationResponse<'setAccountName', '200'>> {
    context.applyOperation('setAccountName');
    rejectUnknownKeys('setAccountName', body, ['displayName']);
    const session = ownSession('setAccountName');
    const displayName = body.displayName.trim();
    if (!displayName || [...displayName].length > 120) throw new MockApiError(400, 'validation failed', 'setAccountName', 'request.validation');
    setUser({ ...context.state.user, displayName });
    return clone({ user: { ...session.user, displayName } });
  }

  /** «Вы вошли на N устройствах» (TA5.2): this session and the others. */
  async function getAccountSessions(): Promise<OperationResponse<'getAccountSessions', '200'>> {
    context.applyOperation('getAccountSessions');
    if (!context.state.session) throw new MockApiError(401, 'authentication required', 'getAccountSessions', 'auth.session_required');
    return { count: 1 + context.state.otherSessions };
  }

  /** «Выйти на остальных» (TA5.3): the other sessions end, this one stays. */
  async function logoutOtherSessions(): Promise<OperationResponse<'logoutOtherSessions', '200'>> {
    context.applyOperation('logoutOtherSessions');
    ownSession('logoutOtherSessions');
    const count = context.state.otherSessions;
    context.state.otherSessions = 0;
    return { count };
  }

  /** The session of the author himself: none is 401, the work of an administrator as him 409. */
  function ownSession(operationId: 'setAccountName' | 'logoutOtherSessions') {
    const session = context.state.session;
    if (!session) throw new MockApiError(401, 'authentication required', operationId, 'auth.session_required');
    if (session.user.impersonation) throw new MockApiError(409, 'impersonation is active', operationId, 'auth.impersonation_conflict');
    return session;
  }

  async function logoutEverywhere(): Promise<void> {
    context.applyOperation('logoutEverywhere');
    context.state.session = null;
  }

  async function logout(): Promise<void> {
    context.applyOperation('logoutUser');
    context.state.session = null;
  }

  async function reportClientError(_body: OperationRequest<'reportClientError'>): Promise<void> {
    context.applyOperation('reportClientError');
  }

  /**
   * The computer of the desktop app (specs/2026-11-12-desk-window.md WN4.1): its local author — verified, nothing to
   * accept — no saga of anyone yet, and no helper. The sagas of the fixtures are of the site's writer.
   */
  function startDesktop() {
    context.reset();
    // The app has no helper until the author connects one (specs/2026-11-12-desk-window.md WN3.5).
    context.setScenario({ ...context.getScenario(), helperMissing: true });
    context.state.user = { ...desktopAuthor };
    context.state.projects = [];
    context.state.deletedProjects = [];
    context.state.deletedBooks = [];
    context.state.deletedChapters = [];
    context.state.invitations = [];
    context.state.personalNotes = [];
  }

  /** openDesktopSession (specs/2026-11-07-desktop-app.md DA3.3): a code of the shell opens the session of the local author. */
  async function openDesktopSession(body: { code?: unknown } | undefined): Promise<OperationResponse<'openDesktopSession', '200'>> {
    context.applyOperation('openDesktopSession');
    if (typeof body?.code !== 'string' || body.code === '') {
      throw new MockApiError(400, 'invalid request body', 'openDesktopSession', 'validation.failed');
    }
    context.state.session = { user: context.state.user };
    return clone(context.state.session);
  }

  function hostOf(address: string | undefined): URL | null {
    if (!address) return null;
    try {
      return new URL(address);
    } catch {
      return null;
    }
  }

  /**
   * Where the app goes, by the one owner of the backend (networkUses): the line of the site while linked or a link waits, which
   * names the questions to the helper and the reading of the books while linked with the source account, and the provider of
   * «Свой ИИ» while its address is kept — an address of this computer as a program of this computer
   * (specs/2026-11-14-desk-journal.md JR4.1).
   */
  function networkOf(): OperationResponse<'getDesktopState', '200'>['network'] {
    const network: OperationResponse<'getDesktopState', '200'>['network'] = [];
    const linked = desktopLinked(context);
    if (linked || context.state.desktopAccount.state === 'waiting') {
      const purposes = ['связь с аккаунтом: имя, тариф и остатки'];
      if (linked && context.state.aiSettings.source === 'account') purposes.push('вопросы помощнику и чтение книг');
      network.push({ host: mockSiteHost, purpose: purposes.join('; ') });
    }
    const provider = hostOf(context.state.aiSettings.own?.baseUrl);
    if (provider?.host) {
      const local = provider.hostname === 'localhost' || provider.hostname === '127.0.0.1' || provider.hostname === '[::1]';
      network.push({ host: provider.host, purpose: local ? 'программа на этом компьютере, помощник через «Свой ИИ»' : 'помощник через «Свой ИИ»' });
    }
    return network;
  }

  /**
   * getDesktopState (specs/2026-11-13-desk-settings.md ST2.1): the app on the computer of the local author — its version,
   * where its data lie and the place they take, two copies before an update (the older one made before the copies named
   * their version), where it goes — one line of the site while linked, which names the questions to the helper and the
   * reading of the books while the source is the account (specs/2026-11-13-desk-account-ai.md AI1.7) — and whether «Добро
   * пожаловать» is still to be shown.
   */
  async function getDesktopState(): Promise<OperationResponse<'getDesktopState', '200'>> {
    context.applyOperation('getDesktopState');
    if (!context.state.session) {
      throw new MockApiError(401, 'Сессия отсутствует.', 'getDesktopState');
    }
    return {
      version: '1.2.0',
      dataDir: '/home/author/.local/share/canonkeeper',
      usage: { bytes: 1_288_490_188, counting: false },
      backups: [
        { madeAt: '2026-11-12T09:15:00.000Z', version: '1.1.4' },
        { madeAt: '2026-11-02T08:00:00.000Z', version: null },
      ],
      network: networkOf(),
      welcome: context.state.desktopWelcome.shown,
      checkUpdates: context.state.desktopWelcome.checkUpdates,
      proxy: null,
      programsRead: context.state.programs.enabled,
    };
  }

  /** dismissWelcome (specs/2026-11-13-desk-upgrade.md UG5.1): the author closed «Добро пожаловать» with the choice. */
  async function dismissWelcome(body: { checkUpdates?: unknown } | undefined): Promise<void> {
    context.applyOperation('dismissWelcome');
    if (!context.state.session) {
      throw new MockApiError(401, 'Сессия отсутствует.', 'dismissWelcome', 'auth.session_required');
    }
    if (typeof body?.checkUpdates !== 'boolean') {
      throw new MockApiError(400, 'invalid request body', 'dismissWelcome', 'validation.failed');
    }
    context.state.desktopWelcome = { shown: false, checkUpdates: body.checkUpdates };
  }

  /**
   * An update of the app (specs/2026-11-13-desk-updates.md UB1.7): the mock is an AppImage of 1.2.0 that finds 1.3.0 at
   * once; a download ends after two reads of the state, then the update waits for the turns of the chat. The state is the
   * one of the repository, so reset() brings back an app that checked nothing (specs/2026-11-15-desk-updates-web.md).
   */
  type AppUpdate = OperationResponse<'getAppUpdate', '200'>;

  function appUpdate(): AppUpdate {
    const update = context.state.appUpdate;
    return {
      version: '1.2.0', daily: context.state.desktopWelcome.checkUpdates, kind: 'appimage', phase: update.phase,
      found: update.phase === 'idle' ? update.found : null, available: clone(update.available),
      percent: update.phase === 'downloading' ? update.percent : null, refusal: null, package: null, checkedAt: update.checkedAt,
    };
  }

  function updateSession(operationId: OperationId): void {
    context.applyOperation(operationId);
    if (!context.state.session) {
      throw new MockApiError(401, 'Сессия отсутствует.', operationId, 'auth.session_required');
    }
  }

  function updateConflict(operationId: OperationId): MockApiError {
    return new MockApiError(409, 'the update is in another phase', operationId, 'desktop.update_state');
  }

  async function getAppUpdate(): Promise<AppUpdate> {
    updateSession('getAppUpdate');
    const update = context.state.appUpdate;
    if (update.phase === 'downloading') {
      update.percent = (update.percent ?? 0) + 50;
      if (update.percent >= 100) {
        update.phase = 'waiting_turn';
        update.percent = null;
      }
    }
    return appUpdate();
  }

  async function checkAppUpdate(): Promise<AppUpdate> {
    updateSession('checkAppUpdate');
    const update = context.state.appUpdate;
    if (['checking', 'downloading', 'waiting_turn', 'installing'].includes(update.phase)) throw updateConflict('checkAppUpdate');
    update.phase = 'idle';
    update.found = 'available';
    update.available = { version: '1.3.0', notes: 'Северный предел читается быстрее; карта саги показывает дороги.' };
    update.checkedAt = new Date().toISOString();
    return appUpdate();
  }

  async function installAppUpdate(): Promise<AppUpdate> {
    updateSession('installAppUpdate');
    const update = context.state.appUpdate;
    const retry = ['download_failed', 'verify_failed', 'install_refused', 'install_failed'].includes(update.phase);
    if (!update.available || !(retry || (update.phase === 'idle' && update.found === 'available'))) {
      throw updateConflict('installAppUpdate');
    }
    update.phase = 'downloading';
    update.percent = 0;
    return appUpdate();
  }

  async function cancelAppUpdate(): Promise<AppUpdate> {
    updateSession('cancelAppUpdate');
    const update = context.state.appUpdate;
    if (update.phase !== 'downloading' && update.phase !== 'waiting_turn') throw updateConflict('cancelAppUpdate');
    update.phase = 'idle';
    update.found = 'available';
    return appUpdate();
  }

  async function updateAppUpdateSettings(body: { daily?: unknown } | undefined): Promise<AppUpdate> {
    updateSession('updateAppUpdateSettings');
    if (typeof body?.daily !== 'boolean') {
      throw new MockApiError(400, 'invalid request body', 'updateAppUpdateSettings', 'validation.failed');
    }
    context.state.desktopWelcome.checkUpdates = body.daily;
    return appUpdate();
  }

  /**
   * eraseDesktopData (specs/2026-11-13-desk-erase.md ER1.2): with the word «стереть» — whatever its case and the spaces
   * at its ends — the request goes to the shell, which the mock has not; another word asks nothing.
   */
  async function eraseDesktopData(body: { confirm?: unknown } | undefined): Promise<void> {
    context.applyOperation('eraseDesktopData');
    if (!context.state.session) {
      throw new MockApiError(401, 'Сессия отсутствует.', 'eraseDesktopData', 'auth.session_required');
    }
    if (typeof body?.confirm !== 'string') {
      throw new MockApiError(400, 'invalid request body', 'eraseDesktopData', 'validation.failed');
    }
    if (body.confirm.trim().toLocaleLowerCase('ru') !== 'стереть') {
      throw new MockApiError(400, 'the word of the erasure is not written', 'eraseDesktopData', 'desktop.erase_unconfirmed');
    }
  }

  async function getCurrentUser(): Promise<OperationResponse<'getCurrentUser', '200'>> {
    context.applyOperation('getCurrentUser');
    if (!context.state.session) {
      throw new MockApiError(401, 'Сессия отсутствует.', 'getCurrentUser');
    }
    return clone(context.state.session.user);
  }

  return {
    setScenario: context.setScenario,
    getScenario: context.getScenario,
    hasActiveSession: context.hasActiveSession,
    reset: context.reset,
    register,
    sendEmailVerification,
    confirmEmailVerification,
    requestPasswordReset,
    resetPassword,
    changeUnverifiedEmail,
    checkPasswordResetLink,
    login,
    rotateSession,
    startImpersonation,
    stopImpersonation,
    logout,
    changePassword,
    logoutEverywhere,
    setAccountName,
    getAccountSessions,
    logoutOtherSessions,
    acceptLegalDocuments,
    reportClientError,
    startDesktop,
    openDesktopSession,
    getDesktopState,
    dismissWelcome,
    getAppUpdate,
    checkAppUpdate,
    installAppUpdate,
    cancelAppUpdate,
    updateAppUpdateSettings,
    eraseDesktopData,
    getCurrentUser,
  };
}
