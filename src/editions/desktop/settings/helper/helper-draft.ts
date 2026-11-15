// SPDX-License-Identifier: AGPL-3.0-only
import type { OperationRequest, Schema } from '../../../../shared/api';
import { presetEntries, presetOf, type Currency, type Preset, type Protocol, type Reasoning } from './helper-words';

type Settings = Schema<'AiSettings'>;
type Own = Schema<'OwnAi'>;

/** What the author wrote for one provider: switching to another keeps it until he saves (HW1.2). */
export interface ProviderDraft {
  baseUrl: string;
  protocol: Protocol;
  chatModel: string;
  cardsModel: string;
}

/** «Дополнительно» as the author writes it: the numbers are text until they are sent. */
export interface AdvancedDraft {
  contextWindow: string;
  maxOutput: string;
  reasoning: Reasoning;
  parallel: string;
  priceIn: string;
  /** The price of a cached input, optional (specs/2026-11-13-desk-consent-web.md RW3.1). */
  priceCache: string;
  priceOut: string;
  /** The currency of the prices, the dollar by default (specs/2026-11-13-desk-consent.md CN1.1). */
  currency: Currency;
}

/** The form of the part: the chosen provider, what was written for each one, and «Дополнительно». */
export interface HelperDraft {
  preset: Preset | null;
  providers: Record<Preset, ProviderDraft>;
  advanced: AdvancedDraft;
}

/** The defaults of the server for «Дополнительно» (specs/2026-11-13-desk-secrets.md SE2.1). */
const defaultAdvanced = { contextWindow: 128_000, maxOutput: 16_000, reasoning: 'medium', parallel: 4 } as const;

const groups = new Intl.NumberFormat('ru-RU', { useGrouping: true, maximumFractionDigits: 4 });

/** «128 000», «0,27»: a number as the author reads and writes it. */
export function numberWords(value: number) {
  return groups.format(value).replace(/\s/gu, ' ');
}

/** The form of the settings the server keeps; a provider not chosen yet takes what its variant gives. */
export function draftOf(settings: Settings): HelperDraft {
  const own = settings.own;
  const providers = Object.fromEntries(presetEntries.map((entry) => [entry.value, {
    baseUrl: entry.baseUrl ?? '', protocol: entry.value === 'custom' ? 'auto' : 'chat', chatModel: '', cardsModel: '',
  } satisfies ProviderDraft])) as Record<Preset, ProviderDraft>;
  if (own) providers[own.preset] = { baseUrl: own.baseUrl, protocol: own.protocol, chatModel: own.chatModel, cardsModel: own.cardsModel };
  const advanced = own?.advanced ?? { ...defaultAdvanced, priceInPerMillion: null, priceOutPerMillion: null };
  const cache = advanced.priceCachedInPerMillion ?? null;
  return {
    preset: own?.preset ?? null,
    providers,
    advanced: {
      contextWindow: numberWords(advanced.contextWindow), maxOutput: numberWords(advanced.maxOutput), reasoning: advanced.reasoning,
      parallel: String(advanced.parallel),
      priceIn: advanced.priceInPerMillion === null ? '' : numberWords(advanced.priceInPerMillion),
      priceCache: cache === null ? '' : numberWords(cache),
      priceOut: advanced.priceOutPerMillion === null ? '' : numberWords(advanced.priceOutPerMillion),
      currency: advanced.currency ?? 'USD',
    },
  };
}

/** A field the page refuses before sending, by the JSON pointer of the server: its words are the same (`fieldWords`). */
export class DraftFieldError extends Error {
  constructor(readonly path: string) {
    super(`the field ${path} is not a number`);
    this.name = 'DraftFieldError';
  }
}

/** «128 000», «128000»: a whole number with the spaces of its groups. */
function wholeOf(text: string, path: string) {
  const digits = text.replace(/\s/gu, '');
  if (!/^\d{1,9}$/u.test(digits)) throw new DraftFieldError(path);
  return Number(digits);
}

/**
 * «0,27», «0.27», «6 000», empty — not named. Up to four digits after the point and up to 1 000 000 a million tokens in any
 * currency, as the server keeps it (specs/2026-11-13-desk-consent.md CN1.1).
 */
function priceOf(text: string, path: string) {
  const value = text.replace(/\s/gu, '').replace(',', '.');
  if (value === '') return null;
  if (!/^\d{1,7}(?:\.\d{1,4})?$/u.test(value) || Number(value) > 1_000_000) throw new DraftFieldError(path);
  return Number(value);
}

/**
 * The body of updateAiSettings for the form (specs/2026-11-13-desk-secrets.md SE2.3): the chosen provider whole — a ready
 * variant without its address and protocol, which it gives — and «Дополнительно» as numbers. A number the page cannot read
 * is refused at its field before anything is sent.
 */
export function changeOf(draft: HelperDraft): OperationRequest<'updateAiSettings'> {
  if (!draft.preset) return { source: 'none' };
  const chosen = draft.providers[draft.preset];
  const advanced = draft.advanced;
  return {
    source: 'own',
    own: {
      preset: draft.preset,
      ...(draft.preset === 'custom' ? { baseUrl: chosen.baseUrl.trim(), protocol: chosen.protocol } : {}),
      chatModel: chosen.chatModel.trim(),
      cardsModel: chosen.cardsModel.trim(),
      advanced: {
        contextWindow: wholeOf(advanced.contextWindow, '/own/advanced/contextWindow'),
        maxOutput: wholeOf(advanced.maxOutput, '/own/advanced/maxOutput'),
        reasoning: advanced.reasoning,
        parallel: wholeOf(advanced.parallel, '/own/advanced/parallel'),
        priceInPerMillion: priceOf(advanced.priceIn, '/own/advanced/priceInPerMillion'),
        priceOutPerMillion: priceOf(advanced.priceOut, '/own/advanced/priceOutPerMillion'),
        // An empty price of the cache is sent as null: it takes the price away (CN1.1).
        priceCachedInPerMillion: priceOf(advanced.priceCache, '/own/advanced/priceCachedInPerMillion'),
        currency: advanced.currency,
      },
    },
  };
}

/** The form says what the server keeps: nothing to save. A number the page cannot read is a change. */
export function sameAsSaved(draft: HelperDraft, settings: Settings) {
  // «Свой ИИ» not chosen: the profile kept beside another source is still to be saved as the source.
  if (settings.source !== 'own') return draft.preset === null;
  try {
    return JSON.stringify(changeOf(draft)) === JSON.stringify(changeOf(draftOf(settings)));
  } catch {
    return false;
  }
}

/** The provider of the form is the one the server keeps: its address, protocol and variant — the list of models is asked of it. */
export function providerSaved(draft: HelperDraft, own: Own | null) {
  if (!draft.preset || !own || own.preset !== draft.preset) return false;
  if (draft.preset !== 'custom') return true;
  const chosen = draft.providers.custom;
  return chosen.baseUrl.trim() === own.baseUrl && chosen.protocol === own.protocol;
}

/**
 * The form changes what a check checked (specs/2026-11-13-desk-probes.md PB2.5): the variant, the address, the protocol or the
 * model of a role. «Дополнительно» does not: the verdicts read the window and the limit as they are saved.
 */
export function checkedChanged(draft: HelperDraft, own: Own | null) {
  if (!draft.preset || !own) return draft.preset !== null;
  const chosen = draft.providers[draft.preset];
  return !providerSaved(draft, own) || chosen.chatModel.trim() !== own.chatModel || chosen.cardsModel.trim() !== own.cardsModel;
}

/**
 * The form would pause a reading under a sum in money (specs/2026-11-13-desk-consent-web.md RW3.1; CN3.4): the kept prices are
 * both named and the form changes their currency or takes a price away. A new price in the same currency changes no unit.
 */
export function pausesMoney(draft: HelperDraft, own: Own | null) {
  const kept = own?.advanced;
  if (!kept || kept.priceInPerMillion === null || kept.priceOutPerMillion === null) return false;
  const advanced = draft.advanced;
  return advanced.currency !== (kept.currency ?? 'USD') || advanced.priceIn.trim() === '' || advanced.priceOut.trim() === '';
}

/** The key the chosen variant takes: needed, none (the local model) or as the provider asks. */
export function keyOf(draft: HelperDraft) {
  return draft.preset ? presetOf(draft.preset).key : 'none';
}
