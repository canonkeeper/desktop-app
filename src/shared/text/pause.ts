/**
 * «0:59» — a pause of a letter or of a closed sign-in, rounded up to a whole second: the one count-down of «Откройте почту»,
 * «Забыли пароль?», the closed sign-in and the line about the address (specs/2026-11-10-ux3-auth.md AU1.4,
 * specs/2026-11-11-ux9-account.md AC1.9).
 */
export function formatPause(ms: number) {
  const seconds = Math.max(0, Math.ceil(ms / 1_000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}
