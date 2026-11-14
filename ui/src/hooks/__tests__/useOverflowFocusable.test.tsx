// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useOverflowFocusable } from '../useOverflowFocusable';

function ScrollBox() {
  const [ref, tabIndex] = useOverflowFocusable<HTMLDivElement>();
  return (
    <div ref={ref} tabIndex={tabIndex} data-testid="box">
      content
    </div>
  );
}

function stubWidths(scrollWidth: number, clientWidth: number) {
  vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockReturnValue(
    scrollWidth
  );
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(
    clientWidth
  );
}

describe('useOverflowFocusable', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        disconnect() {}
      }
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('makes an overflowing container focusable', () => {
    stubWidths(800, 300);
    render(<ScrollBox />);

    expect(screen.getByTestId('box')).toHaveAttribute('tabindex', '0');
  });

  it('adds no tab stop when the content fits', () => {
    stubWidths(300, 300);
    render(<ScrollBox />);

    expect(screen.getByTestId('box')).not.toHaveAttribute('tabindex');
  });
});
