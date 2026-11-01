// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useScrollFade } from '../useScrollFade';

function Row() {
  const [ref, style] = useScrollFade<HTMLDivElement>('x');
  return (
    // jsdom drops mask styles, so the mask is exposed for assertions.
    <div ref={ref} data-mask={style.maskImage ?? ''} data-testid="row">
      tabs
    </div>
  );
}

// The row appears only once its content has loaded.
function LoadedRow({ loaded }: { loaded: boolean }) {
  const [ref, style] = useScrollFade<HTMLDivElement>('x');
  if (!loaded) return <p>Loading</p>;
  return (
    <div ref={ref} data-mask={style.maskImage ?? ''} data-testid="row">
      tabs
    </div>
  );
}

// A child added after the row mounts, such as a tab that needs a loaded user.
function GrowingRow({ extra }: { extra: boolean }) {
  const [ref, style] = useScrollFade<HTMLDivElement>('x');
  return (
    <div ref={ref} data-mask={style.maskImage ?? ''} data-testid="row">
      <span>tabs</span>
      {extra && <button type="button">New view</button>}
    </div>
  );
}

function stubScroll(scrollWidth: number, clientWidth: number) {
  vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockReturnValue(
    scrollWidth
  );
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(
    clientWidth
  );
}

describe('useScrollFade', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('fades only the edges that hide content', () => {
    stubScroll(600, 300);
    render(<Row />);
    const row = screen.getByTestId('row');

    expect(row).toHaveAttribute(
      'data-mask',
      'linear-gradient(to right, #000, #000 calc(100% - 24px), transparent)'
    );

    row.scrollLeft = 300;
    fireEvent.scroll(row);
    expect(row).toHaveAttribute(
      'data-mask',
      'linear-gradient(to right, transparent, #000 24px, #000)'
    );
  });

  it('fades a row that appears after loading', () => {
    stubScroll(600, 300);
    const { rerender } = render(<LoadedRow loaded={false} />);

    rerender(<LoadedRow loaded />);

    expect(screen.getByTestId('row')).toHaveAttribute(
      'data-mask',
      'linear-gradient(to right, #000, #000 calc(100% - 24px), transparent)'
    );
  });

  // A new child can make the row overflow without resizing the row itself.
  it('fades a row that a later child makes overflow', async () => {
    stubScroll(300, 300);
    const { rerender } = render(<GrowingRow extra={false} />);
    const row = screen.getByTestId('row');
    expect(row).toHaveAttribute('data-mask', '');

    stubScroll(600, 300);
    rerender(<GrowingRow extra />);

    await waitFor(() =>
      expect(row).toHaveAttribute(
        'data-mask',
        'linear-gradient(to right, #000, #000 calc(100% - 24px), transparent)'
      )
    );
  });

  it('leaves content that fits untouched', () => {
    stubScroll(300, 300);
    render(<Row />);

    expect(screen.getByTestId('row')).toHaveAttribute('data-mask', '');
  });
});
