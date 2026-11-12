import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { BackLine, backWords } from './BackLine';

// specs/2026-11-10-ux2-patterns-states.md PS4.1, PS4.2: «← К <откуда>» — the word of the section and the name of its object.
describe('BackLine', () => {
  it('names where it leads and steps back on a press', () => {
    const onBack = vi.fn();
    render(<BackLine onBack={onBack} words="К чату «Кто такой Сава?»" />);
    const line = screen.getByRole('navigation', { name: 'Возврат' });
    fireEvent.click(screen.getByRole('button', { name: 'К чату «Кто такой Сава?»' }));
    expect(onBack).toHaveBeenCalledOnce();
    expect(line.querySelector('svg')!.getAttribute('aria-hidden')).toBe('true');
  });

  // The table «откуда → слова строки».
  it('says the section and the name of the object it came from', () => {
    expect([
      backWords({ section: 'chat', name: 'Кто такой Сава?' }),
      backWords({ section: 'chat' }),
      backWords({ section: 'saga', name: 'Мара' }),
      backWords({ section: 'saga' }),
      backWords({ section: 'writing', name: 'Встреча у ворот' }),
      backWords({ section: 'writing', name: '  ' }),
      backWords({ section: 'drawing', name: 'Кира у ворот' }),
      backWords({ section: 'notes' }),
      backWords({ section: 'manuscript', name: 'Белый порт' }),
      backWords({ section: 'manuscript' }),
    ]).toEqual([
      'К чату «Кто такой Сава?»', 'К чату', 'К карте: Мара', 'К карте', 'К главе «Встреча у ворот»', 'К главе в работе',
      'К рисунку «Кира у ворот»', 'К заметкам', 'К главе «Белый порт»', 'К главе в книге',
    ]);
  });

  // specs/2026-11-11-ux9-account.md AC2.6: the lists of the sections, from the menu of the account.
  it('names a list of a section by the word of its menu', () => {
    expect([
      backWords({ section: 'books' }), backWords({ section: 'writings' }), backWords({ section: 'drawings' }), backWords({ section: 'memory' }),
    ]).toEqual(['К книгам', 'К «Письму»', 'К рисункам', 'К «Что помнит помощник»']);
  });

  it('keeps the beginning of a long question', () => {
    const words = backWords({ section: 'chat', name: 'Почему Сава не узнал Киру у северных ворот в ночь пожара и что он видел' });
    expect(words).toBe('К чату «Почему Сава не узнал Киру у северных…»');
  });
});
