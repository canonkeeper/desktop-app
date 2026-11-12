/** The public document pages (legal R1.1); links reach them from sign-in, menus and dialogs. */
export const legalDocumentPaths = {
  terms: '/legal/terms',
  privacy: '/legal/privacy',
  consent: '/legal/consent',
  // The offer of the paid tiers (specs/2026-11-08-subscription-tiers.md S6): accepted by buying or entering a key.
  paid: '/legal/paid',
} as const;

/**
 * A section of a document by its number, the anchor of its part on the page (specs/2026-11-11-ux9-account.md AC3.5):
 * «Тариф» leads to «Возврат денег» of the offer; legal.test.tsx holds the number to the heading of the text.
 */
export const legalSections = {
  refund: { path: legalDocumentPaths.paid, anchor: '5' },
} as const;

/** A document opened from the registration form: its page offers the way back to the form (AC3.5). */
export function fromRegistration(path: string) {
  return `${path}?from=register`;
}
