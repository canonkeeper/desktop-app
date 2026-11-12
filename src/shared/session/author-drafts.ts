/**
 * What the author typed and has not sent is kept in this browser by the page that owns it: a question of the chat, the
 * text of a chapter the server did not take, the idea of a drawing and a message to its helper, the marks of a review of
 * his materials, a message to us. A sign-out is the author leaving this browser, so it forgets all of it; a sign-in that
 * expired keeps it — he comes back to the same page. The settings of the device (the notice of a ready answer, the start
 * of a call) are not his text and stay.
 */
const draftKeys = [
  'canonkeeper.chat.question-drafts',
  'canonkeeper.writing.unsaved.',
  'canonkeeper.drawing.draft.',
  'canonkeeper.materials.review.',
  'canonkeeper.feedback.',
  'canonkeeper.admin.feedback.',
];

function forgetIn(storage: () => Storage) {
  try {
    const store = storage();
    for (const key of Object.keys(store)) {
      if (draftKeys.some((draft) => key.startsWith(draft))) store.removeItem(key);
    }
  } catch {
    // A browser without storage kept nothing.
  }
}

export function forgetAuthorDrafts() {
  forgetIn(() => window.localStorage);
  forgetIn(() => window.sessionStorage);
}

/** Whether this browser keeps something the author typed: the sign-in after an expired one says so (specs/2026-11-11-ux9-account.md AC1.1). */
export function hasAuthorDrafts() {
  const keeps = (storage: () => Storage) => {
    try {
      const store = storage();
      // A place that was emptied keeps an empty record: it is no text.
      return Object.keys(store).some((key) => draftKeys.some((draft) => key.startsWith(draft))
        && !['', '{}', '[]', 'null', '""'].includes((store.getItem(key) ?? '').trim()));
    } catch {
      return false;
    }
  };
  return keeps(() => window.localStorage) || keeps(() => window.sessionStorage);
}
