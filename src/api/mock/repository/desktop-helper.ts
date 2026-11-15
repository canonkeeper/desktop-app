// SPDX-License-Identifier: AGPL-3.0-only
import type { Schema } from '../../../shared/api';
import type { MockRepositoryContext } from './context';

/** A ready variant that answers only with the key of the author (backend/internal/domain/aisettings, NeedsKey). */
export function needsKey(own: Schema<'OwnAi'>) {
  return own.preset === 'deepseek' || own.preset === 'openai';
}

/**
 * A connected helper: «Свой ИИ», both models and the key its variant needs (GW4.1); the helper of the account the site told
 * while the app is linked (specs/2026-11-13-desk-account-ai.md AI1.2) — the caller says whether it is.
 */
export function connected(context: MockRepositoryContext, linked: boolean): boolean {
  const { source, own } = context.state.aiSettings;
  if (source === 'account') return !!context.state.aiAccount && linked;
  return source === 'own' && !!own && own.chatModel !== '' && own.cardsModel !== '' && (context.state.aiKeySaved || !needsKey(own));
}

/**
 * The reading of a saga of the app waits by the one rule «помощник подключён» (GW4.1, GW4.3): for the helper, then — the
 * helper connected — for the consent of the author. Only the computer of the app reads its helper: its start made the
 * reading wait for one (startDesktop); the site's mock keeps its scenario. A write of the settings and a link made or undone
 * follow it.
 */
export function followTheHelper(context: MockRepositoryContext, linked: boolean) {
  const scenario = context.getScenario();
  if (scenario.helperMissing === undefined && scenario.consentMissing === undefined) return;
  const on = connected(context, linked);
  context.setScenario({ ...scenario, helperMissing: !on, consentMissing: on });
}
