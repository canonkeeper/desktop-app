import type { AuthorMaterialProperty } from '../../../entities/author-materials/api';

/** The properties of a record as the author edits them: one «Ключ: значение» on a line. */
export function propertiesToText(properties: readonly AuthorMaterialProperty[]) {
  return properties.map(({ key, value }) => `${key}: ${value}`).join('\n');
}

/** A line without a colon is a key with no value; an empty line is nothing. */
export function textToProperties(text: string): AuthorMaterialProperty[] {
  return text.split('\n').flatMap((line) => {
    const colon = line.indexOf(':');
    const key = (colon < 0 ? line : line.slice(0, colon)).trim();
    return key ? [{ key, value: colon < 0 ? '' : line.slice(colon + 1).trim() }] : [];
  });
}
