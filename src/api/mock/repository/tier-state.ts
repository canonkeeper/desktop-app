import type { Schema } from '../../../shared/api';

type Tier = Schema<'SubscriptionTier'>;
type TierCode = Schema<'SubscriptionTierCode'>;

/**
 * The four tiers as the migration 0130 writes them (specs/2026-11-11-tier-limits.md TL1.1); no price: it is only on
 * FunPay.
 */
export function createInitialTiers(): Tier[] {
  const tier = (code: TierCode, rank: number, title: string, limits: Tier['limits']): Tier => ({
    code, rank, title, lotUrl: null, limits,
  });
  return [
    tier('free', 0, 'Бесплатно', {
      projects: 1, projectBooks: 1, books: 1, chatTurnsPerDay: 5, imagesPerDay: 0, trialImages: 3, chaptersPerWindow: null, chapterEveryDays: null,
      trialChapters: 1, chapterDraftsPerDay: 5, voiceMinutesPerDay: 0, trialVoiceCalls: 3, trialVoiceCallMinutes: 5,
      accountCharacters: 3_000_000, accountChapters: 600, drawingTurnsPerDay: 5, draftTurnsPerDay: 10, draftRewritesPerDay: 20,
      importsPerDay: 5, activeTurns: 2,
    }),
    tier('artist', 1, 'Художник', {
      projects: 2, projectBooks: 6, books: 12, chatTurnsPerDay: 15, imagesPerDay: 10, trialImages: 0, chaptersPerWindow: 2, chapterEveryDays: 7,
      trialChapters: 0, chapterDraftsPerDay: 20, voiceMinutesPerDay: 0, trialVoiceCalls: 3, trialVoiceCallMinutes: 5,
      accountCharacters: 25_000_000, accountChapters: 5000, drawingTurnsPerDay: 60, draftTurnsPerDay: 60, draftRewritesPerDay: 200,
      importsPerDay: 30, activeTurns: 2,
    }),
    tier('writer', 2, 'Писатель', {
      projects: 3, projectBooks: 12, books: 36, chatTurnsPerDay: 30, imagesPerDay: 15, trialImages: 0, chaptersPerWindow: 7, chapterEveryDays: 7,
      trialChapters: 0, chapterDraftsPerDay: 30, voiceMinutesPerDay: 60, trialVoiceCalls: 0, trialVoiceCallMinutes: 5,
      accountCharacters: 40_000_000, accountChapters: 8000, drawingTurnsPerDay: 60, draftTurnsPerDay: 80, draftRewritesPerDay: 200,
      importsPerDay: 30, activeTurns: 2,
    }),
    tier('pro', 3, 'Профи', {
      projects: 5, projectBooks: 30, books: 150, chatTurnsPerDay: 60, imagesPerDay: 30, trialImages: 0, chaptersPerWindow: 30, chapterEveryDays: 7,
      trialChapters: 0, chapterDraftsPerDay: 50, voiceMinutesPerDay: 60, trialVoiceCalls: 0, trialVoiceCallMinutes: 5,
      accountCharacters: 80_000_000, accountChapters: 15_000, drawingTurnsPerDay: 100, draftTurnsPerDay: 120, draftRewritesPerDay: 300,
      importsPerDay: 30, activeTurns: 4,
    }),
  ];
}
