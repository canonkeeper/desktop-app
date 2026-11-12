import { http } from 'msw';
import { MockApiError } from '../../api/mock/scenarios';
import type { OperationId } from '../../shared/api';
import { createAuthSession, mswUser } from './fixtures';
import { readJson, type JsonObject } from './handler-input';
import {
  clearSessionHeaders,
  createMswCsrfState,
  csrfHandler,
  emptyResponse,
  hasMswSessionCookie,
  jsonResponse,
  mswPath,
  sessionHeaders,
  type MswSecurityContext,
} from './handler-runtime';

const mockPassword = 'white-port-12';

function validateRegisterBody(body: JsonObject) {
  const allowed = new Set(['acceptedPersonalDataConsent', 'acceptedTerms', 'displayName', 'email', 'password']);
  for (const key of Object.keys(body)) {
    if (!allowed.has(key)) {
      throw new MockApiError(400, 'Request body contains unsupported keys.', 'registerUser', 'validation_failed');
    }
  }
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

interface AuthHandlersOptions {
  /** Whether the signed-in user accepted the current documents; true by default. */
  documentsAccepted?: boolean;
  /** Whether the signed-in user's address is verified; true by default. */
  emailVerified?: boolean;
  onLogin?: (body: { rememberMe?: unknown }) => void;
  onRegister?: (body: { acceptedTerms?: unknown; displayName?: unknown }) => void;
}

function normalizedEmail(value: string) {
  return value.trim().toLowerCase();
}

export function createAuthHandlers(options: AuthHandlersOptions = {}) {
  const csrfState = createMswCsrfState();
  let sessionActive = true;
  let impersonatedEmail: string | null = null;
  let user = { ...mswUser, emailVerified: options.emailVerified ?? true, documentsAccepted: options.documentsAccepted ?? true };
  // The other devices the author is signed in on (specs/2026-11-10-ux2-tariff-account.md TA5.2).
  let otherSessions = 2;
  const authSecurity = (operationId: OperationId, request: Request): MswSecurityContext => ({
    csrfState,
    hasActiveSession: () => sessionActive,
    operationId,
    request,
  });
  return [
    csrfHandler(csrfState),
    http.get(mswPath('getCurrentUser'), ({ request }) =>
      jsonResponse(
        () => impersonatedEmail
          ? {
              ...mswUser,
              email: impersonatedEmail,
              displayName: 'Impersonated User',
              impersonation: { adminDisplayName: mswUser.displayName, expiresAt: '2026-08-17T00:00:00.000Z' },
            }
          : user,
        undefined,
        authSecurity('getCurrentUser', request),
      ),
    ),
    http.post(mswPath('startImpersonation'), async ({ request }) => {
      const body = await readJson(request);
      return jsonResponse(
        () => {
          if (typeof body.email !== 'string' || !body.email.trim()) {
            throw new MockApiError(400, 'Impersonation email is required.', 'startImpersonation', 'validation_failed');
          }
          impersonatedEmail = normalizedEmail(body.email);
          return createAuthSession({
            ...mswUser,
            email: impersonatedEmail,
            displayName: 'Impersonated User',
            impersonation: { adminDisplayName: mswUser.displayName, expiresAt: '2026-08-17T00:00:00.000Z' },
          });
        },
        { headers: sessionHeaders() },
        authSecurity('startImpersonation', request),
      );
    }),
    http.delete(mswPath('stopImpersonation'), ({ request }) =>
      jsonResponse(
        () => {
          impersonatedEmail = null;
          return createAuthSession({ ...mswUser, canImpersonate: true });
        },
        { headers: sessionHeaders() },
        authSecurity('stopImpersonation', request),
      ),
    ),
    http.post(mswPath('loginUser'), async ({ request }) => {
      const body = await readJson(request);
      return jsonResponse(
        () => {
          options.onLogin?.({ rememberMe: body.rememberMe });
          if (typeof body.email !== 'string' || typeof body.password !== 'string' || normalizedEmail(body.email) !== normalizedEmail(mswUser.email) || body.password !== mockPassword) {
            throw new MockApiError(401, 'invalid credentials', 'loginUser', 'auth.invalid_credentials');
          }
          sessionActive = true;
          return createAuthSession(user);
        },
        { headers: sessionHeaders() },
        { csrfState, operationId: 'loginUser', request },
      );
    }),
    // The window of the desktop opens the session of the local author with a one-time code of the shell
    // (specs/2026-11-07-desktop-app.md DA3.3).
    http.post(mswPath('openDesktopSession'), async ({ request }) => {
      const body = await readJson(request);
      return jsonResponse(
        () => {
          if (typeof body.code !== 'string' || body.code === '') {
            throw new MockApiError(400, 'invalid request body', 'openDesktopSession', 'validation.failed');
          }
          sessionActive = true;
          return createAuthSession(user);
        },
        { headers: sessionHeaders() },
        { csrfState, operationId: 'openDesktopSession', request },
      );
    }),
    http.post(mswPath('rotateSession'), ({ request }) =>
      jsonResponse(() => createAuthSession(), { headers: sessionHeaders() }, authSecurity('rotateSession', request)),
    ),
    http.post(mswPath('logoutUser'), ({ request }) =>
      emptyResponse(() => {
        if (hasMswSessionCookie(request)) {
          sessionActive = false;
        }
      }, authSecurity('logoutUser', request), { headers: clearSessionHeaders() }),
    ),
    http.post(mswPath('registerUser'), async ({ request }) => {
      const body = await readJson(request);
      return jsonResponse(
        () => {
          validateRegisterBody(body);
          options.onRegister?.({ acceptedTerms: body.acceptedTerms, displayName: body.displayName });
          if (normalizedEmail(String(body.email)) === normalizedEmail(user.email)) {
            throw new MockApiError(409, 'email is already registered', 'registerUser', 'auth.email_taken');
          }
          user = { ...user, displayName: String(body.displayName), email: normalizedEmail(String(body.email)), emailVerified: false, documentsAccepted: true };
          sessionActive = true;
          return createAuthSession(user);
        },
        { status: 201, headers: sessionHeaders() },
        { csrfState, operationId: 'registerUser', request },
      );
    }),
    http.post(mswPath('sendEmailVerification'), ({ request }) =>
      jsonResponse(
        () => {
          if (user.emailVerified) {
            throw new MockApiError(409, 'email address is already verified', 'sendEmailVerification', 'auth.email_already_verified');
          }
          return { status: 'accepted' };
        },
        { status: 202 },
        authSecurity('sendEmailVerification', request),
      ),
    ),
    // The fix of an unverified address (specs/2026-11-10-ux3-auth.md AU1.8): the session with the new address.
    http.put(mswPath('changeUnverifiedEmail'), async ({ request }) => {
      const body = await readJson(request);
      return jsonResponse(
        () => {
          if (user.emailVerified) {
            throw new MockApiError(409, 'email address is already verified', 'changeUnverifiedEmail', 'auth.email_already_verified');
          }
          if (typeof body.email !== 'string' || !/^[^\s@]+@[^\s@]+$/u.test(body.email.trim())) {
            throw new MockApiError(400, 'validation failed', 'changeUnverifiedEmail', 'request.validation');
          }
          user = { ...user, email: normalizedEmail(body.email) };
          return createAuthSession(user);
        },
        undefined,
        authSecurity('changeUnverifiedEmail', request),
      );
    }),
    http.post(mswPath('changePassword'), async ({ request }) => {
      const body = await readJson(request);
      return emptyResponse(() => {
        if (typeof body.newPassword !== 'string' || body.newPassword.length < 8) {
          throw new MockApiError(400, 'validation failed', 'changePassword', 'request.validation');
        }
        if (body.currentPassword !== mockPassword) {
          throw new MockApiError(400, 'current password is invalid', 'changePassword', 'auth.current_password_invalid');
        }
      }, authSecurity('changePassword', request));
    }),
    http.post(mswPath('acceptLegalDocuments'), async ({ request }) => {
      const body = await readJson(request);
      return emptyResponse(() => {
        if (body.acceptedTerms !== true || body.acceptedPersonalDataConsent !== true) {
          throw new MockApiError(400, 'validation failed', 'acceptLegalDocuments', 'request.validation');
        }
        user = { ...user, documentsAccepted: true };
      }, authSecurity('acceptLegalDocuments', request));
    }),
    // «Аккаунт» (specs/2026-11-10-ux2-tariff-account.md UC5): the name, the devices and «Выйти на остальных».
    http.put(mswPath('setAccountName'), async ({ request }) => {
      const body = await readJson(request);
      return jsonResponse(
        () => {
          const name = typeof body.displayName === 'string' ? body.displayName.trim() : '';
          if (!name || [...name].length > 120) throw new MockApiError(400, 'validation failed', 'setAccountName', 'request.validation');
          user = { ...user, displayName: name };
          return createAuthSession(user);
        },
        undefined,
        authSecurity('setAccountName', request),
      );
    }),
    http.get(mswPath('getAccountSessions'), ({ request }) =>
      jsonResponse(() => ({ count: 1 + otherSessions }), undefined, authSecurity('getAccountSessions', request))),
    http.post(mswPath('logoutOtherSessions'), ({ request }) =>
      jsonResponse(() => {
        const count = otherSessions;
        otherSessions = 0;
        return { count };
      }, undefined, authSecurity('logoutOtherSessions', request))),
    http.post(mswPath('logoutEverywhere'), ({ request }) =>
      emptyResponse(() => {
        sessionActive = false;
      }, authSecurity('logoutEverywhere', request), { headers: clearSessionHeaders() }),
    ),
    http.post(mswPath('requestPasswordReset'), async ({ request }) => {
      const body = await readJson(request);
      return jsonResponse(
        () => {
          if (typeof body.email !== 'string' || !body.email.includes('@')) {
            throw new MockApiError(400, 'validation failed', 'requestPasswordReset', 'request.validation');
          }
          return { status: 'accepted' };
        },
        { status: 202 },
        { csrfState, operationId: 'requestPasswordReset', request },
      );
    }),
    http.post(mswPath('resetPassword'), async ({ request }) => {
      const body = await readJson(request);
      return jsonResponse(
        () => {
          if (typeof body.newPassword !== 'string' || body.newPassword.length < 8) {
            throw new MockApiError(400, 'validation failed', 'resetPassword', 'request.validation');
          }
          if (body.token !== `mock-password_reset-${user.id}`) {
            throw new MockApiError(400, 'link is invalid or expired', 'resetPassword', 'auth.token_invalid');
          }
          user = { ...user, emailVerified: true };
          sessionActive = true;
          return createAuthSession(user);
        },
        { headers: sessionHeaders() },
        { csrfState, operationId: 'resetPassword', request },
      );
    }),
    // A live reset link names its address and is not spent (specs/2026-11-10-ux3-auth.md AU4.1).
    http.post(mswPath('checkPasswordResetLink'), async ({ request }) => {
      const body = await readJson(request);
      return jsonResponse(
        () => {
          if (body.token !== `mock-password_reset-${user.id}`) {
            throw new MockApiError(400, 'link is invalid or expired', 'checkPasswordResetLink', 'auth.token_invalid');
          }
          return { email: user.email };
        },
        undefined,
        { csrfState, operationId: 'checkPasswordResetLink', request },
      );
    }),
    http.post(mswPath('confirmEmailVerification'), async ({ request }) => {
      const body = await readJson(request);
      return jsonResponse(
        () => {
          if (body.token !== `mock-email_verification-${user.id}`) {
            throw new MockApiError(400, 'link is invalid or expired', 'confirmEmailVerification', 'auth.token_invalid');
          }
          user = { ...user, emailVerified: true };
          return { status: 'verified' };
        },
        undefined,
        { csrfState, operationId: 'confirmEmailVerification', request },
      );
    }),
  ];
}
