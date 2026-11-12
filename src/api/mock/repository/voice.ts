import type { CanonKeeperApiClient } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';
import { mockVoice } from './tiers';

type VoiceOperations = Pick<CanonKeeperApiClient, 'getProjectVoice' | 'setAccountVoice' | 'voiceCallToken'>;
type Voice = Awaited<ReturnType<CanonKeeperApiClient['getProjectVoice']>>['voice'];

const voices: Voice[] = ['breeze', 'vale', 'spruce', 'arbor', 'ember'];

/**
 * The mock voice: configured, an editor talks on every page of the saga, the user's voice is kept for the session of the
 * mock (specs/2026-11-03-voice-assistant.md V1.1, V1.9, V2.1), and what his tier lets him talk
 * (specs/2026-11-08-subscription-tiers.md TR8.5).
 */
export function createVoiceOperations(context: MockRepositoryContext): VoiceOperations {
  let chosen: Voice = 'breeze';
  return {
    async getProjectVoice(projectId) {
      context.requireProjectAccess('getProjectVoice', projectId);
      const talk = ['editor', 'admin', 'owner'].includes(context.actorRole(projectId));
      const { kind, secondsLeft, trialCallsLeft, callSeconds } = mockVoice(context.state, context.currentPrincipalId());
      return { talk, modes: ['writing', 'ask', 'drawing'], voice: chosen, voices, allowance: { kind, secondsLeft, trialCallsLeft, callSeconds } };
    },
    async setAccountVoice(body) {
      if (!voices.includes(body.voice)) {
        throw new MockApiError(400, 'validation failed', 'setAccountVoice', 'request.validation');
      }
      chosen = body.voice;
      return { voice: chosen };
    },
    async voiceCallToken() {
      return 'ck_mock_csrf_token_000000';
    },
  };
}
