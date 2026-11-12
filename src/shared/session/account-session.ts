import { createContext, useContext, type ReactNode } from 'react';
import type { Schema } from '../api';
import { formatPause } from '../text/pause';

/**
 * The one reason of an unverified address (specs/2026-11-10-ux1-shell.md SH4.1): the line under the top bar of every page
 * and the notice where the author meets a closed action say these words, and only these.
 */
export function emailUnverifiedReason(email: string) {
  return `Подтвердите почту ${email} — и можно будет загружать книги, спрашивать, рисовать и писать.`;
}

/** Its short form, said at the place of a closed action and for a refusal of the server (SH4.4, SH4.5). */
export const emailUnverifiedShort = 'Сначала подтвердите почту — письмо со ссылкой уже у вас.';

/**
 * The words of the button of a new letter (AC1.9): «Отправить письмо ещё раз», while it is on the way «Отправляем…», while
 * the next waits the count-down of «Откройте почту» — the one function `formatPause`.
 */
export function sendAgainWords(verification: Pick<EmailVerificationState, 'status' | 'pauseMs'>) {
  if (verification.status === 'sending') return 'Отправляем…';
  return verification.pauseMs ? `Ещё раз через ${formatPause(verification.pauseMs)}` : 'Отправить письмо ещё раз';
}

/** The unverified address of the signed-in writer and the way to get its letter again (UC1.3). */
export interface EmailVerificationState {
  email: string;
  /** sending — the request is on its way; sent — a letter is queued; waiting — the account limit; failed — see message. */
  status: 'idle' | 'sending' | 'sent' | 'waiting' | 'failed';
  /** Minutes until the next letter is allowed while waiting. */
  retryAfterMinutes?: number;
  /**
   * How long the next letter waits after a letter or a refusal, counted down by the second: «Ещё раз через 0:59» on the
   * button (specs/2026-11-11-ux9-account.md AC1.9); absent when a letter may go.
   */
  pauseMs?: number;
  /** The address the last letter went to, once «Изменить» moved the account to it (specs/2026-11-10-ux3-auth.md AU1.7). */
  sentTo?: string;
  message?: string;
  /** False while a request is on its way or the account limit holds the next letter. */
  canSend: boolean;
  /** Absent while a platform admin works as this user. */
  onSend?: () => void;
  /** Opens «Изменить почту»; absent while a platform admin works as this user (AU1.7). */
  onChangeEmail?: () => void;
}

/**
 * What «Написать нам» opens with (specs/2026-11-10-ux2-contact.md CT1.1): the topic, the code for support of the error
 * or the refusal, and the first lines of a new message, such as the reason of a refused key (UXK3.3). Nothing of the
 * address of the page or of the text of the author goes in it: the page is attached by the window itself, without its
 * query.
 */
export interface ContactRequest {
  /** Chosen for a new message, unless the author already began one with another topic (CT1.5). */
  topic?: Schema<'FeedbackTopic'>;
  /** The code for support, sent in the context of the message next to the page (CT1.3). */
  code?: string;
  /** The first lines of a new message; the author's own draft stays after them (CT1.5). */
  draft?: string;
}

/**
 * What the account menu of every surface shows (specs/2026-11-10-ux2-tariff-account.md UC1): the name and the address,
 * «Тариф», «Аккаунт», «Написать нам» and «Выйти», with the lines of the sign-in — the work of an administrator as another
 * user and the unverified address. «Сменить пароль», the devices and the documents are on «Аккаунт».
 */
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
  /** Opens «Смена пароля» of «Аккаунт»; absent while a platform admin works as another user. */
  onChangePassword?: () => void;
  /** Opens «Выйти на всех устройствах?» of «Аккаунт»; absent while a platform admin works as another user. */
  onLogoutEverywhere?: () => void;
  /**
   * Opens «Написать нам»: the form, the author's threads and the contacts (specs/2026-11-07-contacts.md C2.3, F3;
   * specs/2026-11-10-ux2-contact.md CT1.1); absent where the window does not work — a guest, the admin working as
   * another user, the desktop build. A place gives what the new message opens with; opened bare with an unread reply,
   * the window opens on «Ответы» (CT2.1).
   */
  onOpenContact?: (request?: ContactRequest) => void;
  /** Threads with a reply of support the author has not opened yet: the mark of the account button (F3.2). */
  contactUnread?: number;
  /**
   * A request of CanonKeeper to show a saga waits for the answer (specs/2026-11-11-ux9-account.md AC3.7): the mark of the
   * account button and a line of its menu that opens the request; absent where a build has none or nothing waits.
   */
  promoRequest?: { onOpen: () => void };
  /** Opens «Тариф», where a build has plans (specs/2026-11-10-ux2-tariff-account.md TA1.1). */
  onOpenTariff?: () => void;
  /** Opens «Аккаунт», where a build has one (TA1.1). */
  onOpenAccount?: () => void;
  /**
   * The tier in force once the plan is read, where a build has plans (TR9.9, TA1.3): the badge next to the account button
   * of a top bar and the line «Тариф · …» of the account menu in the same words, in the tone of a warning when the tier
   * ends within 3 days. Absent while the plan loads or fails: the tier is not guessed.
   */
  plan?: { badge: ReactNode; line: string; ending: boolean };
  /**
   * A line of the account under the top bar of every page, where a build has plans: the end of a paid tier on its day and
   * seven after it (specs/2026-11-11-ux9-account.md AC2.4).
   */
  notice?: ReactNode;
}

export const AccountSessionContext = createContext<AccountSession | null>(null);
export function useAccountSession() { return useContext(AccountSessionContext); }
