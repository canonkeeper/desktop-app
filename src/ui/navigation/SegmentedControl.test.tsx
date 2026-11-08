import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { SegmentedControl } from './SegmentedControl';

describe('SegmentedControl', () => {
  it('keeps one selected option and skips disabled options with the keyboard', () => {
    const onValueChange = vi.fn();
    render(
      <SegmentedControl
        label="Область"
        onValueChange={onValueChange}
        options={[
          { label: 'Абзацы', value: 'paragraphs' },
          { disabled: true, label: 'Недоступно', value: 'disabled' },
          { label: 'Целиком', value: 'whole' },
        ]}
        value="paragraphs"
      />,
    );

    expect(screen.getByRole('radiogroup', { name: 'Область' })).toBeTruthy();
    const segments = screen.getAllByRole('radio');
    expect(segments.map((segment) => segment.getAttribute('aria-checked'))).toEqual(['true', 'false', 'false']);
    expect(segments.map((segment) => segment.tabIndex)).toEqual([0, -1, -1]);

    segments[0]!.focus();
    fireEvent.keyDown(segments[0]!, { key: 'ArrowRight' });
    expect(document.activeElement).toBe(segments[2]);
    expect(onValueChange).toHaveBeenCalledWith('whole');
  });
});
