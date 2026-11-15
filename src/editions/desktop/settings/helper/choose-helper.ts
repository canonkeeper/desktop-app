// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { isApiStatusFailure } from '../../../../entities/api-errors';
import type { OperationRequest, Schema } from '../../../../shared/api';
import type { AppRoute } from '../../../../shared/navigation/app-route';
import { accountQueryKey, afterLinkChange } from '../../account/account-api';
import { helperPartKey } from '../part-props';
import { aiSettingsQueryKey, aiSettingsQueryOptions, type HelperClient } from './helper-api';
import { changeOf, draftOf } from './helper-draft';

type Settings = Schema<'AiSettings'>;
export type HelperSource = 'account' | 'own';

/** The choice of the source of «Помощник» on the page: its place, so the line of a link made can bring the author to it. */
export const sourceChoiceId = 'helper-source-choice';

/** The author stands at «Настройки → Помощник»: by the address. */
export function atHelperPart(route: Pick<AppRoute, 'surface' | 'query'> | undefined) {
  return route?.surface === 'settings' && route.query.get('part') === helperPartKey;
}

/** The focus on the choice of the source of the part, in sight: the option chosen now. */
export function focusSourceChoice() {
  const choice = document.getElementById(sourceChoiceId);
  if (!choice) return;
  choice.scrollIntoView?.({ block: 'center' });
  (choice.querySelector<HTMLInputElement>('input:checked') ?? choice.querySelector<HTMLInputElement>('input'))?.focus();
}

/**
 * The body of a choice of the helper (specs/2026-11-15-desk-account-link-web.md AW3.1): updateAiSettings replaces the settings
 * whole, so the choice sends the source with the profile of «Свой ИИ» the server keeps, by the same builder as «Сохранить» of
 * the part — the provider, the models, «Дополнительно» and the prices live through the helper of the account and back. No
 * profile kept — the source alone.
 */
export function choiceOf(settings: Settings, source: HelperSource): OperationRequest<'updateAiSettings'> {
  const profile = settings.own ? changeOf(draftOf(settings)).own : undefined;
  return profile ? { source, own: profile } : { source };
}

/** The words of a choice refused (Data: «отказ выбора»). */
export const chooseWords = {
  unreachable: 'Помощника CanonKeeper выбрать не получилось: нет связи с сервером CanonKeeper',
  retry: 'Повторить',
  failed: 'Помощника выбрать не получилось. Попробуйте ещё раз',
} as const;

/** The code of a refused choice: 503 ai.account_unreachable, 409 helper.account_unlinked, or another. */
export function choiceRefusal(error: unknown) {
  return isApiStatusFailure(error) ? error.problem.code ?? null : null;
}

/**
 * The choice of the helper (AW3.1): of the account — the window «Подключить помощника», the part «Помощник», the line of the
 * outcome of a link — or «Свой ИИ» back with its profile. The settings the server keeps are read first, the answer is what
 * the pages show next; what the helper connected changes is read again. A refusal changes nothing: 409 helper.account_unlinked
 * has the link read again, which then shows «Связать с аккаунтом»; the caller says 503 with «Повторить».
 */
export function useChooseHelper(api: Pick<HelperClient, 'getAiSettings' | 'updateAiSettings'>) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (source: HelperSource) => {
      const settings = await client.ensureQueryData(aiSettingsQueryOptions(api));
      return api.updateAiSettings(choiceOf(settings.settings, source));
    },
    onSuccess: (state) => {
      client.setQueryData(aiSettingsQueryKey, state);
      afterLinkChange(client);
    },
    onError: (error) => {
      if (choiceRefusal(error) === 'helper.account_unlinked') void client.invalidateQueries({ queryKey: accountQueryKey });
    },
  });
}
