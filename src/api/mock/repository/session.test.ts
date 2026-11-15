import { describe, expect, it } from 'vitest';
import { createMockApiClient, MockApiError } from '../index';
import { mockPassword } from './fixtures';
import { mockAccountToken } from './session';

async function refusal(promise: Promise<unknown>) {
  try {
    await promise;
  } catch (error) {
    if (error instanceof MockApiError) return error;
  }
  throw new Error('expected a refusal');
}

// The mock keeps the rules of specs/2026-11-10-ux3-auth.md the pages read: the window of sign-ins, the fix of an
// unverified address and the check of a reset link.
describe('the mock account', () => {
  it('counts failed sign-ins down to the closed window and a sign-in starts again', async () => {
    const api = createMockApiClient();
    const left: unknown[] = [];
    for (let attempt = 0; attempt < 5; attempt += 1) {
      const error = await refusal(api.login({ email: 'nobody@example.test', password: 'wrong-password', rememberMe: false }));
      left.push(error.problem.attemptsLeft);
      expect(error.problem).toMatchObject({ code: 'auth.invalid_credentials', lockMinutes: 15 });
    }
    expect(left).toEqual([4, 3, 2, 1, 0]);
    const closed = await refusal(api.login({ email: 'nobody@example.test', password: 'wrong-password', rememberMe: false }));
    expect(closed.status).toBe(429);
    expect(closed.response.headers.get('Retry-After')).toBe('900');

    await refusal(api.login({ email: 'mira@example.com', password: 'wrong-password', rememberMe: false }));
    await api.login({ email: 'mira@example.com', password: mockPassword, rememberMe: false });
    const again = await refusal(api.login({ email: 'mira@example.com', password: 'wrong-password', rememberMe: false }));
    expect(again.problem.attemptsLeft).toBe(4);
  });

  it('fixes an unverified address and refuses a taken or a verified one', async () => {
    const api = createMockApiClient();
    await api.register({ displayName: 'Автор', email: 'author@exmple.example.test', password: mockPassword, acceptedTerms: true, acceptedPersonalDataConsent: true });
    expect((await refusal(api.changeUnverifiedEmail({ email: 'mira@example.com' }))).problem.code).toBe('auth.email_taken');
    expect((await refusal(api.changeUnverifiedEmail({ email: 'author@' }))).problem.code).toBe('request.validation');
    const session = await api.changeUnverifiedEmail({ email: ' Author@Example.test ' });
    expect(session.user).toMatchObject({ email: 'author@example.test', emailVerified: false });
    expect((await api.getCurrentUser()).email).toBe('author@example.test');
    await api.confirmEmailVerification({ token: mockAccountToken('email_verification', session.user.id) });
    expect((await refusal(api.changeUnverifiedEmail({ email: 'other@example.test' }))).problem.code).toBe('auth.email_already_verified');
  });

  it('names the address of a live reset link only', async () => {
    const api = createMockApiClient();
    expect(await api.checkPasswordResetLink({ token: mockAccountToken('password_reset', 'user-mira') })).toEqual({ email: 'mira@example.com' });
    expect((await refusal(api.checkPasswordResetLink({ token: mockAccountToken('password_reset', 'user-unknown') }))).problem.code).toBe('auth.token_invalid');
  });
});

// specs/2026-11-15-desk-updates-web.md UW1, UW2 (mock): the computer of the mock begins as an app that has said its welcome
// and checked nothing yet, so the scenarios of the app open without the window and without a strip; reset() brings that
// state back after any welcome and any update.
describe('the update and the welcome of the mock app', () => {
  it('begin answered and unchecked, and reset() brings them back', async () => {
    const api = createMockApiClient();
    await api.openDesktopSession({ code: 'code-of-the-shell' });
    const first = await api.getDesktopState();
    expect([first.welcome, first.checkUpdates]).toEqual([false, true]);
    expect(await api.getAppUpdate()).toMatchObject({ phase: 'idle', found: 'never', available: null, checkedAt: null, daily: true });

    await api.updateAppUpdateSettings({ daily: false });
    await api.checkAppUpdate();
    await api.installAppUpdate();
    expect((await api.getAppUpdate()).phase).toBe('downloading');

    api.reset();
    await api.openDesktopSession({ code: 'code-of-the-shell' });
    const again = await api.getDesktopState();
    expect([again.welcome, again.checkUpdates]).toEqual([false, true]);
    expect(await api.getAppUpdate()).toMatchObject({ phase: 'idle', found: 'never', available: null, percent: null, checkedAt: null, daily: true });
  });

  it('keep the choice of the welcome and of the switch as one setting', async () => {
    const api = createMockApiClient();
    await api.openDesktopSession({ code: 'code-of-the-shell' });
    await api.dismissWelcome({ checkUpdates: false });
    expect((await api.getDesktopState()).checkUpdates).toBe(false);
    expect((await api.getAppUpdate()).daily).toBe(false);
    await api.updateAppUpdateSettings({ daily: true });
    expect((await api.getDesktopState()).checkUpdates).toBe(true);
  });
});
