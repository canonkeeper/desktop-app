/**
 * The colours of a cover with their names (specs/2026-11-11-ux7-shelf.md SF4.1): a name, not a code, is what is said. They
 * are values of a book, kept by the server with the book and the same in both themes of the site — not colours of the look
 * (specs/2026-11-12-ux11-dark-sections.md DS3.1). The first one is the colour of a book without its own.
 */
export const coverColours: readonly { color: string; name: string }[] = [
  { color: '#4F46E5', name: 'Синий' },
  { color: '#285f58', name: 'Сине-зелёный' },
  { color: '#4c3a5d', name: 'Сливовый' },
  { color: '#30472b', name: 'Тёмно-зелёный' },
  { color: '#8a6500', name: 'Охра' },
];

/** The colour of a cover whose book has none of its own. */
export const defaultCoverColour = coverColours[0]!.color;
