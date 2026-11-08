import { render, screen, within } from '@testing-library/react';
import { ChevronRight } from 'lucide-react';
import { describe, expect, it } from 'vitest';
import { Breadcrumbs } from './Breadcrumbs';
import styles from './navigation.module.css';

describe('Breadcrumbs', () => {
  it('обозначает путь списком внутри именованной навигации', () => {
    render(
      <Breadcrumbs label="Путь к главе">
        <li>
          <button type="button">Рукопись</button>
        </li>
        <li aria-current="page"><ChevronRight aria-hidden="true" size={14} />Глава 1</li>
      </Breadcrumbs>,
    );

    const navigation = screen.getByRole('navigation', { name: 'Путь к главе' });
    expect(within(navigation).getByRole('list')).toBeTruthy();
    expect(within(navigation).getAllByRole('listitem')).toHaveLength(2);
    expect(within(navigation).getByText('Глава 1').getAttribute('aria-current')).toBe('page');
    expect(navigation.querySelectorAll('.lucide-chevron-right')).toHaveLength(1);
    const generatedSeparators = [...document.styleSheets]
      .flatMap((sheet) => [...sheet.cssRules])
      .filter((rule) => rule instanceof CSSStyleRule && rule.selectorText === `.${styles.breadcrumbs} li + li::before`);
    expect(generatedSeparators).toHaveLength(0);
  });
});
