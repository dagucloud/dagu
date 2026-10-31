// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen } from '@testing-library/react';
import { useRef } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  measureRightPanel,
  PANEL_WATCH_MS,
  useRightPanelClearance,
} from '../useRightPanelClearance';

function box(left: number, right: number): DOMRect {
  return {
    left,
    right,
    width: right - left,
    top: 0,
    bottom: 100,
    height: 100,
    x: left,
    y: 0,
    toJSON: () => ({}),
  };
}

function place(element: Element, rect: DOMRect) {
  element.getBoundingClientRect = () => rect;
}

function zoneWith(opts: {
  panel?: [number, number];
  canvas: [number, number];
}) {
  const zone = document.createElement('div');
  place(zone, box(0, 800));
  const canvas = document.createElement('div');
  canvas.dataset.testid = 've-canvas';
  place(canvas, box(...opts.canvas));
  zone.append(canvas);
  if (opts.panel) {
    const panel = document.createElement('div');
    panel.dataset.panelShell = 'right';
    place(panel, box(...opts.panel));
    zone.append(panel);
  }
  return zone;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('measureRightPanel', () => {
  it('is empty without a panel', () => {
    expect(measureRightPanel(zoneWith({ canvas: [0, 800] }))).toEqual({
      overlap: 0,
      free: null,
    });
  });

  // The panel covers the canvas at a medium width.
  it('gives the covered width of a panel over the canvas', () => {
    const zone = zoneWith({ canvas: [0, 800], panel: [272, 800] });
    expect(measureRightPanel(zone)).toEqual({ overlap: 528, free: 272 });
  });

  // The editor kept a padding for the panel at a wide window.
  it('covers nothing when the canvas ends at the panel', () => {
    const zone = zoneWith({ canvas: [0, 240], panel: [240, 800] });
    expect(measureRightPanel(zone)).toEqual({ overlap: 0, free: 240 });
  });
});

describe('useRightPanelClearance', () => {
  function Probe({ trigger }: { trigger: string }) {
    const ref = useRef<HTMLDivElement>(null);
    const { overlap, free } = useRightPanelClearance(ref, trigger);
    return (
      <div ref={ref} data-testid="zone">
        <span data-testid="out">{`${overlap}/${free}`}</span>
      </div>
    );
  }

  it('measures again when the trigger changes', () => {
    vi.stubGlobal('ResizeObserver', undefined);
    const { rerender } = render(<Probe trigger="none" />);
    expect(screen.getByTestId('out')).toHaveTextContent('0/null');

    const zone = screen.getByTestId('zone');
    place(zone, box(0, 800));
    const panel = document.createElement('div');
    panel.dataset.panelShell = 'right';
    place(panel, box(300, 800));
    zone.append(panel);
    act(() => {
      rerender(<Probe trigger="workflow" />);
    });
    expect(screen.getByTestId('out')).toHaveTextContent('500/300');
  });

  // A panel mounts after its claim, and a zone that nobody changes is not
  // watched for ever.
  it('finds a panel that mounts after the trigger changed, for a while', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('ResizeObserver', undefined);
    vi.stubGlobal('requestAnimationFrame', (run: () => void) =>
      setTimeout(run, 0)
    );
    vi.stubGlobal('cancelAnimationFrame', (id: number) => clearTimeout(id));
    try {
      const { rerender } = render(<Probe trigger="none" />);
      act(() => {
        rerender(<Probe trigger="workflow" />);
      });
      const zone = screen.getByTestId('zone');
      place(zone, box(0, 800));
      const panel = document.createElement('div');
      panel.dataset.panelShell = 'right';
      place(panel, box(300, 800));
      await act(async () => {
        zone.append(panel);
      });
      await act(async () => {
        vi.advanceTimersByTime(50);
      });
      expect(screen.getByTestId('out')).toHaveTextContent('500/300');

      // After the watch, a change in the zone asks for no measure.
      await act(async () => {
        vi.advanceTimersByTime(PANEL_WATCH_MS);
      });
      place(panel, box(200, 800));
      await act(async () => {
        zone.append(document.createElement('div'));
      });
      await act(async () => {
        vi.advanceTimersByTime(50);
      });
      expect(screen.getByTestId('out')).toHaveTextContent('500/300');
    } finally {
      vi.useRealTimers();
    }
  });
});
