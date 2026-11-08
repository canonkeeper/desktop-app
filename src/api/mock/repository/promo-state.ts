import type { Schema } from '../../../shared/api';
import { now } from './common';

type PromoPlace = Schema<'PromoPlace'>;

/** The links of the mock letters (specs/2026-10-30-promo-permissions.md R2.2): the page /promo/link#… reads them. */
export const mockPromoAnswerLink = 'mockPromoAnswerLinkForTheAuthorOfWhitePort01';
export const mockPromoRevokeLink = 'mockPromoRevokeLinkForTheAuthorOfWhitePort01';

export type MockPromoPermission = {
  id: string;
  projectId: string;
  sagaName: string;
  ownerUserId: string;
  recipient: { kind: 'writer'; userId: string; email: string } | { kind: 'email'; userId: null; email: string };
  status: 'pending' | 'granted' | 'declined' | 'revoked';
  fragments: Array<{ key: string; label: string; from: number; to: number; text: string }>;
  places: PromoPlace[];
  proposedAttribution: string | null;
  attribution: string | null;
  message: string | null;
  createdAt: string;
  expiresAt: string;
  answeredAt: string | null;
  revokedAt: string | null;
  answerLink: string | null;
  revokeLink: string | null;
};

export const lifetime = 30 * 24 * 60 * 60 * 1000;
/** The seed: the writer of the mock is asked about his saga; the author of its books by an address. */
export function createInitialPromoPermissions(writer: { id: string; email: string }, projectId: string, sagaName: string): MockPromoPermission[] {
  // The seed waits for the answer whenever the mock runs: its term counts from today, not from the fixed now.
  const expiresAt = new Date(Date.now() + lifetime).toISOString();
  const base = {
    projectId, sagaName, ownerUserId: writer.id, fragments: [],
    places: ['blog_author_today', 'canonkeeper_home'] as PromoPlace[], attribution: null, createdAt: now, expiresAt,
    answeredAt: null, revokedAt: null, revokeLink: null,
  };
  return [
    {
      ...base, id: 'promo-white-port-writer', recipient: { kind: 'writer', userId: writer.id, email: writer.email },
      status: 'pending', proposedAttribution: 'Мира Волкова', answerLink: null,
      message: 'Хотим показать сцену у северной пристани в первом посте блога.',
    },
    {
      ...base, id: 'promo-white-port-author', recipient: { kind: 'email', userId: null, email: 'author@example.test' },
      status: 'pending', proposedAttribution: 'Автор «Хроник»', answerLink: mockPromoAnswerLink, message: null,
    },
  ];
}
