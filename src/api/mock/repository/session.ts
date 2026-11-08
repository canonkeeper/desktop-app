import type { OperationRequest, OperationResponse } from '../../../shared/api';
import { clone, normalizedEmail, rejectUnknownKeys } from './common';
import type { MockRepositoryContext } from './context';
import { mockPassword } from './fixtures';
import { MockApiError } from '../scenarios';

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

  async function login(body: OperationRequest<'loginUser'>): Promise<OperationResponse<'loginUser', '200'>> {
    context.applyOperation('loginUser');
    if (normalizedEmail(body.email) !== normalizedEmail(context.state.user.email) || body.password !== mockPassword) {
      throw new MockApiError(401, 'invalid credentials', 'loginUser', 'auth.invalid_credentials');
    }
    context.state.session = { user: context.state.user };
    return clone(context.state.session);
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
    login,
    rotateSession,
    startImpersonation,
    stopImpersonation,
    logout,
    changePassword,
    logoutEverywhere,
    acceptLegalDocuments,
    reportClientError,
    getCurrentUser,
  };
}
