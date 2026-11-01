// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { useLayoutEffect } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import {
  MINIMAP_FRAME_MS,
  MINIMAP_HIDE_DELAY_MS,
  Controls,
  MiniMap,
} from '../overlays';
import {
  FlowProvider,
  useFlowStore,
  useViewport,
  type FlowStore,
} from '../store';
import type { FlowNode, Viewport } from '../types';

const SIZE = { width: 1000, height: 400 };

const nodes: FlowNode[] = [
  {
    id: 'a',
    type: 'box',
    position: { x: 0, y: 0 },
    width: 240,
    height: 64,
    data: {},
  },
  {
    id: 'b',
    type: 'box',
    position: { x: 300, y: 0 },
    width: 240,
    height: 64,
    data: {},
  },
];

// The store parts that Flow sets in a real canvas: the nodes and the size.
let store: FlowStore;
let viewport: Viewport;
let shownNodes: FlowNode[] = nodes;

function Harness() {
  const current = useFlowStore();
  store = current;
  viewport = useViewport();
  current.setNodes(shownNodes);
  const { setSize } = current;
  useLayoutEffect(() => setSize(SIZE), [setSize]);
  return null;
}

function renderOverlays(
  props: Parameters<typeof Controls>[0] = {}
): ReturnType<typeof render> {
  return render(
    <UserPreferencesProvider>
      <FlowProvider>
        <Harness />
        <Controls {...props} />
        <MiniMap onUserMove={props.onUserMove} />
      </FlowProvider>
    </UserPreferencesProvider>
  );
}

// A pan or zoom by the user; a fit or a reveal passes `byUser` false.
function moveView(next: Viewport, byUser = true) {
  act(() => {
    if (byUser) {
      store.noteUserMove();
    }
    store.setViewport(next);
  });
}

// The flow point under the centre of the view.
function centrePoint(v: Viewport) {
  return {
    x: (SIZE.width / 2 - v.x) / v.zoom,
    y: (SIZE.height / 2 - v.y) / v.zoom,
  };
}

describe('Controls', () => {
  it('has 28 px icon buttons that show their keys in the tooltip', async () => {
    renderOverlays({ shortcuts: { zoomIn: '+', zoomOut: '-', fit: 'F' } });
    for (const [name, key] of [
      ['Zoom out', '-'],
      ['Zoom in', '+'],
      ['Fit view', 'F'],
    ] as const) {
      const button = screen.getByRole('button', { name });
      expect(button).toHaveClass('size-7');
      act(() => button.focus());
      const tip = await screen.findByRole('tooltip');
      expect(tip).toHaveTextContent(`${name}${key}`);
      act(() => button.blur());
    }
  });

  // parity:graph-zoom-controls
  it('shows the zoom as a percent', () => {
    renderOverlays();
    const readout = screen.getByLabelText('Zoom 100%, reset to 100%');
    expect(readout).toHaveTextContent('100%');

    fireEvent.click(screen.getByLabelText('Zoom in'));
    expect(screen.getByTestId('flow-zoom-reset')).toHaveTextContent('125%');
    expect(
      screen.getByLabelText('Zoom 125%, reset to 100%')
    ).toBeInTheDocument();

    moveView({ x: 0, y: 0, zoom: 0.25 });
    expect(screen.getByTestId('flow-zoom-reset')).toHaveTextContent('25%');
  });

  // The buttons walk the ladder of round percentages, one step each.
  it('moves one step of the ladder for each click', () => {
    renderOverlays();
    const readout = () => screen.getByTestId('flow-zoom-reset').textContent;
    const seen = [readout()];
    for (let i = 0; i < 5; i++) {
      fireEvent.click(screen.getByLabelText('Zoom in'));
      seen.push(readout());
    }
    // The flow of the test goes from 10 % to 200 %.
    expect(seen).toEqual(['100%', '125%', '150%', '200%', '200%', '200%']);
    for (let i = 0; i < 4; i++) {
      fireEvent.click(screen.getByLabelText('Zoom out'));
    }
    expect(readout()).toBe('75%');
    for (let i = 0; i < 8; i++) {
      fireEvent.click(screen.getByLabelText('Zoom out'));
    }
    // The ladder stops at the lowest step.
    expect(readout()).toBe('10%');
  });

  it('snaps a zoom between two steps to the next step', () => {
    renderOverlays();
    moveView({ x: 0, y: 0, zoom: 1.08 });
    fireEvent.click(screen.getByLabelText('Zoom in'));
    expect(viewport.zoom).toBe(1.25);
    moveView({ x: 0, y: 0, zoom: 1.08 });
    fireEvent.click(screen.getByLabelText('Zoom out'));
    expect(viewport.zoom).toBe(1);
  });

  // parity:graph-zoom-controls
  it('sets the zoom to 100% around the centre of the view', () => {
    renderOverlays();
    const before = { x: 100, y: 50, zoom: 2 };
    moveView(before);

    fireEvent.click(screen.getByLabelText('Zoom 200%, reset to 100%'));
    expect(viewport.zoom).toBe(1);
    expect(viewport.x).toBeCloseTo(300, 5);
    expect(viewport.y).toBeCloseTo(125, 5);
    expect(centrePoint(viewport).x).toBeCloseTo(centrePoint(before).x, 5);
    expect(centrePoint(viewport).y).toBeCloseTo(centrePoint(before).y, 5);
    expect(screen.getByTestId('flow-zoom-reset')).toHaveTextContent('100%');
  });

  // parity:graph-zoom-controls
  it('reports a zoom by the buttons as a user move, but not a fit', () => {
    const onUserMove = vi.fn();
    const onFitView = vi.fn();
    renderOverlays({ onUserMove, onFitView });

    fireEvent.click(screen.getByLabelText('Zoom in'));
    fireEvent.click(screen.getByLabelText('Zoom out'));
    fireEvent.click(screen.getByLabelText('Zoom 100%, reset to 100%'));
    expect(onUserMove).toHaveBeenCalledTimes(3);
    expect(onFitView).not.toHaveBeenCalled();

    fireEvent.click(screen.getByLabelText('Fit view'));
    expect(onFitView).toHaveBeenCalledTimes(1);
    expect(onUserMove).toHaveBeenCalledTimes(3);
  });

  // parity:graph-zoom-controls
  it('fits the view with the fit button', () => {
    renderOverlays({ fitOptions: { padding: 0.1, maxZoom: 2 } });
    fireEvent.click(screen.getByLabelText('Fit view'));
    // The nodes span 540 x 64: 1000 / (540 * 1.2) = 1.54 fits, and the
    // largest step under it is 150 %.
    expect(viewport.zoom).toBe(1.5);
  });
});

// A recording canvas: jsdom has none. The calls tell what the overview draws.
interface Call {
  canvas: HTMLCanvasElement;
  op: string;
  args: number[];
  style: string;
}

function recordingContext(
  canvas: HTMLCanvasElement,
  calls: Call[]
): CanvasRenderingContext2D {
  const context: Record<string, unknown> = {
    fillStyle: '',
    strokeStyle: '',
    lineWidth: 0,
  };
  for (const op of [
    'setTransform',
    'clearRect',
    'drawImage',
    'beginPath',
    'roundRect',
    'fill',
    'stroke',
    'fillRect',
    'strokeRect',
  ]) {
    context[op] = (...args: number[]) => {
      calls.push({
        canvas,
        op,
        args,
        style: String(
          op === 'stroke' || op === 'strokeRect'
            ? context.strokeStyle
            : context.fillStyle
        ),
      });
    };
  }
  return context as unknown as CanvasRenderingContext2D;
}

describe('MiniMap', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  function minimap(): HTMLElement {
    return screen.getByTestId('flow-minimap');
  }

  let calls: Call[];
  let colors: Record<string, string>;
  const NODE = 'rgb(4, 5, 6)';
  const SELECTED = 'rgb(1, 2, 3)';
  const FILL = 'rgb(7, 8, 9)';

  beforeEach(() => {
    calls = [];
    colors = {
      'text-muted-foreground/40': NODE,
      'text-primary': SELECTED,
      'text-primary/5': FILL,
    };
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(
      function (this: HTMLCanvasElement) {
        return recordingContext(this, calls);
      } as unknown as typeof HTMLCanvasElement.prototype.getContext
    );
    const original = window.getComputedStyle.bind(window);
    vi.spyOn(window, 'getComputedStyle').mockImplementation(
      (element, pseudo) => {
        const color = colors[(element as HTMLElement).className];
        return color
          ? ({ color } as CSSStyleDeclaration)
          : original(element, pseudo);
      }
    );
  });

  const drawn = (op: string) => calls.filter((call) => call.op === op);
  /** Calls of the canvas on screen; the node layer is another canvas. */
  const onScreen = (op: string) =>
    drawn(op).filter((call) => call.canvas.isConnected);
  const frame = () => act(() => vi.advanceTimersByTime(16));
  const last = (list: Call[]): Call => list[list.length - 1]!;

  function panel(): HTMLElement {
    const element = minimap().closest<HTMLElement>('[data-flow-overlay]');
    if (!element) {
      throw new Error('no minimap panel');
    }
    return element;
  }

  function pointer(
    target: Element,
    type: 'pointerdown' | 'pointermove' | 'pointerup',
    x: number,
    y: number
  ) {
    act(() => {
      target.dispatchEvent(
        new MouseEvent(type, {
          bubbles: true,
          cancelable: true,
          clientX: x,
          clientY: y,
          button: 0,
        })
      );
    });
  }

  // parity:canvas-minimap
  it('shows while the view moves and fades out 1 s after the last move', () => {
    renderOverlays();
    expect(minimap().dataset.state).toBe('hidden');
    expect(panel()).toHaveClass('opacity-0', 'pointer-events-none');

    moveView({ x: -10, y: 0, zoom: 1 });
    expect(minimap().dataset.state).toBe('visible');
    expect(panel()).toHaveClass('opacity-100');
    expect(panel()).not.toHaveClass('pointer-events-none');

    act(() => vi.advanceTimersByTime(MINIMAP_HIDE_DELAY_MS - 100));
    expect(minimap().dataset.state).toBe('visible');

    // A new move starts the delay again.
    moveView({ x: -20, y: 0, zoom: 1 });
    act(() => vi.advanceTimersByTime(MINIMAP_HIDE_DELAY_MS - 100));
    expect(minimap().dataset.state).toBe('visible');

    act(() => vi.advanceTimersByTime(100));
    expect(minimap().dataset.state).toBe('hidden');
    expect(panel()).toHaveClass('opacity-0', 'pointer-events-none');
    expect(minimap()).toHaveAttribute('aria-hidden', 'true');
  });

  // A pan is 60 moves a second: they share the fade timer and one draw timer,
  // and the last view is drawn at the end of the move.
  it('sets two timers at most for the moves of a pan, and draws the last', () => {
    renderOverlays();
    moveView({ x: -1, y: 0, zoom: 1 });
    frame();
    const timers = vi.getTimerCount();
    const draws = onScreen('strokeRect').length;
    const first = last(onScreen('strokeRect')).args[0]!;
    for (let i = 2; i < 30; i++) {
      moveView({ x: -i, y: 0, zoom: 1 });
    }
    // One timer for the frame of the view and one for the box.
    expect(vi.getTimerCount()).toBeLessThanOrEqual(timers + 2);
    frame();
    // The 28 moves make one draw, and the view rectangle went to the right.
    expect(onScreen('strokeRect').length).toBe(draws + 1);
    expect(last(onScreen('strokeRect')).args[0]).toBeGreaterThan(first);
    act(() => vi.advanceTimersByTime(MINIMAP_FRAME_MS));
  });

  // The measured cost: a style or an attribute of an SVG costs a layout of the
  // page at each frame. A pan within the graph changes nothing in the DOM.
  it('changes no DOM node while the view moves inside the graph', () => {
    renderOverlays();
    moveView({ x: -1, y: 0, zoom: 1 });
    act(() => vi.advanceTimersByTime(MINIMAP_FRAME_MS));
    const seen: MutationRecord[] = [];
    const observer = new MutationObserver((records) => seen.push(...records));
    observer.observe(panel(), {
      subtree: true,
      childList: true,
      attributes: true,
      characterData: true,
    });
    for (let i = 2; i < 40; i++) {
      moveView({ x: -i, y: 0, zoom: 1 + (i % 2) / 100 });
      frame();
    }
    act(() => vi.advanceTimersByTime(MINIMAP_FRAME_MS));
    observer.takeRecords().forEach((record) => seen.push(record));
    observer.disconnect();
    expect(seen).toHaveLength(0);
    // The rectangle of the view moved on the canvas.
    expect(onScreen('strokeRect').length).toBeGreaterThan(10);
  });

  // The colours come from the theme: a change of the root class reads them again.
  it('reads the colours again when the theme changes', async () => {
    vi.useRealTimers();
    renderOverlays({});
    moveView({ x: -1, y: 0, zoom: 1 });
    await act(async () => {
      await new Promise<void>((resolve) =>
        requestAnimationFrame(() => resolve())
      );
    });
    expect(drawn('fill')[0]!.style).toBe(NODE);
    colors['text-muted-foreground/40'] = 'rgb(9, 9, 9)';
    await act(async () => {
      document.documentElement.classList.add('dark');
      await Promise.resolve();
    });
    document.documentElement.classList.remove('dark');
    expect(last(drawn('fill')).style).toBe('rgb(9, 9, 9)');
  });

  // A fit after a panel opened is not a move of the user.
  it('stays hidden when a fit or a reveal moves the view', () => {
    renderOverlays();
    moveView({ x: -10, y: 0, zoom: 1 }, false);
    expect(minimap().dataset.state).toBe('hidden');
  });

  // A fit during the delay must not leave the overview on for good.
  it('fades out after a move of the user even when a fit follows', () => {
    renderOverlays();
    moveView({ x: -10, y: 0, zoom: 1 });
    act(() => vi.advanceTimersByTime(MINIMAP_HIDE_DELAY_MS / 2));
    moveView({ x: -50, y: 0, zoom: 1.5 }, false);
    expect(minimap().dataset.state).toBe('visible');
    act(() => vi.advanceTimersByTime(MINIMAP_HIDE_DELAY_MS));
    expect(minimap().dataset.state).toBe('hidden');
  });

  // A read-only canvas has no overview while the whole graph is in view.
  it('hides itself while every node is in view when asked', () => {
    const { rerender } = render(
      <UserPreferencesProvider>
        <FlowProvider>
          <Harness />
          <MiniMap hideWhenFitted />
        </FlowProvider>
      </UserPreferencesProvider>
    );
    expect(screen.queryByTestId('flow-minimap')).toBeNull();

    // Node a leaves the view: the overview has somewhere to go.
    moveView({ x: -100, y: 0, zoom: 1 });
    rerender(
      <UserPreferencesProvider>
        <FlowProvider>
          <Harness />
          <MiniMap hideWhenFitted />
        </FlowProvider>
      </UserPreferencesProvider>
    );
    expect(screen.getByTestId('flow-minimap')).toBeInTheDocument();
  });

  // A workflow of 100 steps in a column was a line of 2 px in a box of 200 x 150.
  describe('for a tall graph', () => {
    const column = Array.from({ length: 100 }, (_, i) => ({
      id: `n${i}`,
      type: 'box',
      position: { x: 0, y: i * 96 },
      width: 240,
      height: 64,
      data: {},
    })) as FlowNode[];

    afterEach(() => {
      shownNodes = nodes;
    });

    function renderColumn(props: Parameters<typeof MiniMap>[0] = {}) {
      shownNodes = column;
      return render(
        <UserPreferencesProvider>
          <FlowProvider>
            <Harness />
            <MiniMap {...props} />
          </FlowProvider>
        </UserPreferencesProvider>
      );
    }

    it('is taller than the default and as wide as the graph needs', () => {
      renderColumn();
      const width = Number.parseFloat(minimap().style.width);
      expect(Number.parseFloat(minimap().style.height)).toBe(260);
      expect(width).toBeLessThan(200);
      expect(width).toBeGreaterThanOrEqual(64);
    });

    it('draws a node at least 3 px high', () => {
      renderColumn({ persistent: true });
      const boxes = drawn('roundRect');
      expect(boxes).toHaveLength(column.length);
      // The box has a border of 1 px: 3 px in the overview are 2.97 here.
      const heights = boxes.map((box) => box.args[3]!);
      expect(Math.min(...heights)).toBeGreaterThanOrEqual(2.9);
    });

    it('keeps the default size for a wide graph', () => {
      renderOverlays();
      expect(minimap().style.width).toBe('200px');
      expect(minimap().style.height).toBe('150px');
    });

    // Where the view is must be found without a move: a large graph keeps
    // the overview on at rest.
    it('stays on at a low opacity while the view rests when persistent', () => {
      renderColumn({ persistent: true });
      expect(minimap().dataset.state).toBe('visible');
      expect(minimap()).not.toHaveAttribute('aria-hidden');
      expect(panel()).toHaveClass('opacity-60');
      expect(panel()).not.toHaveClass('pointer-events-none');
    });

    // The node boxes are drawn on a layer that a pan inside the graph does not
    // draw again; only the rectangle of the view moves.
    it('draws the node boxes again only when the graph changes', () => {
      renderColumn({ persistent: true });
      expect(drawn('roundRect')).toHaveLength(column.length);
      const views = onScreen('strokeRect').length;
      for (let i = 1; i < 20; i++) {
        moveView({ x: 0, y: -i * 100, zoom: 1 });
        frame();
      }
      expect(drawn('roundRect')).toHaveLength(column.length);
      expect(onScreen('strokeRect').length).toBeGreaterThan(views + 10);
    });

    // A zoom must not resize the box at each step: that costs a layout.
    it('keeps its size while the view zooms and pans away', () => {
      renderColumn({ persistent: true });
      const size = [minimap().style.width, minimap().style.height];
      for (const zoom of [2, 0.5, 0.2, 1]) {
        moveView({ x: -3000, y: -2000, zoom });
        act(() => vi.advanceTimersByTime(MINIMAP_FRAME_MS));
        expect([minimap().style.width, minimap().style.height]).toEqual(size);
      }
    });

    it('is off at rest when it is not persistent', () => {
      renderColumn();
      expect(minimap().dataset.state).toBe('hidden');
    });
  });

  it('marks the selected node in the colour of the selection', () => {
    shownNodes = [nodes[0]!, { ...nodes[1]!, selected: true }];
    try {
      renderOverlays();
      moveView({ x: -1, y: 0, zoom: 1 });
      frame();
      const fills = drawn('fill').map((call) => call.style);
      expect(fills).toEqual([NODE, SELECTED]);
      // The selected node has an outline in the same colour.
      expect(drawn('stroke').map((call) => call.style)).toEqual([SELECTED]);
    } finally {
      shownNodes = nodes;
    }
  });

  // The overview sits at the bottom left, where the zoom panel was.
  it('is at the bottom left and absent when hidden', () => {
    const { rerender } = renderOverlays();
    expect(panel()).toHaveClass('bottom-2', 'left-2');
    expect(panel().style.right).toBe('');
    rerender(
      <UserPreferencesProvider>
        <FlowProvider>
          <Harness />
          <MiniMap hidden />
        </FlowProvider>
      </UserPreferencesProvider>
    );
    expect(screen.queryByTestId('flow-minimap')).toBeNull();
  });

  // parity:canvas-minimap
  // A zoom at the limit or a fit with the same result sets an equal view.
  it('fades out when an equal view is set during the delay', () => {
    renderOverlays();
    moveView({ x: -10, y: 0, zoom: 2 });
    act(() => vi.advanceTimersByTime(MINIMAP_HIDE_DELAY_MS / 2));
    fireEvent.click(screen.getByLabelText('Zoom in'));
    expect(viewport).toEqual({ x: -10, y: 0, zoom: 2 });

    act(() => vi.advanceTimersByTime(MINIMAP_HIDE_DELAY_MS / 2));
    expect(minimap().dataset.state).toBe('hidden');
  });

  // parity:canvas-minimap
  it('stays while the pointer is on it', () => {
    renderOverlays();
    moveView({ x: -10, y: 0, zoom: 1 });
    fireEvent.pointerEnter(minimap());
    act(() => vi.advanceTimersByTime(MINIMAP_HIDE_DELAY_MS * 3));
    expect(minimap().dataset.state).toBe('visible');

    fireEvent.pointerLeave(minimap());
    expect(minimap().dataset.state).toBe('hidden');
  });

  // parity:canvas-minimap
  it('is not shown below the md breakpoint', () => {
    renderOverlays();
    expect(panel()).toHaveClass('hidden', 'md:block');
  });

  describe('moves the view as before', () => {
    beforeEach(() => {
      vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(
        DOMRect.fromRect({ x: 0, y: 0, width: 200, height: 150 })
      );
    });

    afterEach(() => {
      vi.restoreAllMocks();
    });

    // parity:canvas-minimap
    it('centres the view on a click', () => {
      renderOverlays();
      // The view shows 200..1200 x 0..400; with the nodes the overview
      // spans 0..1200 x 0..400, and its centre is (600, 200).
      moveView({ x: -200, y: 0, zoom: 1 });
      pointer(minimap(), 'pointerdown', 100, 75);
      pointer(minimap(), 'pointerup', 100, 75);
      expect(viewport).toEqual({ x: -100, y: 0, zoom: 1 });
    });

    // parity:canvas-minimap
    it('follows a drag and stops at the release', () => {
      renderOverlays();
      moveView({ x: -200, y: 0, zoom: 1 });
      pointer(minimap(), 'pointerdown', 100, 75);
      const start = viewport;
      pointer(minimap(), 'pointermove', 20, 75);
      expect(viewport.x).toBeGreaterThan(start.x);
      const dragged = viewport;
      pointer(minimap(), 'pointerup', 20, 75);
      pointer(minimap(), 'pointermove', 180, 75);
      expect(viewport).toEqual(dragged);
    });

    // A touch can end in a cancel, without a release.
    it.each(['pointerCancel', 'lostPointerCapture'] as const)(
      'stops a drag at a %s',
      (type) => {
        renderOverlays();
        moveView({ x: -200, y: 0, zoom: 1 });
        pointer(minimap(), 'pointerdown', 100, 75);
        fireEvent[type](minimap());
        const stopped = viewport;
        pointer(minimap(), 'pointermove', 180, 75);
        expect(viewport).toEqual(stopped);
      }
    );

    it('zooms with a wheel that counts lines, as the canvas does', () => {
      renderOverlays();
      moveView({ x: -200, y: 0, zoom: 1 });
      const moves = store.userMoves();
      // Seven lines are 112 px: one step. The event keeps the page still.
      expect(fireEvent.wheel(minimap(), { deltaY: -7, deltaMode: 1 })).toBe(
        false
      );
      expect(viewport.zoom).toBe(1.25);
      expect(store.userMoves()).toBe(moves + 1);
    });

    // A view that the user moved with the overview is not fitted again.
    it('reports a click, a drag and the wheel as user moves', () => {
      const onUserMove = vi.fn();
      renderOverlays({ onUserMove });
      moveView({ x: -200, y: 0, zoom: 1 });
      pointer(minimap(), 'pointerdown', 100, 75);
      pointer(minimap(), 'pointermove', 20, 75);
      pointer(minimap(), 'pointerup', 20, 75);
      expect(onUserMove).toHaveBeenCalledTimes(2);
      fireEvent.wheel(minimap(), { deltaY: -250 });
      expect(onUserMove).toHaveBeenCalledTimes(3);
    });

    // parity:canvas-minimap
    it('zooms around the centre of the view with the wheel', () => {
      renderOverlays();
      const before = { x: -200, y: 0, zoom: 1 };
      moveView(before);
      fireEvent.wheel(minimap(), { deltaY: -250 });
      // One step of the ladder, whatever the size of the move.
      expect(viewport.zoom).toBe(1.25);
      expect(centrePoint(viewport).x).toBeCloseTo(centrePoint(before).x, 5);
      expect(centrePoint(viewport).y).toBeCloseTo(centrePoint(before).y, 5);
    });
  });
});
