// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { WorkspaceLayout } from '../WorkspaceLayout';

const dock = vi.hoisted(() => ({ maximized: false }));
vi.mock('../../dock/DockContext', () => ({ useDock: () => dock }));

const FLOOR = '[@media(min-height:640px)]:min-h-[280px]';

function renderLayout() {
  return render(
    <WorkspaceLayout
      bar={<div data-testid="ww-bar" />}
      main={<div data-testid="main" />}
      dock={<section data-testid="ww-dock" />}
    />
  );
}

beforeEach(() => {
  dock.maximized = false;
});

afterEach(() => {
  vi.restoreAllMocks();
});

/** Gives the main zone this top in the viewport; jsdom has no layout. */
function zoneTop(top: number) {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    function (this: HTMLElement) {
      const value = this.dataset.testid === 'ww-zone-main' ? top : 0;
      return { top: value, bottom: value, left: 0, right: 0 } as DOMRect;
    }
  );
}

const NDV_BELOW_BAR =
  '[&_[data-testid=ndv][data-state=docked]:not([data-layout=sheet])]:top-(--ww-zone-top)';

// Browsers apply the classes; e2e/workspace/layout.spec.ts measures them.
describe('WorkspaceLayout', () => {
  it('stacks the bar, the main zone and the dock', () => {
    renderLayout();
    const root = screen.getByTestId('workflow-workspace');
    expect(Array.from(root.children)).toEqual([
      screen.getByTestId('ww-bar'),
      screen.getByTestId('ww-zone-main'),
      screen.getByTestId('ww-dock'),
    ]);
    expect(screen.getByTestId('ww-zone-main')).toContainElement(
      screen.getByTestId('main')
    );
  });

  it('hides the minimap in the main zone below md', () => {
    renderLayout();
    expect(screen.getByTestId('ww-zone-main')).toHaveClass(
      'max-md:[&_[data-testid=flow-minimap]]:hidden'
    );
  });

  // An open dock shrinks (to its bar at least) before the zone goes under
  // the floor, so that the dock can be dragged up to about two thirds.
  it('keeps a floor for the main zone and lets the dock shrink', () => {
    renderLayout();
    expect(screen.getByTestId('ww-zone-main')).toHaveClass(
      'min-h-0',
      'flex-1',
      FLOOR
    );
    expect(screen.getByTestId('workflow-workspace')).toHaveClass(
      'h-full',
      'min-h-0',
      '[&>[data-testid=ww-dock]]:shrink',
      '[&>[data-testid=ww-dock][data-state=open]]:min-h-8'
    );
  });

  it('drops the floor while the dock is maximized', () => {
    dock.maximized = true;
    renderLayout();
    expect(screen.getByTestId('ww-zone-main')).not.toHaveClass(FLOOR);
  });

  // The docked step details are fixed to the window. They start at the top
  // of the main zone, so they never cover the bar and its Run controls.
  it('keeps the docked step details below the bar', () => {
    zoneTop(88);
    renderLayout();
    const root = screen.getByTestId('workflow-workspace');
    expect(root).toHaveClass(NDV_BELOW_BAR);
    expect(root.style.getPropertyValue('--ww-zone-top')).toBe('88px');
  });

  // The expanded step details start at --ndv-top: the top of the main zone,
  // never negative.
  it('puts the top of the main zone in --ndv-top and follows a resize', () => {
    zoneTop(96);
    renderLayout();
    const root = screen.getByTestId('workflow-workspace');
    expect(root.style.getPropertyValue('--ndv-top')).toBe('96px');

    vi.restoreAllMocks();
    zoneTop(130);
    fireEvent(window, new Event('resize'));
    expect(root.style.getPropertyValue('--ndv-top')).toBe('130px');

    vi.restoreAllMocks();
    zoneTop(-20);
    fireEvent(window, new Event('resize'));
    expect(root.style.getPropertyValue('--ndv-top')).toBe('0px');
  });

  it('moves the step details with the zone when the window resizes', () => {
    zoneTop(88);
    renderLayout();
    vi.restoreAllMocks();
    zoneTop(120);
    fireEvent(window, new Event('resize'));
    expect(
      screen
        .getByTestId('workflow-workspace')
        .style.getPropertyValue('--ww-zone-top')
    ).toBe('120px');
  });
});
