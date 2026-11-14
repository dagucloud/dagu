// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  OVERLAY_WIDTH_KEY,
  OverlayResizeHandle,
  useOverlayWidth,
} from '../OverlayResizeHandle';

/** The handle of an overlay that shows `width`, the default when null. */
function Harness() {
  const overlay = useOverlayWidth();
  const shown = overlay.width ?? 1000;
  return (
    <>
      <output data-testid="width">{overlay.width ?? 'default'}</output>
      <OverlayResizeHandle
        width={() => shown}
        onResize={overlay.set}
        onReset={overlay.reset}
      />
    </>
  );
}

const separator = () =>
  screen.getByRole('separator', { name: 'Resize workflow view' });
const width = () => screen.getByTestId('width').textContent;

describe('OverlayResizeHandle', () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  // The overlay stands at the right of the window: Left widens it.
  it('changes the width with the arrow keys and keeps it', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(2000);
    render(<Harness />);

    fireEvent.keyDown(separator(), { key: 'ArrowLeft' });
    expect(width()).toBe('1032');
    fireEvent.keyDown(separator(), { key: 'ArrowRight' });
    expect(width()).toBe('1000');
    expect(localStorage.getItem(OVERLAY_WIDTH_KEY)).toBe('1000');
  });

  it('keeps the page below in sight and the overlay usable', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(1200);
    localStorage.setItem(OVERLAY_WIDTH_KEY, '5000');
    render(<Harness />);

    expect(width()).toBe('1104');
    for (let i = 0; i < 30; i++) {
      fireEvent.keyDown(separator(), { key: 'ArrowRight' });
    }
    expect(width()).toBe('640');
  });

  it('gives the default width back on a double click', () => {
    localStorage.setItem(OVERLAY_WIDTH_KEY, '900');
    render(<Harness />);

    fireEvent.doubleClick(separator());

    expect(width()).toBe('default');
    expect(localStorage.getItem(OVERLAY_WIDTH_KEY)).toBeNull();
  });
});
