import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { orderedSteps, renderCard } from './BookIndexingCard.test-helpers';

describe('active indexing presentation', () => {
  it('leaves the dismiss minimum to UIKit while preserving its fine size', async () => {
    renderCard({ steps: orderedSteps });
    const dismiss = await screen.findByRole('button', { name: 'Скрыть подготовку' });
    const style = getComputedStyle(dismiss);
    expect(style.width).toBe('32px');
    expect(style.height).toBe('32px');
    expect(style.minHeight).toBe('');
  });

  it('keeps desktop progress spacing separate from the tablet layout', async () => {
    renderCard({ steps: [{ kind: 'text_index', status: 'running', progress: 6 / 14, counters: { indexedChapters: 6, totalChapters: 14 } }] });
    const progress = await screen.findByRole('progressbar');
    const step = progress.closest('li')!;
    const detail = within(step).getByText('Готово для поиска 6 из 14 глав.');
    const rules = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules]);
    const desktop = rules
      .filter((rule): rule is CSSMediaRule => rule instanceof CSSMediaRule && rule.conditionText === '(min-width: 1101px)')
      .flatMap((rule) => [...rule.cssRules])
      .filter((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule);
    const spacing = (element: Element, property: string) => desktop.find((rule) => (
      rule.style.getPropertyValue(property) && element.matches(rule.selectorText)
    ))?.style.getPropertyValue(property);

    expect(spacing(step, 'row-gap')).toBe('0');
    expect(spacing(detail, 'margin-top')).toBe('6px');
    expect(spacing(progress.parentElement!, 'margin-top')).toBe('4px');
  });

  it('uses the standard Lucide stroke for header and step spinners', async () => {
    renderCard({ steps: orderedSteps });
    await screen.findByRole('list', { name: 'Этапы подготовки' });
    const spinners = document.querySelectorAll('svg.lucide-loader-circle');
    expect(spinners).toHaveLength(2);
    for (const spinner of spinners) {
      expect(Number.parseFloat(getComputedStyle(spinner).strokeWidth || spinner.getAttribute('stroke-width')!)).toBe(2);
    }
  });

  it('uses native pending circles only inside the active tablet breakpoint', async () => {
    renderCard({ steps: orderedSteps });
    const list = await screen.findByRole('list', { name: 'Этапы подготовки' });
    const pending = list.querySelectorAll('li[data-status="pending"]');
    expect(pending).toHaveLength(2);
    for (const step of pending) {
      const icon = step.querySelector('svg.lucide-circle');
      expect(icon).not.toBeNull();
      expect(icon!.getAttribute('width')).toBe('18');
      expect(icon!.getAttribute('height')).toBe('18');
      expect(icon!.getAttribute('stroke-width')).toBe('2');
      expect(icon!.parentElement!.getAttribute('aria-hidden')).toBe('true');
      expect(getComputedStyle(icon!).display).toBe('none');
    }
    const icon = pending[0]!.querySelector('svg.lucide-circle')!;
    const media = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSMediaRule => rule instanceof CSSMediaRule);
    const visibleScopes = media.filter((rule) => [...rule.cssRules].some((style) => (
      style instanceof CSSStyleRule && style.style.display === 'block' && icon.matches(style.selectorText)
    )));
    expect(visibleScopes.map((rule) => rule.conditionText)).toEqual(['(min-width: 761px) and (max-width: 1100px)']);
    const tablet = [...visibleScopes[0]!.cssRules].filter((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule);
    expect(tablet.some((rule) => rule.style.border === '0' && icon.parentElement!.matches(rule.selectorText))).toBe(true);
    expect(tablet.find((rule) => rule.style.color && icon.matches(rule.selectorText))!.style.color).toBe('var(--ck-muted-foreground)');
  });

  it('keeps the standard CircleCheck stroke on completed tablet markers', async () => {
    renderCard({ steps: orderedSteps });
    const list = await screen.findByRole('list', { name: 'Этапы подготовки' });
    const marker = list.querySelector('li[data-status="succeeded"] svg.lucide-circle-check')!;
    expect(marker.getAttribute('stroke-width')).toBe('2');
    const rules = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules])
      .flatMap((rule) => rule instanceof CSSMediaRule ? [...rule.cssRules] : [rule]);
    expect(rules.filter((rule) => rule instanceof CSSStyleRule
      && rule.style.getPropertyValue('stroke-width')
      && marker.matches(rule.selectorText))).toHaveLength(0);
  });
});
