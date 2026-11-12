import { describe, expect, it } from 'vitest';
import { phraseMarks } from './answer-phrase';

// specs/2026-11-11-ux9-shelf-chat.md SC3.7 (J-25): the words of the answer inside the paragraph it cites.
describe('the phrase of an answer in its paragraph', () => {
  const paragraph = 'Долго молчали у костра. Сава снял с пояса старый кинжал и протянул его Маре, не сказав ни слова. Потом ушёл.';
  const at = (text: string) => ({ start: paragraph.indexOf(text), end: paragraph.indexOf(text) + text.length });

  it('marks a quote of the answer as it stands, whatever its case and «ё»', () => {
    expect(phraseMarks(paragraph, 'Кинжал дарит Сава: «снял с пояса старый кинжал» (гл. 3).')).toEqual([at('снял с пояса старый кинжал')]);
  });

  it('marks the longest run of the same words without a quote, and nothing below three words', () => {
    expect(phraseMarks(paragraph, 'Сава протянул его Маре у костра — так кинжал перешёл к ней.')).toEqual([at('протянул его Маре')]);
    expect(phraseMarks(paragraph, 'Кинжал перешёл к Маре.')).toEqual([]);
    expect(phraseMarks(paragraph, null)).toEqual([]);
  });
});
