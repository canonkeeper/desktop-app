/**
 * The revision a write of `drawing.describe` made, from the summary of its event in the timeline: the server cuts it from
 * the first line of the tool's own answer, «Описание записано: ревизия N. …» (backend services/imageworkshop/tools.go),
 * not from what the helper says. Another answer — a refusal, a typed error — names none.
 */
export function writtenRevision(summary: string | undefined) {
  const match = summary ? /^Описание записано: ревизия (\d+)/u.exec(summary.trim()) : null;
  return match ? Number(match[1]) : undefined;
}
