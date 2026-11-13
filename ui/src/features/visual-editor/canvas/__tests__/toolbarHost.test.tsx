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
    expect(tokens(TOOLBAR_ROW_CLASS)).toContain('pr-(--ww-close-slot,0.5rem)');
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

  it('stops before the buttons and follows their width', () => {
    let notify: ResizeObserverCallback = () => {};
    vi.stubGlobal(
      'ResizeObserver',
      class {
        constructor(callback: ResizeObserverCallback) {
          notify = callback;
        }
        observe() {}
        disconnect() {}
      }
    );
    const host = document.createElement('div');
    host.append(document.createElement('div'), document.createElement('div'));
    renderTitle(host);
    const title = screen.getByTestId('panel-row-title');
    const resize = (width: number) =>
      act(() =>
        notify(
          [{ contentRect: { width } } as ResizeObserverEntry],
          {} as ResizeObserver
        )
      );
    resize(284);
    expect(title.style.right).toBe('296px');
    resize(216);
    expect(title.style.right).toBe('228px');
  });

  // The title also leaves the slot of the Close button, 40px at the right end.
  it('stops before the Close slot when the panel has a Close button', () => {
    let notify: ResizeObserverCallback = () => {};
    vi.stubGlobal(
      'ResizeObserver',
      class {
        constructor(callback: ResizeObserverCallback) {
          notify = callback;
        }
        observe() {}
        disconnect() {}
      }
    );
    const host = document.createElement('div');
    host.append(document.createElement('div'), document.createElement('div'));
    render(
      <PanelPlacementProvider value="zone">
        <CanvasToolbarHostContext.Provider value={host}>
          <PanelRowTitle title="YAML" closeLabel="Close" onClose={vi.fn()} />
        </CanvasToolbarHostContext.Provider>
      </PanelPlacementProvider>
    );
    act(() =>
      notify(
        [{ contentRect: { width: 284 } } as ResizeObserverEntry],
        {} as ResizeObserver
      )
    );
    expect(screen.getByTestId('panel-row-title').style.right).toBe('336px');
  });
});
