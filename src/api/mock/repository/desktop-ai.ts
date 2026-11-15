// SPDX-License-Identifier: AGPL-3.0-only
import type { OperationRequest, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import { desktopLinked, mockSiteHelper } from './desktop-account';
import { connected, followTheHelper, needsKey } from './desktop-helper';
import { moneyStopSagasOf } from './reading';
import type { MockAiCheckKept } from './state';

type OwnAi = Schema<'OwnAi'>;
type Probe = Schema<'AiProbe'>;
type Reason = Schema<'AiReason'>;

/** The ready variants of «Свой ИИ»: the address and the protocol each gives (backend/internal/domain/aisettings). */
const presets: Record<Exclude<OwnAi['preset'], 'custom'>, Pick<OwnAi, 'baseUrl' | 'protocol'>> = {
  deepseek: { baseUrl: 'https://api.deepseek.com/v1', protocol: 'chat' },
  openai: { baseUrl: 'https://api.openai.com/v1', protocol: 'responses' },
  ollama: { baseUrl: 'http://127.0.0.1:11434/v1', protocol: 'chat' },
};

/** The models a ready variant of the mock lists (specs/2026-11-13-desk-probes.md PB1.1), by the alphabet. */
const listedModels: Record<Exclude<OwnAi['preset'], 'custom'>, readonly string[]> = {
  deepseek: ['deepseek-chat', 'deepseek-reasoner'],
  openai: ['north-large', 'north-small'],
  ollama: ['gemma4', 'qwen3:8b'],
};

/**
 * An address of «Другой, совместимый с OpenAI» the mock answers as a provider that fails (specs/2026-11-13-desk-helper-web.md,
 * checklist step 2): it does not answer, asks to wait, or answers with an error. Any other address gives no list of models.
 */
const failingHosts: Record<string, Reason> = {
  'down.example.test': 'unreachable',
  'busy.example.test': 'rate_limited',
  'broken.example.test': 'other',
};

/** A key of the mock the shell does not keep, and one the provider refuses: the mock keeps neither, only what it means. */
const keyNotKept = 'key-test-not-kept';
const keyRefused = 'key-test-refused';

/** «Дополнительно» left out (specs/2026-11-13-desk-secrets.md SE2.1). */
const defaults: OwnAi['advanced'] = {
  contextWindow: 128_000, maxOutput: 16_000, reasoning: 'medium', parallel: 4, priceInPerMillion: null, priceOutPerMillion: null,
};

function invalid(operationId: 'updateAiSettings' | 'setAiKey', code: string, path: string): MockApiError {
  return new MockApiError(400, 'a field is not valid', operationId, code, { fields: { errors: [{ path, message: 'not valid', code }] } });
}

function within(value: number | null | undefined, low: number, high: number): boolean {
  return value === undefined || value === null || (Number.isFinite(value) && value >= low && value <= high);
}

/** The profile the server keeps for a write, by the rules of the domain the window meets most: variant, address, numbers. */
function ownOf(own: NonNullable<OperationRequest<'updateAiSettings'>['own']>, previous: OwnAi | null = null): OwnAi {
  const ready = own.preset === 'custom' ? null : presets[own.preset];
  if (own.preset !== 'custom' && !ready) throw invalid('updateAiSettings', 'ai.settings_invalid', '/own/preset');
  if (ready && own.baseUrl !== undefined && own.baseUrl !== ready.baseUrl) throw invalid('updateAiSettings', 'ai.settings_invalid', '/own/baseUrl');
  if (!ready && !/^https?:\/\/[^/@\s]+(\/\S*)?$/u.test(own.baseUrl ?? '')) throw invalid('updateAiSettings', 'ai.settings_invalid', '/own/baseUrl');
  // The currency and the price of the cache a write leaves out stay (specs/2026-11-13-desk-consent.md CN1.1).
  const kept = { currency: previous?.advanced.currency ?? 'USD', priceCachedInPerMillion: previous?.advanced.priceCachedInPerMillion ?? null };
  const advanced = { ...defaults, ...kept, ...own.advanced };
  if (!within(advanced.contextWindow, 8_000, 2_000_000)) throw invalid('updateAiSettings', 'ai.settings_invalid', '/own/advanced/contextWindow');
  if (!within(advanced.maxOutput, 1_000, 200_000)) throw invalid('updateAiSettings', 'ai.settings_invalid', '/own/advanced/maxOutput');
  if (!within(advanced.parallel, 1, 32)) throw invalid('updateAiSettings', 'ai.settings_invalid', '/own/advanced/parallel');
  if (!within(advanced.priceInPerMillion, 0, 1_000)) throw invalid('updateAiSettings', 'ai.settings_invalid', '/own/advanced/priceInPerMillion');
  if (!within(advanced.priceOutPerMillion, 0, 1_000)) throw invalid('updateAiSettings', 'ai.settings_invalid', '/own/advanced/priceOutPerMillion');
  return {
    preset: own.preset,
    baseUrl: ready?.baseUrl ?? own.baseUrl ?? '',
    protocol: ready?.protocol ?? own.protocol ?? 'auto',
    chatModel: own.chatModel ?? '',
    cardsModel: own.cardsModel ?? '',
    advanced,
  };
}

function hostOf(own: OwnAi) {
  try {
    return new URL(own.baseUrl).hostname;
  } catch {
    return '';
  }
}


/** The reasons that stop a check (Reason.Stops): the provider did not answer or refused the key. */
const stops = (reason: Reason | null) => reason === 'unreachable' || reason === 'key_refused';

/**
 * «Свой ИИ» of the desktop app (specs/2026-11-13-desk-secrets.md SE2): the settings of the window, and the key the mock
 * keeps as the shell would and never answers. Its provider answers by the names the author writes (checklist step 2 of
 * specs/2026-11-13-desk-helper-web.md): a model of `reasoner` or `no-tools` calls no tool, of `short-context` sees only
 * the start of a long text, of `short-answer` cuts a long answer, of `slow` runs out of the three minutes on the long
 * answer, of `missing` is not there and of `busy` asks to wait; the hosts of `failingHosts` fail, a key of `key-test-refused`
 * is refused and one of `key-test-not-kept` is not kept by the shell.
 */
export function createDesktopAiOperations(context: MockRepositoryContext) {
  function signedIn(
    operationId: 'getAiSettings' | 'updateAiSettings' | 'setAiKey' | 'deleteAiKey' | 'listAiModels' | 'getAiCheck' | 'startAiCheck' | 'cancelAiCheck',
  ) {
    context.applyOperation(operationId);
    if (!context.state.session) throw new MockApiError(401, 'Сессия отсутствует.', operationId, 'auth.session_required');
  }

  function state(): OperationResponse<'getAiSettings', '200'> {
    const scenario = context.getScenario();
    return {
      settings: structuredClone(context.state.aiSettings), keySaved: context.state.aiKeySaved,
      secretsStore: scenario.aiSecretsStore ?? 'system', appliesAfterAnswer: scenario.aiAnswerRunning === true && context.state.aiWindowWaits,
      check: context.state.aiSettings.source === 'account' ? null : report(),
      moneyStopSagas: moneyStopSagasOf(context.state),
      account: structuredClone(context.state.aiAccount),
    };
  }

  /** The kept check with its verdicts by the settings now (the rules of backend/internal/domain/aisettings, PB2.4, PB2.5). */
  function report(): Schema<'AiCheckReport'> | null {
    const kept = context.state.aiCheckKept;
    const own = context.state.aiSettings.own;
    if (!kept || !own) return null;
    const failed = (model: string, probes: Probe['probe'][]) => {
      for (const probe of probes) {
        const result = kept.probes.find((one) => one.probe === probe && one.model === model);
        if (!result) return kept.probes.find((one) => stops(one.reason) || one.reason === 'slow')?.reason ?? 'other';
        if (!result.passed) return result.reason ?? 'other';
      }
      return null;
    };
    const chatFailed = failed(kept.checked.chatModel, ['short', 'tool', 'long_input']);
    const booksFailed = failed(kept.checked.cardsModel, ['short', 'long_input', 'long_output']);
    const window = own.advanced.contextWindow;
    const chat: Schema<'AiVerdict'> = chatFailed ? { fit: 'unfit', reason: chatFailed }
      : window < 64_000 ? { fit: 'unfit', reason: 'small_window' } : window < 128_000 ? { fit: 'thin', reason: 'small_window' } : { fit: 'fit', reason: null };
    const books: Schema<'AiVerdict'> = booksFailed ? { fit: 'unfit', reason: booksFailed }
      : own.advanced.maxOutput < 16_000 ? { fit: 'unfit', reason: 'small_limit' } : { fit: 'fit', reason: null };
    const checked = { baseUrl: own.baseUrl, protocol: own.protocol, chatModel: own.chatModel, cardsModel: own.cardsModel };
    return {
      at: kept.at, chatModel: kept.checked.chatModel, cardsModel: kept.checked.cardsModel, probes: structuredClone(kept.probes),
      calibration: kept.calibration, chat, books, stale: kept.keyChanged || JSON.stringify(checked) !== JSON.stringify(kept.checked),
    };
  }

  /** How a probe of the mock goes, by the address, the key and the name of the model (see above). */
  function outcomeOf(probe: Probe['probe'], model: string, own: OwnAi): Pick<Probe, 'passed' | 'reason'> {
    const fails = (reason: Reason) => ({ passed: false, reason });
    if (hostOf(own) === 'down.example.test') return fails('unreachable');
    if (context.state.aiKeySaved && context.state.aiKeyRefused) return fails('key_refused');
    // A provider without the model, or one that asks to wait, fails every probe of it: only the connection and the key stop
    // a check (Reason.Stops), the rest go on.
    if (model.includes('missing')) return fails('no_model');
    if (model.includes('busy')) return fails('rate_limited');
    if (probe === 'tool' && /reasoner|no-tools/u.test(model)) return fails('no_tools');
    if (probe === 'long_input' && model.includes('short-context')) return fails('start_only');
    if (probe === 'long_output' && model.includes('short-answer')) return fails('cut_answer');
    if (probe === 'long_output' && model.includes('slow')) return fails('slow');
    return { passed: true, reason: null };
  }

  /** The probes of a check: one model in both roles once, the long answer last (PB2.3), each with how it goes. */
  function planOf(own: OwnAi): Probe[] {
    const probe = (kind: Probe['probe'], role: 'chat' | 'books', model: string): Probe => ({ probe: kind, role, model, ...outcomeOf(kind, model, own) });
    const { chatModel: chat, cardsModel: books } = own;
    if (chat === books) {
      return [probe('short', 'chat', chat), probe('tool', 'chat', chat), probe('long_input', 'chat', chat), probe('calibrate', 'books', books), probe('long_output', 'books', books)];
    }
    return [
      probe('short', 'chat', chat), probe('short', 'books', books), probe('tool', 'chat', chat), probe('long_input', 'chat', chat),
      probe('long_input', 'books', books), probe('calibrate', 'books', books), probe('long_output', 'books', books),
    ];
  }

  /** The key changed: the kept check and the one that runs are stale (PB2.5). */
  function keyChanged() {
    if (context.state.aiCheckKept) context.state.aiCheckKept.keyChanged = true;
  }

  /** The check ends: its probes are kept with what it checked (PB2.5). */
  function endCheck(own: OwnAi, state: 'done' | 'timed_out') {
    const check = context.state.aiCheck;
    const kept: MockAiCheckKept = {
      at: new Date().toISOString(), probes: structuredClone(check.probes),
      calibration: check.probes.some((probe) => probe.probe === 'calibrate' && probe.passed) ? 301.6 : null, keyChanged: false,
      checked: { baseUrl: own.baseUrl, protocol: own.protocol, chatModel: own.chatModel, cardsModel: own.cardsModel },
    };
    context.state.aiCheckKept = kept;
    context.state.aiCheckPlan = [];
    context.state.aiCheck = { ...check, state, current: null, report: report() };
  }

  return {
    async getAiSettings(): Promise<OperationResponse<'getAiSettings', '200'>> {
      signedIn('getAiSettings');
      return state();
    },

    async updateAiSettings(body: OperationRequest<'updateAiSettings'>): Promise<OperationResponse<'updateAiSettings', '200'>> {
      signedIn('updateAiSettings');
      if (body?.source !== 'none' && body?.source !== 'own' && body?.source !== 'account') throw invalid('updateAiSettings', 'ai.settings_invalid', '/source');
      if (body.source === 'own' && !body.own) throw invalid('updateAiSettings', 'ai.settings_invalid', '/own');
      if (body.source === 'account') {
        // The helper of the account: a linked app whose site answers now (specs/2026-11-13-desk-account-ai.md AI1.1).
        if (!desktopLinked(context)) {
          throw new MockApiError(409, 'the app is not linked with an account', 'updateAiSettings', 'helper.account_unlinked');
        }
        if (context.getScenario().accountSilent === true) {
          throw new MockApiError(503, 'the site did not answer', 'updateAiSettings', 'ai.account_unreachable');
        }
        context.state.aiAccount = structuredClone(mockSiteHelper);
      }
      const before = context.state.aiSettings.own?.advanced.contextWindow;
      context.state.aiSettings = { source: body.source, own: body.own ? ownOf(body.own, context.state.aiSettings.own) : null };
      const after = context.state.aiSettings.own?.advanced.contextWindow;
      // A new window of the chat waits for the answer that runs (GW2.3); none runs — it holds at once.
      context.state.aiWindowWaits = context.getScenario().aiAnswerRunning === true && (context.state.aiWindowWaits || before !== after);
      followTheHelper(context, desktopLinked(context));
      return state();
    },

    async setAiKey(body: OperationRequest<'setAiKey'>): Promise<void> {
      signedIn('setAiKey');
      const key = body?.key;
      if (typeof key !== 'string' || key === '' || new TextEncoder().encode(key).length > 1280 || /[\s\p{Cc}]/u.test(key)) {
        throw invalid('setAiKey', 'ai.key_invalid', '/key');
      }
      // The shell did not keep it — the store of the system did not answer (specs/2026-11-13-desk-secrets.md SE2.5).
      if (key.startsWith(keyNotKept)) throw new MockApiError(503, 'the key was not saved', 'setAiKey', 'ai.key_not_saved');
      context.state.aiKeySaved = true;
      context.state.aiKeyRefused = key.startsWith(keyRefused);
      keyChanged();
      followTheHelper(context, desktopLinked(context));
    },

    async deleteAiKey(): Promise<void> {
      signedIn('deleteAiKey');
      context.state.aiKeySaved = false;
      context.state.aiKeyRefused = false;
      keyChanged();
      followTheHelper(context, desktopLinked(context));
    },

    /**
     * The models of the provider of the saved settings (specs/2026-11-13-desk-probes.md PB1): a ready variant lists them, another
     * address does not; a failing address or a refused key says why.
     */
    async listAiModels(): Promise<OperationResponse<'listAiModels', '200'>> {
      signedIn('listAiModels');
      if (context.state.aiSettings.source === 'account') {
        throw new MockApiError(409, 'the helper of the account has no models to list', 'listAiModels', 'helper.not_for_source');
      }
      const own = context.state.aiSettings.own;
      if (!own || (needsKey(own) && !context.state.aiKeySaved)) {
        throw new MockApiError(409, 'no provider to ask', 'listAiModels', 'ai.not_set');
      }
      const failing = failingHosts[hostOf(own)];
      if (failing) return { outcome: 'failed', models: [], reason: failing };
      if (context.state.aiKeySaved && context.state.aiKeyRefused) return { outcome: 'failed', models: [], reason: 'key_refused' };
      if (own.preset === 'custom') return { outcome: 'unlisted', models: [], reason: null };
      return { outcome: 'listed', models: [...listedModels[own.preset]], reason: null };
    },

    /** «Проверить связь» (PB2.1): the check of the mock goes one probe a read of the window, then it is done and kept. */
    async startAiCheck(): Promise<OperationResponse<'startAiCheck', '202'>> {
      signedIn('startAiCheck');
      if (context.state.aiSettings.source === 'account') {
        throw new MockApiError(409, 'the helper of the account is not checked', 'startAiCheck', 'helper.not_for_source');
      }
      if (!connected(context, desktopLinked(context))) throw new MockApiError(409, 'the helper is not connected', 'startAiCheck', 'helper.not_connected');
      if (context.state.aiCheck.state === 'running') throw new MockApiError(409, 'a check runs', 'startAiCheck', 'ai.check_running');
      const plan = planOf(context.state.aiSettings.own!);
      const first = plan[0]!;
      context.state.aiCheckPlan = plan;
      context.state.aiCheck = {
        state: 'running', startedAt: new Date().toISOString(), current: { probe: first.probe, role: first.role, model: first.model }, probes: [], report: null,
      };
      return structuredClone(context.state.aiCheck);
    },

    async getAiCheck(): Promise<OperationResponse<'getAiCheck', '200'>> {
      signedIn('getAiCheck');
      const check = context.state.aiCheck;
      const own = context.state.aiSettings.own;
      if (check.state === 'running' && own) {
        const [went, ...rest] = context.state.aiCheckPlan;
        if (went) check.probes = [...check.probes, went];
        const next = rest[0];
        context.state.aiCheckPlan = rest;
        if (went?.reason === 'slow') endCheck(own, 'timed_out');
        else if (!next || stops(went?.reason ?? null)) endCheck(own, 'done');
        else check.current = { probe: next.probe, role: next.role, model: next.model };
      }
      return structuredClone(context.state.aiCheck);
    },

    async cancelAiCheck(): Promise<void> {
      signedIn('cancelAiCheck');
      if (context.state.aiCheck.state === 'running') context.state.aiCheck = { ...context.state.aiCheck, state: 'canceled', current: null };
      context.state.aiCheckPlan = [];
    },
  };
}
