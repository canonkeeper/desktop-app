/**
 * What a closed button says (specs/2026-11-10-ux2-patterns-refusals.md PR1.1): the reason it is described by, and the
 * words of its plate once a press or the focus showed them — null while no plate stands beside it.
 */
export function reasonOf(button: HTMLElement) {
  return button.getAttribute('aria-description');
}

export function shownReason(button: HTMLElement) {
  const plate = button.nextElementSibling;
  if (!plate?.hasAttribute('data-reason')) return null;
  return plate.firstElementChild?.textContent ?? null;
}
