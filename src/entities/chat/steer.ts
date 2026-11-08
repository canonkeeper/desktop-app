import { useMutation } from '@tanstack/react-query';
import { useRef, useState } from 'react';
import { createIdempotencyKey, type CanonKeeperApiClient } from '../../shared/api';

/** What a correction needs of the API: the one operation of every running turn, of the chat or of a helper. */
export type SteerApiClient = Pick<CanonKeeperApiClient, 'steerChatTurn'>;

/** Why a correction was refused, in the words of the field that keeps the text (chat-steering R4.3). */
export const steerFailures: Record<string, string> = {
  'chat.turn_ended': 'Ход уже завершился. Текст остался в поле — отправьте его новым сообщением.',
  'chat.steer_retry': 'Сейчас уточнение не принимается. Текст остался в поле — повторите через несколько секунд.',
  'chat.steer_limit': 'В этот ход отправлено слишком много уточнений. Дождитесь ответа — текст останется в поле.',
};

/** A correction the server took, not yet in the timeline. */
export interface PendingSteer {
  steerId: string;
  text: string;
  images: readonly number[];
  firstPhoto?: number;
}

/** The timeline as far as a correction reads it: the questions and the corrections with their images. */
export interface SteerTimelineEvent {
  kind: string;
  steerId?: string;
  images?: readonly number[];
  firstPhoto?: number;
}

interface SentSteer extends PendingSteer {
  turnId: string;
}

interface SteerAttempt {
  turnId: string;
  text: string;
  images: readonly number[];
  firstPhoto: number;
  key: string;
}

/**
 * «фото N» of the next image of a turn (specs/2026-10-30-chat-steering.md R4.2): after the images of
 * its question and of the corrections sent so far.
 */
export function nextPhotoNumber(events: readonly SteerTimelineEvent[], waiting: readonly PendingSteer[]) {
  let used = 0;
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index]!;
    if (event.kind === 'steer') used = Math.max(used, photosThrough(event));
    if (event.kind === 'user') {
      used = Math.max(used, event.images?.length ?? 0);
      break;
    }
  }
  for (const steer of waiting) used = Math.max(used, photosThrough(steer));
  return used + 1;
}

function photosThrough({ firstPhoto, images }: { firstPhoto?: number; images?: readonly number[] }) {
  return images?.length ? (firstPhoto ?? 1) + images.length - 1 : 0;
}

/** Sends a correction into the running turn (specs/2026-10-30-chat-steering.md): no Job, no quota. */
export function useSteerChatTurnMutation(api: SteerApiClient) {
  return useMutation({
    mutationFn: (input: {
      chatId: string; turnId: string; body: string; idempotencyKey: string; images: readonly number[]; firstPhoto?: number;
    }) => api.steerChatTurn(
      input.chatId,
      input.turnId,
      {
        body: input.body,
        ...(input.images.length ? { attachedImages: [...input.images], firstPhoto: input.firstPhoto } : {}),
      },
      { idempotencyKey: input.idempotencyKey },
    ),
  });
}

/**
 * The corrections of the running turn of a chat or of a helper (specs/2026-11-03-workshop-helpers-steering.md S1.6):
 * the send and its key, what the server took and the timeline does not show yet (a fact of the form, not server
 * state: it goes once the timeline has it, by `steerId`) and the number the next image takes. A turn has its own
 * list, so chats do not mix.
 */
export function useTurnSteer({ active, api, chatId, events, turnId }: {
  active: boolean;
  api: SteerApiClient;
  chatId: string;
  events: readonly SteerTimelineEvent[];
  turnId: string;
}) {
  const mutation = useSteerChatTurnMutation(api);
  const [sent, setSent] = useState<readonly SentSteer[]>([]);
  const attempt = useRef<SteerAttempt | null>(null);
  const waiting = sent.filter((steer) => steer.turnId === turnId && !events.some((event) => event.kind === 'steer' && event.steerId === steer.steerId));
  const firstPhoto = active ? nextPhotoNumber(events, waiting) : 1;

  /** A repeat of the same text with the same images keeps its key; anything else is a new correction. */
  function attemptFor(text: string, images: readonly number[]) {
    const current = attempt.current;
    if (current?.turnId === turnId && current.text === text && current.firstPhoto === firstPhoto && current.images.join(',') === images.join(',')) return current;
    attempt.current = { turnId, text, images: [...images], firstPhoto, key: createIdempotencyKey('steerChatTurn') };
    return attempt.current;
  }

  async function send(text: string, images: readonly number[]) {
    const { firstPhoto: first, key } = attemptFor(text, images);
    const { steerId } = await mutation.mutateAsync({ chatId, turnId, body: text, images, idempotencyKey: key, ...(images.length ? { firstPhoto: first } : {}) });
    attempt.current = null;
    setSent((list) => list.some((steer) => steer.steerId === steerId) ? list : [
      ...list, { turnId, steerId, text, images: [...images], ...(images.length ? { firstPhoto: first } : {}) },
    ]);
  }

  return {
    firstPhoto,
    send,
    sending: mutation.isPending && mutation.variables?.turnId === turnId,
    error: mutation.error,
    reset: mutation.reset,
    waiting,
  };
}
