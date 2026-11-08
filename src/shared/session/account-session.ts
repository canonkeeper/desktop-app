import { createContext, useContext } from 'react';

/** The unverified address of the signed-in writer and the way to get its letter again (UC1.3). */
export interface EmailVerificationState {
  email: string;
  /** sending — the request is on its way; sent — a letter is queued; waiting — the account limit; failed — see message. */
  status: 'idle' | 'sending' | 'sent' | 'waiting' | 'failed';
  /** Minutes until the next letter is allowed while waiting. */
  retryAfterMinutes?: number;
  message?: string;
  /** False while a request is on its way or the account limit holds the next letter. */
  canSend: boolean;
  /** Absent while a platform admin works as this user. */
  onSend?: () => void;
}

/** What the account menu of every surface shows. */
export interface AccountSession {
  /** The user a platform admin works as until returning. */
  actingAs?: {
    name: string;
    email: string;
    adminName: string;
    /** When the work as this user ends: together with the admin's own sign-in. */
    expiresAt: string;
    expiresSoon: boolean;
  };
  /**
   * Opens the dialog in which a platform admin picks a user to work as; with an email the work
   * as that user starts at once (admin panel R3.5).
   */
  onActAsUser?: (email?: string) => void;
  /** Opens the admin panel; only the platform admin outside the work as another user (R6.3). */
  onOpenAdmin?: () => void;
  /** Ends the work as another user and restores the admin. */
  onReturnToAdmin?: () => void;
  busy: boolean;
  /** Present while the address is not verified: model work stays closed until it is. */
  emailVerification?: EmailVerificationState;
  /** Opens «Смена пароля»; absent while a platform admin works as another user. */
  onChangePassword?: () => void;
  /** Opens «Выйти на всех устройствах?»; absent while a platform admin works as another user. */
  onLogoutEverywhere?: () => void;
  /** Opens the terms, the privacy policy and the consent (legal R1.2). */
  onOpenDocuments?: () => void;
  /**
   * Opens «Разрешения на продвижение»; present once the writer has a permission to answer, keep or
   * revoke, and absent while a platform admin works as him (specs/2026-10-30-promo-permissions.md R3.4).
   */
  onOpenPromoPermissions?: () => void;
  /**
   * Opens «Связаться с нами»: the form, the author's threads and the contacts (specs/2026-11-07-contacts.md C2.3, F3);
   * absent while a platform admin works as another user.
   */
  onOpenContact?: () => void;
  /** Threads with a reply of support the author has not opened yet: the mark of the account button (F3.2). */
  contactUnread?: number;
}

export const AccountSessionContext = createContext<AccountSession | null>(null);
export function useAccountSession() { return useContext(AccountSessionContext); }
