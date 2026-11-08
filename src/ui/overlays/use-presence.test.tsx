import { act, render, screen } from '@testing-library/react';
import { useLayoutEffect, useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { usePresence } from './use-presence';

function Harness({ open }: { open: boolean }) {
  const presence = usePresence(open);
  if (!presence.mounted) {
    return <p>closed</p>;
  }
  return <p data-state={presence.status}>open</p>;
}

describe('usePresence', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('mounts immediately when the value is active', () => {
    render(<Harness open />);
    expect(screen.getByText('open').getAttribute('data-state')).toBe('open');
  });

  it('unmounts synchronously when matchMedia is unavailable (jsdom / reduced motion)', () => {
    const { rerender } = render(<Harness open />);
    expect(screen.queryByText('open')).not.toBeNull();

    act(() => {
      rerender(<Harness open={false} />);
    });

    expect(screen.queryByText('open')).toBeNull();
    expect(screen.queryByText('closed')).not.toBeNull();
  });

  it('does not mount a value that was closed from the first render', () => {
    vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false })));

    render(<Harness open={false} />);

    expect(screen.queryByText('open')).toBeNull();
    expect(screen.getByText('closed')).toBeTruthy();
  });

  it('mounts synchronously on a false-to-true transition', () => {
    vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false })));
    let openingCommitMounted: boolean | null = null;

    function TransitionHarness({ open }: { open: boolean }) {
      const presence = usePresence(open);
      useLayoutEffect(() => {
        if (open) openingCommitMounted = presence.mounted;
      }, [open, presence.mounted]);
      return presence.mounted ? <p data-state={presence.status}>open</p> : <p>closed</p>;
    }

    const { rerender } = render(<TransitionHarness open={false} />);
    expect(screen.getByText('closed')).toBeTruthy();

    act(() => {
      rerender(<TransitionHarness open />);
    });

    expect(openingCommitMounted).toBe(true);
    expect(screen.getByText('open').getAttribute('data-state')).toBe('open');
  });

  it('retains the last truthy value while mounted', () => {
    function ValueHarness() {
      const [value] = useState<string | null>('chapter-7');
      const presence = usePresence(value);
      return presence.mounted ? <p>{presence.value}</p> : <p>gone</p>;
    }

    render(<ValueHarness />);
    expect(screen.queryByText('chapter-7')).not.toBeNull();
  });
});
