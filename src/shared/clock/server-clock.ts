/**
 * The time of the server as this tab knows it (specs/2026-11-11-ux9-shelf-chat.md SC3.8, J-80): the transport reads the Date of
 * every answer, and a counter of a running work — a turn of the chat, a drawing — counts from it, not from a clock of the
 * device that runs ahead or behind. The difference changes only by more than a second, so a counter does not jump; until
 * an answer comes the clocks are taken as one.
 */
let difference = 0;
const steadyMs = 1_000;

/** The Date of an answer, sent between `sentAt` and `receivedAt` by the clock of the device. */
export function noteServerDate(header: string | null | undefined, sentAt: number, receivedAt: number) {
  if (!header) return;
  const server = Date.parse(header);
  if (Number.isNaN(server)) return;
  // The header names the second the answer left the server: half a second on average past it, at the middle of the way.
  const next = server + 500 - (sentAt + receivedAt) / 2;
  if (Math.abs(next - difference) > steadyMs) difference = next;
}

/** Now by the clock of the server. */
export function serverNow() {
  return Date.now() + difference;
}

/** The clocks taken as one again (the tests). */
export function forgetServerClock() {
  difference = 0;
}
