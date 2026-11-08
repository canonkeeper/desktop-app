import type { AgentTimelineEvent } from '../../../shared/api';

/**
 * Server order of timeline events: an event already seen is replaced in place (by id, not by
 * cursor); a new one goes next to its neighbours from the same batch, else to the end.
 */
export function mergeEvents(
  current: readonly AgentTimelineEvent[],
  incoming: readonly AgentTimelineEvent[],
) {
  const merged = [...current];
  const byId = new Map<string, number>();
  const rebuildIndexes = () => {
    byId.clear();
    merged.forEach((event, index) => {
      byId.set(event.id, index);
    });
  };
  rebuildIndexes();
  for (let incomingIndex = 0; incomingIndex < incoming.length; incomingIndex += 1) {
    const event = incoming[incomingIndex];
    if (!event) continue;
    const existingIndex = byId.get(event.id);
    if (existingIndex !== undefined) {
      const existing = merged[existingIndex];
      if (existing) {
        merged[existingIndex] = event;
        rebuildIndexes();
      }
      continue;
    }
    let insertAt = merged.length;
    for (let nextIndex = incomingIndex + 1; nextIndex < incoming.length; nextIndex += 1) {
      const nextPosition = byId.get(incoming[nextIndex]?.id ?? '');
      if (nextPosition !== undefined) {
        insertAt = nextPosition;
        break;
      }
    }
    if (insertAt === merged.length) {
      for (let previousIndex = incomingIndex - 1; previousIndex >= 0; previousIndex -= 1) {
        const previousPosition = byId.get(incoming[previousIndex]?.id ?? '');
        if (previousPosition !== undefined) {
          insertAt = previousPosition + 1;
          break;
        }
      }
    }
    merged.splice(insertAt, 0, event);
    rebuildIndexes();
  }
  return merged;
}

/** Live order: replacements in place, events not seen yet appended. */
export function mergeLiveEvents(
  current: readonly AgentTimelineEvent[],
  incoming: readonly AgentTimelineEvent[],
) {
  const replacements = new Map(incoming.map((event) => [event.id, event]));
  const seen = new Set(current.map((event) => event.id));
  return [
    ...current.map((event) => replacements.get(event.id) ?? event),
    ...incoming.filter((event) => !seen.has(event.id)),
  ];
}
