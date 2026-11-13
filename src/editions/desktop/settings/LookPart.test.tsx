// SPDX-License-Identifier: AGPL-3.0-only
import { fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { SITE_THEME_STORAGE_KEY, useSiteThemeStore } from '../../../shared/theme/site-theme';
import { LookPart } from './LookPart';
import { appOnTheComputer, noShell, renderPart } from './settings.test-helpers';

afterEach(() => {
  noShell();
  localStorage.clear();
  useSiteThemeStore.setState({ choice: 'light', theme: 'light' });
});

// specs/2026-11-13-desk-settings.md ST3.1 (board 44·13): the same choice of the theme of the same owner as the site's.
describe('«Вид» of «Настройки»', () => {
  it('chooses the theme of the app as the site does and says what «Как в системе» means', () => {
    useSiteThemeStore.setState({ choice: 'system', theme: 'light' });
    renderPart(LookPart, appOnTheComputer);
    const theme = screen.getByRole('radiogroup', { name: 'Тема' });
    expect(screen.getByRole('radio', { name: 'Как в системе' }).getAttribute('aria-checked')).toBe('true');
    expect(theme.textContent).toBe('СветлаяТёмнаяКак в системе');
    expect(screen.getByText('«Как в системе» — как на этом компьютере: приложение станет тёмным вместе с ним.')).toBeTruthy();
    fireEvent.click(screen.getByRole('radio', { name: 'Тёмная' }));
    expect(localStorage.getItem(SITE_THEME_STORAGE_KEY)).toBe('dark');
    expect(useSiteThemeStore.getState().choice).toBe('dark');
  });
});
