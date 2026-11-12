import type { CanonKeeperApiClient } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';

type LettersOperations = Pick<CanonKeeperApiClient, 'getAccountLetters' | 'setAccountLetters'>;

/**
 * The letters of the site an account chose to get (FS3 of the spec «Сага готова», specs/2026-11-09;
 * specs/2026-11-10-service-letters.md M4.5): «Сага готова» and «Письма о сроке тарифа» are on until the author turns them
 * off; a change names at least one of them and leaves the other as it is; the choice is kept in the state of the mock,
 * each account its own.
 */
export function createLettersOperations(context: MockRepositoryContext): LettersOperations {
  const choice = (account: string) => ({
    sagaReady: !context.state.lettersOff.includes(account),
    tierLetters: !context.state.tierLettersOff.includes(account),
  });
  const keep = (list: string[], account: string, on: boolean) => [...list.filter((id) => id !== account), ...(on ? [] : [account])];
  return {
    async getAccountLetters() {
      context.applyOperation('getAccountLetters');
      return choice(context.currentPrincipalId());
    },
    async setAccountLetters(body) {
      context.applyOperation('setAccountLetters');
      const fields = Object.entries((body ?? {}) as Record<string, unknown>);
      if (fields.length === 0 || fields.some(([name, value]) => !['sagaReady', 'tierLetters'].includes(name) || typeof value !== 'boolean')) {
        throw new MockApiError(400, 'validation failed', 'setAccountLetters', 'request.validation');
      }
      const account = context.currentPrincipalId();
      if (typeof body.sagaReady === 'boolean') context.state.lettersOff = keep(context.state.lettersOff, account, body.sagaReady);
      if (typeof body.tierLetters === 'boolean') context.state.tierLettersOff = keep(context.state.tierLettersOff, account, body.tierLetters);
      return choice(account);
    },
  };
}
