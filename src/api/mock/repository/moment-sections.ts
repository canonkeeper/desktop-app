/**
 * The names of the sections of «Что помощник знает к этой главе» in the words of the author, in the order the writing
 * reads them (specs/2026-11-12-ux12-writing.md WR1.1, WR1.4): a copy of `chaptermoment.ViewSections` of the server, held to
 * `backend/internal/domain/chaptermoment/testdata/view-sections.json` by `moment-sections.test.ts`, so that the mock
 * answers what the author reads.
 */
export const momentSections = [
  { key: 'кто_в_кадре', title: 'Герои главы: где впервые и как говорят' },
  { key: 'состояние_на_момент', title: 'Что с героями к этой главе' },
  { key: 'кто_что_знает', title: 'Кто что знает' },
  { key: 'нити', title: 'Что осталось открытым' },
  { key: 'правила_мира', title: 'Предметы, навыки и правила из замысла' },
  { key: 'что_было', title: 'Что было раньше' },
  { key: 'место', title: 'Где встаёт глава' },
  { key: 'стиль', title: 'Стиль ваших глав' },
  { key: 'хвост', title: 'Чем кончилась предыдущая глава' },
  { key: 'дальше', title: 'Что будет позже — в главу не попадёт' },
  { key: 'избегать', title: 'Чего избегать' },
  { key: 'планы_автора', title: 'Ваши планы' },
] as const;

export type MomentSection = (typeof momentSections)[number]['key'];

/** The name of a section for the author. */
export const momentTitle = (key: MomentSection) => momentSections.find((section) => section.key === key)!.title;
