import { createContext, useContext, type ReactNode } from 'react';
import type { ReturnPlace } from '../../shared/navigation/app-route';

/**
 * The third part of a refusal by a limit (specs/2026-11-10-ux2-patterns-refusals.md PR2.1;
 * specs/2026-11-08-subscription-tiers.md TR9.4): what the next tier gives and «Тариф», which carries the place of the
 * refusal so the page of the tier leads back to it (PR2.4). The site gives it; the desktop has none, and its refusals say
 * what ended and when it comes back.
 */
export interface TierNote {
  /** «На тарифе „Художник“ — 20 вопросов в день.» */
  offer: ReactNode;
  /** The button «Тариф». */
  tariff: ReactNode;
  /**
   * The tail of the quiet line of a limit of space before the press (PR2.7): « на тарифе „Бесплатно“ · больше — на
   * „Художнике“», the second part a link to «Тариф» that leads back to the place given.
   */
  line: ReactNode;
}

export type QuotaNote = (code: string | null | undefined, place?: ReturnPlace) => TierNote | null;

const QuotaNoteContext = createContext<QuotaNote | null>(null);

export const QuotaNoteProvider = QuotaNoteContext.Provider;

const noNote: QuotaNote = () => null;

export function useQuotaNote(): QuotaNote {
  return useContext(QuotaNoteContext) ?? noNote;
}
