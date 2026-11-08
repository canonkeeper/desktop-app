import type { OperationResponse } from '../../shared/api';

type AuthSession = OperationResponse<'loginUser', '200'>;

export const mswUser: AuthSession['user'] = {
  createdAt: '2026-06-16T05:00:00.000Z',
  displayName: 'Мира Волкова',
  email: 'mira@example.com',
  emailVerified: true,
  documentsAccepted: true,
  id: 'user-mira',
};

export function createAuthSession(user: AuthSession['user'] = mswUser): AuthSession {
  return { user };
}
