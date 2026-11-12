import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SkeletonArea, skeletonDelayMs } from './Skeleton';

// specs/2026-11-10-ux2-patterns-states.md PS3.1: the form of what comes, after 200 ms, announced as busy, not by words.
describe('SkeletonArea', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('holds the place of the content at once and shows its form only after 200 ms', () => {
    const view = render(<SkeletonArea count={4} shape="rows" />);
    const block = view.container.firstElementChild as HTMLElement;
    expect(block.getAttribute('aria-busy')).toBe('true');
    expect(block.children).toHaveLength(4);
    expect(block.hasAttribute('data-shown')).toBe(false);
    act(() => vi.advanceTimersByTime(skeletonDelayMs - 1));
    expect(block.hasAttribute('data-shown')).toBe(false);
    act(() => vi.advanceTimersByTime(1));
    expect(block.hasAttribute('data-shown')).toBe(true);
  });

  it('says nothing in words and hides its shapes from the reader of the screen', () => {
    const view = render(<SkeletonArea shape="page" />);
    const block = view.container.firstElementChild as HTMLElement;
    expect(block.textContent).toBe('');
    expect(block.getAttribute('role')).toBeNull();
    expect(block.getAttribute('aria-label')).toBeNull();
    expect([...block.querySelectorAll('[aria-hidden="true"]')].length).toBeGreaterThanOrEqual(5);
  });

  it('draws the form of what comes: tiles, cards, lines', () => {
    expect(render(<SkeletonArea count={6} shape="tiles" />).container.querySelectorAll('[aria-hidden="true"]')).toHaveLength(6);
    expect(render(<SkeletonArea shape="cards" />).container.querySelectorAll('[aria-hidden="true"]')).toHaveLength(3);
    expect(render(<SkeletonArea count={2} shape="lines" />).container.querySelectorAll('[aria-hidden="true"]')).toHaveLength(2);
  });
});
