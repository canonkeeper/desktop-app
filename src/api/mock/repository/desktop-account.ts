// SPDX-License-Identifier: AGPL-3.0-only
import type { OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import { followTheHelper } from './desktop-helper';

type DesktopAccount = Schema<'DesktopAccount'>;
type AccountInfo = NonNullable<DesktopAccount['account']>;

/** The site of the mock build: the pages the window opens come from its address (specs/2026-11-13-desk-account.md AC2.1). */
const site = 'https://canonkeeper.example.test';

/** The host of the site of the mock: the consent of the helper of the account names it (specs/2026-11-13-desk-account-ai.md AI3.4). */
export const mockSiteHost = new URL(site).host;

/** The helper the site of the mock tells for the app (specs/2026-11-13-desk-account-ai.md AI1.1, AI5.1). */
export const mockSiteHelper: Schema<'AiAccountHelper'> = {
  chatModel: 'gpt-north-chat', cardsModel: 'gpt-north-cards', contextWindow: 1_000_000, compactLimit: 950_000,
};

/** How long a link waits for the return of the browser (specs/2026-11-13-desk-account.md AC1.1). */
const waitLifetimeMs = 10 * 60_000;

/** The account the site of the mock tells: the author of the made-up saga, its tier and its counts. */
function accountOfTheSite(): AccountInfo {
  return {
    name: 'Автор Севера', email: 'a…@example.test', tier: { code: 'writer', title: 'Писатель', until: null },
    questions: { used: 4, limit: 30 }, reading: { used: 3_200_000, limit: 40_000_000 }, at: new Date().toISOString(),
  };
}

const none = (): MockRepositoryContext['state']['desktopAccount'] => ({ state: 'none', failure: null, account: null, link: null });

/**
 * A link made (specs/2026-11-13-desk-account.md AC1.3): the site told the account and its helper; the helper of the account is
 * chosen when the link was started to choose it or no helper is chosen yet — «Свой ИИ» chosen without that intent stays, and
 * its profile stays in every case (specs/2026-11-15-desk-account-link-web.md AW3.2, the rule of backend/internal/domain/aisettings).
 */
function linked(context: MockRepositoryContext, chooseHelper: boolean) {
  context.state.desktopAccount = { state: 'linked', failure: null, link: null, account: accountOfTheSite() };
  context.state.aiAccount = structuredClone(mockSiteHelper);
  const settings = context.state.aiSettings;
  if (chooseHelper || settings.source === 'none') context.state.aiSettings = { ...settings, source: 'account' };
  followTheHelper(context, true);
}

/**
 * The state of the link as the backend would hold it now: a scenario links the mock, which has no browser (`accountLinked`), or
 * breaks it (`accountBroken`) once — unlinking does not undo what it undid (specs/2026-11-15-desk-account-link-web.md, checklist
 * step 2); a link that waits ends by its term as `failed` `expired` (AW1.6), or as the scenario returns it (`accountLinkOutcome`).
 */
function settle(context: MockRepositoryContext) {
  const scenario = context.getScenario();
  const made = context.state.desktopAccountScenario;
  const link = context.state.desktopAccount;
  // The app the scenario starts linked was linked before: no link is made now, the helper stays as it was chosen.
  if (scenario.accountLinked === true && !made.linked && link.state === 'none') {
    made.linked = true;
    context.state.desktopAccount = { state: 'linked', failure: null, link: null, account: accountOfTheSite() };
  }
  if (scenario.accountBroken === true && !made.broken && (context.state.desktopAccount.state === 'none' || context.state.desktopAccount.state === 'linked')) {
    made.broken = true;
    context.state.desktopAccount = { state: 'broken', failure: null, link: null, account: null };
    followTheHelper(context, false);
  }
  const waiting = context.state.desktopAccount;
  if (waiting.state !== 'waiting' || !waiting.link) return;
  const outcome = scenario.accountLinkOutcome ?? (Date.parse(waiting.link.expiresAt) <= Date.now() ? 'expired' : null);
  if (outcome === 'linked') linked(context, waiting.chooseHelper === true);
  else if (outcome) context.state.desktopAccount = { state: 'failed', failure: outcome, link: null, account: null };
}

/** Whether the app is linked (specs/2026-11-13-desk-account-ai.md): the client holds a pass. */
export function desktopLinked(context: MockRepositoryContext): boolean {
  settle(context);
  return context.state.desktopAccount.state === 'linked';
}

/**
 * The helper of the account chosen while the app is not linked (specs/2026-11-13-desk-account-ai.md AI1.2): the helper is not
 * connected — the reading of a saga waits for it and a question is refused helper.not_connected — and the source stays.
 */
export function accountUnlinked(context: MockRepositoryContext): boolean {
  return context.state.aiSettings.source === 'account' && !desktopLinked(context);
}

/**
 * The link of the desktop app with an account of the site (specs/2026-11-13-desk-account.md): the mock has no browser and
 * no site, so a link it starts waits until its term, «Отменить» or the return a scenario gives; unlinking reaches the site
 * unless a scenario keeps it silent, and the shell. What the site told is shaped by the scenario of the read: no connection,
 * nothing known yet, the questions not told, the end of the tier, the pass in the file of the data directory.
 */
export function createDesktopAccountOperations(context: MockRepositoryContext) {
  function signedIn(operationId: 'getAccount' | 'startAccountLink' | 'cancelAccountLink' | 'unlinkAccount') {
    context.applyOperation(operationId);
    if (!context.state.session) throw new MockApiError(401, 'Сессия отсутствует.', operationId, 'auth.session_required');
  }

  function accountOf(info: AccountInfo | null): AccountInfo | null {
    const scenario = context.getScenario();
    if (!info || scenario.accountInfoMissing === true) return null;
    const account = structuredClone(info);
    if (scenario.accountQuestionsUnknown === true) account.questions = null;
    if (scenario.accountTierUntil) account.tier.until = scenario.accountTierUntil;
    return account;
  }

  function answer(): DesktopAccount {
    settle(context);
    const link = context.state.desktopAccount;
    const scenario = context.getScenario();
    return {
      state: link.state,
      link: link.state === 'waiting' ? structuredClone(link.link) : null,
      failure: link.state === 'failed' ? link.failure : null,
      account: link.state === 'linked' ? accountOf(link.account) : null,
      unreachable: link.state === 'linked' && scenario.accountUnreachable === true,
      computer: 'Рабочий стол',
      passStore: scenario.accountPassStore ?? 'system',
      pages: { tariffs: `${site}/tariffs`, apps: `${site}/account#apps` },
    };
  }

  return {
    async getAccount(): Promise<OperationResponse<'getAccount', '200'>> {
      signedIn('getAccount');
      return answer();
    },

    async startAccountLink(body?: OperationRequest<'startAccountLink'>): Promise<OperationResponse<'startAccountLink', '200'>> {
      signedIn('startAccountLink');
      // The intent of the author is optional (AW3.2); a body that is not the object of the operation is refused.
      if (body !== undefined && body !== null && typeof (body as { chooseHelper?: unknown }).chooseHelper !== 'boolean') {
        throw new MockApiError(400, 'invalid request body', 'startAccountLink', 'validation.failed');
      }
      settle(context);
      if (context.state.desktopAccount.state === 'linked' || context.state.desktopAccount.state === 'waiting') {
        throw new MockApiError(409, 'a link does not start now', 'startAccountLink', 'account.link_unavailable');
      }
      context.state.desktopAccount = {
        state: 'waiting', failure: null, account: null, chooseHelper: body?.chooseHelper === true,
        link: {
          address: `${site}/link?challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&platform=linux&port=24567&state=state-of-the-mock-0000000000000000000000000`,
          expiresAt: new Date(Date.now() + waitLifetimeMs).toISOString(),
        },
      };
      return answer();
    },

    async cancelAccountLink(): Promise<void> {
      signedIn('cancelAccountLink');
      // A canceled waiting leaves no reason: the author canceled it himself (AW1.6).
      if (context.state.desktopAccount.state === 'waiting') context.state.desktopAccount = none();
    },

    async unlinkAccount(): Promise<OperationResponse<'unlinkAccount', '200'>> {
      signedIn('unlinkAccount');
      settle(context);
      const held = context.state.desktopAccount.state === 'linked';
      context.state.desktopAccount = none();
      followTheHelper(context, false);
      return { siteUnlinked: held && context.getScenario().unlinkSiteSilent !== true, passRemoved: true };
    },
  };
}
