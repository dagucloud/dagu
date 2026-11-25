// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PanelPlacementProvider } from '@/features/dags/components/step-details/PanelShell';
import {
  CanvasToolbarHostContext,
  PanelRowTitle,
  TOOLBAR_GROUP_CLASS,
  TOOLBAR_ROW_CLASS,
  TOOLBAR_ZONE_CLASS,
} from '../toolbarHost';

const tokens = (value: string) => value.split(/\s+/);

// The row of the toolbars floats over the graph: it has no background, it
// lets a click through, and each group takes the pointer again.
describe('toolbar row of a graph zone', () => {
  it('floats over the top of the zone with no background and no pointer', () => {
    const row = tokens(TOOLBAR_ROW_CLASS);
    expect(row).toEqual(
      expect.arrayContaining([
        'absolute',
        'inset-x-0',
        'top-0',
        'pointer-events-none',
        'h-10',
      ])
    );
    for (const token of row) {
      expect(token).not.toMatch(/^(bg-|border|shadow)/);
    }
  });

  // A panel with its Close button in the row keeps a slot for it: the zone
  // sets the width of the slot, and the row ends its toolbars before it.
  it('ends the toolbars before the Close button of a panel in the row', () => {
    expect(TOOLBAR_ZONE_CLASS).toContain(
      'has-[[data-panel-close=row]]:[--ww-close-slot:2.5rem]'
    );
    expect(tokens(TOOLBAR_ROW_CLASS)).toContain('pr-(--ww-close-slot)');
  });

  // The run zone sits in the editor zone, whose hidden panels stay mounted. A
  // custom property inherits, so each zone sets the slot itself: a panel of
  // the outer zone never shortens the toolbars of the inner one.
  it('keeps the Close slot of a zone to its own panels', () => {
    expect(tokens(TOOLBAR_ZONE_CLASS)).toContain('[--ww-close-slot:0.5rem]');
  });

  it('keeps the height of the row for the panels', () => {
    expect(TOOLBAR_ZONE_CLASS).toContain('[--ww-toolbar-row:2.5rem]');
    expect(TOOLBAR_ZONE_CLASS).toContain('max-md:[--ww-toolbar-row:0px]');
  });

  it('gives the pointer back to a group and a floating chrome to its buttons', () => {
    const group = tokens(TOOLBAR_GROUP_CLASS);
    expect(group).toContain('pointer-events-auto');
    // No background on the group itself; only on its buttons.
    expect(group.filter((token) => /^bg-|^border|^shadow/.test(token))).toEqual(
      []
    );
    expect(group.some((token) => token.endsWith(':bg-card/90'))).toBe(true);
    // The canvas moves under the row: no blur to repaint.
    expect(group.some((token) => token.includes('backdrop-blur'))).toBe(false);
  });
});

// The title of a panel in the row stands at the left of the band and ends
// before the buttons at the right end of the row.
describe('title of a panel in the row', () => {
  afterEach(() => vi.unstubAllGlobals());

  function renderTitle(host: HTMLElement | undefined, placement = 'zone') {
    return render(
      <PanelPlacementProvider value={placement as 'zone' | 'window'}>
        <CanvasToolbarHostContext.Provider value={host}>
          <PanelRowTitle title="YAML" />
        </CanvasToolbarHostContext.Provider>
      </PanelPlacementProvider>
    );
  }

  it('shows the name, hidden from screen readers, in a zone with a row', () => {
    renderTitle(document.createElement('div'));
    const title = screen.getByTestId('panel-row-title');
    expect(title).toHaveTextContent('YAML');
    expect(title.querySelector('h2')).toHaveAttribute('aria-hidden', 'true');
    expect(title).toHaveClass('pointer-events-none', 'max-md:hidden');
  });

  // The dock has no header row: this button is the Close of the panel. It
  // stands at the right end of the band, apart from the title, and carries the
  // mark that keeps its slot free of the toolbars.
  it('puts a Close button at the right end of the band, marked for the zone', () => {
    const onClose = vi.fn();
    render(
      <PanelPlacementProvider value="zone">
        <CanvasToolbarHostContext.Provider
          value={document.createElement('div')}
        >
          <PanelRowTitle title="YAML" closeLabel="Close" onClose={onClose} />
        </CanvasToolbarHostContext.Provider>
      </PanelPlacementProvider>
    );
    const close = screen.getByRole('button', { name: 'Close' });
    expect(close).toHaveClass('pointer-events-auto');
    expect(screen.getByTestId('panel-row-title')).not.toContainElement(close);
    const slot = close.closest('[data-panel-close="row"]');
    expect(slot).not.toBeNull();
    expect(slot).toHaveClass('absolute', 'right-0', 'top-0', 'w-10');
    fireEvent.click(close);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('has no Close slot without a Close button', () => {
    renderTitle(document.createElement('div'));
    expect(document.querySelector('[data-panel-close]')).toBeNull();
  });

  // A bar with a note keeps its own title: the band has the button alone.
  it('shows the Close button alone without a title', () => {
    render(
      <PanelPlacementProvider value="zone">
        <CanvasToolbarHostContext.Provider
          value={document.createElement('div')}
        >
          <PanelRowTitle closeLabel="Close" onClose={vi.fn()} />
        </CanvasToolbarHostContext.Provider>
      </PanelPlacementProvider>
    );
    expect(screen.queryByTestId('panel-row-title')).toBeNull();
    expect(
      screen
        .getByRole('button', { name: 'Close' })
        .closest('[data-panel-close]')
    ).not.toBeNull();
  });

  it('shows a dot and a smaller line with the title', () => {
    render(
      <PanelPlacementProvider value="zone">
        <CanvasToolbarHostContext.Provider
          value={document.createElement('div')}
        >
          <PanelRowTitle
            title="build"
            subtitle="nightly"
            leading={<i data-testid="dot" />}
          />
        </CanvasToolbarHostContext.Provider>
      </PanelPlacementProvider>
    );
    const title = screen.getByTestId('panel-row-title');
    expect(title).toHaveTextContent('nightly');
    expect(title).toHaveTextContent('build');
    expect(title).toContainElement(screen.getByTestId('dot'));
  });

  it('shows nothing without a row or outside a zone', () => {
    const none = renderTitle(undefined);
    expect(screen.queryByTestId('panel-row-title')).toBeNull();
    none.unmount();
    renderTitle(document.createElement('div'), 'window');
    expect(screen.queryByTestId('panel-row-title')).toBeNull();
  });

  // The observer of the title, which a test calls by hand once it has set the
  // boxes of the groups and of the title.
  function stubResizeObserver() {
    let notify = () => {};
    vi.stubGlobal(
      'ResizeObserver',
      class {
        constructor(callback: ResizeObserverCallback) {
          notify = () => act(() => callback([], {} as ResizeObserver));
        }
        observe() {}
        disconnect() {}
      }
    );
    return () => notify();
  }

  // Gives `element` a box from `left` to `right`, in px of the window.
  function setBox(element: Element, left: number, right: number) {
    vi.spyOn(element, 'getBoundingClientRect').mockReturnValue({
      left,
      right,
      width: right - left,
    } as DOMRect);
  }

  // A row with the two groups that the canvas puts in it.
  function rowWithGroups() {
    const host = document.createElement('div');
    const first = document.createElement('div');
    const last = document.createElement('div');
    host.append(first, last);
    return { host, first, last };
  }

  it('stops before the buttons and follows their width', () => {
    const notify = stubResizeObserver();
    const { host, last } = rowWithGroups();
    renderTitle(host);
    const title = screen.getByTestId('panel-row-title');
    setBox(last, 716, 1000);
    notify();
    expect(title.style.paddingRight).toBe('296px');
    setBox(last, 784, 1000);
    notify();
    expect(title.style.paddingRight).toBe('228px');
  });

  // The title also leaves the slot of the Close button, 40px at the right end.
  it('stops before the Close slot when the panel has a Close button', () => {
    const notify = stubResizeObserver();
    const { host, last } = rowWithGroups();
    render(
      <PanelPlacementProvider value="zone">
        <CanvasToolbarHostContext.Provider value={host}>
          <PanelRowTitle title="YAML" closeLabel="Close" onClose={vi.fn()} />
        </CanvasToolbarHostContext.Provider>
      </PanelPlacementProvider>
    );
    setBox(last, 716, 1000);
    notify();
    expect(screen.getByTestId('panel-row-title').style.paddingRight).toBe(
      '336px'
    );
  });

  // The first group of the row stays over a wide panel: the title starts
  // 12px after it, in place of its own 16px of padding, and follows the left
  // edge of the panel.
  it('starts after the buttons of the row that reach over the panel', () => {
    const notify = stubResizeObserver();
    const { host, first } = rowWithGroups();
    renderTitle(host);
    const title = screen.getByTestId('panel-row-title');
    setBox(first, 8, 420);
    setBox(title, 300, 1000);
    notify();
    expect(title.style.paddingLeft).toBe('116px');
    setBox(title, 400, 1000);
    notify();
    expect(title.style.paddingLeft).toBe('16px');
    setBox(title, 600, 1000);
    notify();
    expect(title.style.paddingLeft).toBe('0px');
  });
});
