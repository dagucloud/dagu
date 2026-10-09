// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  GraphResizeHandle,
  useResizableGraphHeight,
} from '../GraphResizeHandle';

const STORAGE_KEY = 'dagu.graph.height';
const MEASURED_HEIGHT = 300;

function ResizableGraph() {
  const { height, graphBoxRef, handleProps } = useResizableGraphHeight();
  return (
    <>
      <div
        ref={graphBoxRef}
        data-testid="graph"
        data-height={height ?? 'auto'}
      />
      <GraphResizeHandle {...handleProps} />
    </>
  );
}

function graphHeight(): string | null {
  return screen.getByTestId('graph').getAttribute('data-height');
}

function handle(): HTMLElement {
  return screen.getByRole('separator', { name: 'Resize graph' });
}

function drag(fromY: number, toY: number) {
  const pointer = { pointerId: 1, button: 0 };
  fireEvent.pointerDown(handle(), { ...pointer, clientY: fromY });
  fireEvent.pointerMove(handle(), { ...pointer, clientY: toY });
  fireEvent.pointerUp(handle(), { ...pointer, clientY: toY });
}

describe('useResizableGraphHeight', () => {
  // jsdom has no layout, so the graph box reports a fixed measured height.
  beforeEach(() => {
    localStorage.clear();
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      height: MEASURED_HEIGHT,
    } as DOMRect);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('lets the graph size itself until it is resized', () => {
    render(<ResizableGraph />);

    expect(graphHeight()).toBe('auto');
  });

  it('remembers a dragged height for later graphs', () => {
    const { unmount } = render(<ResizableGraph />);

    drag(100, 160);

    expect(graphHeight()).toBe('360');
    expect(localStorage.getItem(STORAGE_KEY)).toBe('360');
    unmount();
    render(<ResizableGraph />);
    expect(graphHeight()).toBe('360');
  });

  it('keeps the automatic size after a press without movement', () => {
    render(<ResizableGraph />);

    drag(100, 100);

    expect(graphHeight()).toBe('auto');
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it('stays between the minimum height and the window height', () => {
    render(<ResizableGraph />);

    drag(500, 0);
    expect(graphHeight()).toBe('200');

    drag(0, 5000);
    expect(graphHeight()).toBe(String(window.innerHeight));
  });

  it('resizes from the keyboard', () => {
    render(<ResizableGraph />);

    fireEvent.keyDown(handle(), { key: 'ArrowDown' });
    expect(graphHeight()).toBe('340');

    fireEvent.keyDown(handle(), { key: 'ArrowUp' });
    expect(graphHeight()).toBe('300');

    fireEvent.keyDown(handle(), { key: 'Home' });
    expect(graphHeight()).toBe('200');

    fireEvent.keyDown(handle(), { key: 'End' });
    expect(graphHeight()).toBe(String(window.innerHeight));
  });

  it('returns to the automatic size on double-click', () => {
    localStorage.setItem(STORAGE_KEY, '360');
    render(<ResizableGraph />);

    fireEvent.doubleClick(handle());

    expect(graphHeight()).toBe('auto');
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it('ignores an unusable stored height', () => {
    localStorage.setItem(STORAGE_KEY, 'tall');
    const { unmount } = render(<ResizableGraph />);
    expect(graphHeight()).toBe('auto');
    unmount();

    localStorage.setItem(STORAGE_KEY, '99999');
    render(<ResizableGraph />);
    expect(graphHeight()).toBe(String(window.innerHeight));
  });
});
