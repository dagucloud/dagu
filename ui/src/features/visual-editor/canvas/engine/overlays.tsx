// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Maximize, Minus, Plus } from 'lucide-react';
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type PointerEvent as ReactPointerEvent,
  type ReactElement,
  type ReactNode,
} from 'react';
import { IconButton } from '@/components/ui/icon-button';
import { useI18n } from '@/i18n/I18nProvider';
import { cn, FOCUS_RING } from '@/lib/utils';
import {
  backgroundDots,
  boundsOf,
  clamp,
  createWheelStepper,
  stepZoom,
  visibleRect,
  wheelDeltaUnit,
  zoomAt,
  type FitOptions,
  type Rect,
} from './geometry';
import {
  useFlow,
  useFlowStore,
  usePanning,
  useViewportEffect,
  useViewportThrottled,
  useViewportStore,
  useViewportValue,
} from './store';

const PANEL_POSITION = {
  'top-left': 'left-2 top-2',
  'top-right': 'right-2 top-2',
  'bottom-left': 'bottom-2 left-2',
  'bottom-right': 'bottom-2 right-2',
} as const;

/** Content over the canvas, at a corner. It never starts a pan. */
export function Panel({
  position,
  className,
  style,
  children,
}: {
  position: keyof typeof PANEL_POSITION;
  className?: string;
  style?: CSSProperties;
  children: ReactNode;
}): ReactElement {
  return (
    <div
      data-flow-overlay=""
      className={cn(
        'absolute z-10 cursor-default',
        PANEL_POSITION[position],
        className
      )}
      style={style}
    >
      {children}
    </div>
  );
}

function backgroundStyle(
  { x, y, zoom }: { x: number; y: number; zoom: number },
  gap: number,
  size: number
) {
  const { step, opacity } = backgroundDots(gap, zoom);
  const radius = size * Math.max(zoom, 0.5);
  // The dots are one tile larger than the canvas on each side, and the pan
  // moves them by a transform of less than one tile: no repaint for a pan.
  const wrap = (offset: number) => ((offset % step) + step) % step;
  return {
    backgroundImage: `radial-gradient(circle, currentColor ${radius}px, transparent ${radius + 0.5}px)`,
    backgroundSize: `${step}px ${step}px`,
    left: `${-step}px`,
    top: `${-step}px`,
    width: `calc(100% + ${2 * step}px)`,
    height: `calc(100% + ${2 * step}px)`,
    transform: `translate(${wrap(x)}px, ${wrap(y)}px)`,
    opacity: String(opacity),
  };
}

/**
 * Dots that move and zoom with the view. They follow it by their style, not
 * by a draw of the component. A pan changes only a transform, so that the
 * dots keep their raster.
 */
export function Background({
  gap = 16,
  size = 1,
}: {
  gap?: number;
  size?: number;
}): ReactElement {
  const ref = useRef<HTMLDivElement>(null);
  const viewports = useViewportStore();
  const panning = usePanning();
  useViewportEffect((viewport) => {
    if (ref.current) {
      Object.assign(ref.current.style, backgroundStyle(viewport, gap, size));
    }
  });
  return (
    <div
      ref={ref}
      aria-hidden="true"
      data-testid="flow-background"
      className="pointer-events-none absolute z-0 text-border"
      style={{
        ...backgroundStyle(viewports.get(), gap, size),
        willChange: panning ? 'transform' : undefined,
      }}
    />
  );
}

const ZOOM_TEXT_CLASS = cn(
  'flex h-7 min-w-9 items-center justify-center rounded px-1 text-[11px] tabular-nums text-muted-foreground hover:bg-muted hover:text-foreground',
  FOCUS_RING
);

/** Key texts that the zoom buttons show in their tooltips. */
export interface ControlsShortcuts {
  zoomIn?: string;
  zoomOut?: string;
  fit?: string;
}

/**
 * One row of buttons to zoom out, show the zoom and set it back to 100 %, zoom
 * in and fit the view. It has no frame and no corner: the host puts it in its
 * toolbar. `onUserMove` is called when a button changes the zoom.
 */
export function Controls({
  fitOptions,
  onFitView,
  onUserMove,
  shortcuts,
}: {
  fitOptions?: FitOptions;
  onFitView?(): void;
  onUserMove?(): void;
  shortcuts?: ControlsShortcuts;
}): ReactElement {
  const { ts } = useI18n();
  const { zoomIn, zoomOut, fitView, setViewport } = useFlow();
  const store = useFlowStore();
  const percent = useViewportValue((view) => Math.round(view.zoom * 100));
  const resetZoom = () => {
    const { size, zoomLimits, getViewport } = store;
    store.noteUserMove();
    onUserMove?.();
    setViewport(
      zoomAt(
        getViewport(),
        1,
        { x: size.width / 2, y: size.height / 2 },
        zoomLimits
      )
    );
  };
  return (
    <div
      role="group"
      aria-label={ts('Zoom')}
      data-testid="flow-zoom-controls"
      className="flex items-center gap-0.5"
    >
      <IconButton
        label={ts('Zoom out')}
        icon={Minus}
        shortcut={shortcuts?.zoomOut}
        onClick={() => {
          store.noteUserMove();
          onUserMove?.();
          zoomOut();
        }}
      />
      <button
        type="button"
        className={ZOOM_TEXT_CLASS}
        aria-label={ts('Zoom {percent}%, reset to 100%', { percent })}
        title={ts('Reset zoom')}
        data-testid="flow-zoom-reset"
        onClick={resetZoom}
      >
        {percent}%
      </button>
      <IconButton
        label={ts('Zoom in')}
        icon={Plus}
        shortcut={shortcuts?.zoomIn}
        onClick={() => {
          store.noteUserMove();
          onUserMove?.();
          zoomIn();
        }}
      />
      <IconButton
        label={ts('Fit view')}
        icon={Maximize}
        shortcut={shortcuts?.fit}
        onClick={() => {
          onFitView?.();
          fitView(fitOptions);
        }}
      />
    </div>
  );
}

const MINIMAP_WIDTH = 200;
const MINIMAP_HEIGHT = 150;
/** A tall graph gets a tall overview, up to this height, and at least this width. */
const MINIMAP_TALL_HEIGHT = 260;
const MINIMAP_MIN_WIDTH = 96;
const MINIMAP_PADDING = 24;
/**
 * Width in px that a canvas keeps free on the left for a persistent
 * overview: its box and its margin. A fit that uses it as an inset leaves
 * no node under the overview.
 */
export const MINIMAP_RESERVE = MINIMAP_WIDTH + 24;
/** A node is drawn at least this many px wide and high in the overview. */
const MINIMAP_MIN_NODE = 3;
/** True when `inner` lies inside `outer`. */
function within(inner: Rect, outer: Rect): boolean {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.width <= outer.x + outer.width &&
    inner.y + inner.height <= outer.y + outer.height
  );
}

/**
 * Time in ms between two draws of the overview component while the view moves.
 * Its size follows the graph, not the view, so that a pan or a zoom changes
 * nothing in the DOM of the box. The rectangle of the view follows the view at
 * each frame, on the canvas.
 */
export const MINIMAP_FRAME_MS = 66;

/** Time after the last pan or zoom before the overview fades out. */
export const MINIMAP_HIDE_DELAY_MS = 1000;

/** Width in px of the border of the overview box. */
const MINIMAP_BORDER = 1;
/** Corner radius in px of a node in the overview. */
const MINIMAP_NODE_RADIUS = 4;
/** Line width in px of the rectangle of the view. */
const MINIMAP_VIEW_LINE = 1.5;

/** Size of the overview in px, and the part of the flow that it shows. */
export interface MinimapLayout {
  pixelWidth: number;
  pixelHeight: number;
  /** The part of the flow that the box shows, with its padding. */
  box: Rect;
  /** Flow units that one px of the overview shows, with its padding. */
  unit: number;
}

/**
 * A tall graph, such as a workflow of 100 steps in a column, gets a tall
 * overview, so that it is not a line of 2 px. Returns null when there is no
 * node.
 */
export function minimapLayout(
  rects: readonly Rect[],
  view: Rect
): MinimapLayout | null {
  if (rects.length === 0) {
    return null;
  }
  const graph = boundsOf(rects);
  const bounds = boundsOf([...rects, view]);
  if (!graph || !bounds) {
    return null;
  }
  // The size of the overview follows the shape of the graph, not the view: a
  // zoom would else resize the box at each step, which costs a layout. The
  // window of the overview still holds the view, centred in the box.
  const aspect = graph.width / Math.max(graph.height, 1);
  const pixelHeight =
    aspect < MINIMAP_WIDTH / MINIMAP_HEIGHT
      ? clamp(MINIMAP_WIDTH / aspect, MINIMAP_HEIGHT, MINIMAP_TALL_HEIGHT)
      : MINIMAP_HEIGHT;
  const pixelWidth = clamp(
    pixelHeight * aspect,
    MINIMAP_MIN_WIDTH,
    MINIMAP_WIDTH
  );
  const scale = Math.max(
    bounds.width / pixelWidth,
    bounds.height / pixelHeight
  );
  const width = pixelWidth * scale;
  const height = pixelHeight * scale;
  const pad = MINIMAP_PADDING * scale;
  const box = {
    x: bounds.x - (width - bounds.width) / 2 - pad,
    y: bounds.y - (height - bounds.height) / 2 - pad,
    width: width + 2 * pad,
    height: height + 2 * pad,
  };
  return {
    pixelWidth,
    pixelHeight,
    box,
    unit: Math.max(box.width / pixelWidth, box.height / pixelHeight),
  };
}

/** Colours of the overview, read from the theme. */
interface MinimapColors {
  node: string;
  selected: string;
  viewFill: string;
}

/** Flow to canvas px: `px = flow * scale + offset`. The box is centred. */
interface MinimapPlace {
  scale: number;
  x: number;
  y: number;
}

function minimapPlace(
  layout: MinimapLayout,
  width: number,
  height: number
): MinimapPlace {
  const { box } = layout;
  const scale = Math.min(width / box.width, height / box.height);
  return {
    scale,
    x: (width - box.width * scale) / 2 - box.x * scale,
    y: (height - box.height * scale) / 2 - box.y * scale,
  };
}

/** Draws the node boxes. A node that is selected keeps the selection colour. */
export function drawMinimapNodes(
  ctx: CanvasRenderingContext2D,
  rects: readonly Rect[],
  selected: ReadonlySet<number>,
  layout: MinimapLayout,
  place: MinimapPlace,
  colors: MinimapColors
): void {
  const minNode = MINIMAP_MIN_NODE * layout.unit;
  ctx.lineWidth = place.scale;
  rects.forEach((r, index) => {
    const isSelected = selected.has(index);
    ctx.fillStyle = isSelected ? colors.selected : colors.node;
    ctx.beginPath();
    const x = r.x * place.scale + place.x;
    const y = r.y * place.scale + place.y;
    const w = Math.max(r.width, minNode) * place.scale;
    const h = Math.max(r.height, minNode) * place.scale;
    if (ctx.roundRect) {
      ctx.roundRect(x, y, w, h, MINIMAP_NODE_RADIUS);
    } else {
      ctx.rect(x, y, w, h);
    }
    ctx.fill();
    if (isSelected) {
      ctx.strokeStyle = colors.selected;
      ctx.stroke();
    }
  });
}

/** Draws the part that the view shows. */
export function drawMinimapView(
  ctx: CanvasRenderingContext2D,
  view: Rect,
  place: MinimapPlace,
  colors: MinimapColors
): void {
  const x = view.x * place.scale + place.x;
  const y = view.y * place.scale + place.y;
  const w = view.width * place.scale;
  const h = view.height * place.scale;
  ctx.fillStyle = colors.viewFill;
  ctx.fillRect(x, y, w, h);
  ctx.strokeStyle = colors.selected;
  ctx.lineWidth = MINIMAP_VIEW_LINE;
  ctx.strokeRect(x, y, w, h);
}

function sameRects(a: readonly Rect[], b: readonly Rect[]): boolean {
  return (
    a.length === b.length &&
    a.every(
      (r, i) =>
        r.x === b[i]!.x &&
        r.y === b[i]!.y &&
        r.width === b[i]!.width &&
        r.height === b[i]!.height
    )
  );
}

function sameSet(a: ReadonlySet<number>, b: ReadonlySet<number>): boolean {
  return a.size === b.size && [...a].every((index) => b.has(index));
}

/** What the node layer of the overview shows. */
interface MinimapStatic {
  rects: readonly Rect[];
  selected: ReadonlySet<number>;
  box: Rect;
  width: number;
  height: number;
  ratio: number;
  colors: MinimapColors;
}

function sameStatic(a: MinimapStatic | null, b: MinimapStatic): boolean {
  return (
    a !== null &&
    a.width === b.width &&
    a.height === b.height &&
    a.ratio === b.ratio &&
    a.colors === b.colors &&
    a.box.x === b.box.x &&
    a.box.y === b.box.y &&
    a.box.width === b.box.width &&
    a.box.height === b.box.height &&
    sameSet(a.selected, b.selected) &&
    sameRects(a.rects, b.rects)
  );
}

/**
 * The drawing of the overview. The node boxes go to a layer that is drawn
 * again only when the graph, the selection, the box or the theme changes. The
 * rectangle of the view goes over a copy of the layer at each frame of a pan
 * or a zoom. No frame changes the DOM, so that no frame costs a layout.
 */
function MiniMapCanvas({
  rects,
  selected,
  layout,
  size,
  visible,
}: {
  rects: readonly Rect[];
  selected: ReadonlySet<number>;
  layout: MinimapLayout;
  size: { width: number; height: number };
  visible: boolean;
}): ReactElement {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const probesRef = useRef<HTMLSpanElement>(null);
  const viewports = useViewportStore();
  const layer = useRef<HTMLCanvasElement | null>(null);
  const drawn = useRef<MinimapStatic | null>(null);
  // Read again when the theme changes.
  const colors = useRef<MinimapColors | null>(null);
  const live = useRef({ rects, selected, layout, size, visible });
  live.current = { rects, selected, layout, size, visible };

  const paint = useCallback(() => {
    const canvas = canvasRef.current;
    const probes = probesRef.current;
    const current = live.current;
    if (!canvas || !probes || !current.visible) {
      return;
    }
    const context = canvas.getContext('2d');
    if (!context) {
      return;
    }
    if (!colors.current) {
      const [node, selectedColor, viewFill] = Array.from(probes.children).map(
        (probe) => getComputedStyle(probe).color
      );
      colors.current = {
        node: node ?? '',
        selected: selectedColor ?? '',
        viewFill: viewFill ?? '',
      };
    }
    // The box follows the view when the view leaves the graph, at each frame:
    // the node layer and the view rectangle always agree. The size of the
    // overview is the one of the last draw of the component.
    const view = visibleRect(viewports.get(), current.size);
    const layout = minimapLayout(current.rects, view) ?? current.layout;
    const width = current.layout.pixelWidth - 2 * MINIMAP_BORDER;
    const height = current.layout.pixelHeight - 2 * MINIMAP_BORDER;
    const ratio = window.devicePixelRatio || 1;
    const pixelsWide = Math.round(width * ratio);
    const pixelsHigh = Math.round(height * ratio);
    if (canvas.width !== pixelsWide || canvas.height !== pixelsHigh) {
      canvas.width = pixelsWide;
      canvas.height = pixelsHigh;
    }
    const wanted: MinimapStatic = {
      rects: current.rects,
      selected: current.selected,
      box: layout.box,
      width,
      height,
      ratio,
      colors: colors.current,
    };
    const place = minimapPlace(layout, width, height);
    layer.current ??= document.createElement('canvas');
    if (!sameStatic(drawn.current, wanted)) {
      drawn.current = wanted;
      const nodes = layer.current;
      nodes.width = pixelsWide;
      nodes.height = pixelsHigh;
      const nodesContext = nodes.getContext('2d');
      if (nodesContext) {
        nodesContext.setTransform(ratio, 0, 0, ratio, 0, 0);
        drawMinimapNodes(
          nodesContext,
          current.rects,
          current.selected,
          layout,
          place,
          colors.current
        );
      }
    }
    context.setTransform(1, 0, 0, 1, 0, 0);
    context.clearRect(0, 0, pixelsWide, pixelsHigh);
    context.drawImage(layer.current, 0, 0);
    context.setTransform(ratio, 0, 0, ratio, 0, 0);
    drawMinimapView(context, view, place, colors.current);
  }, [viewports]);

  // A draw for each render of the overview: a new node layer or a new box.
  useLayoutEffect(() => {
    paint();
  });

  // A draw for each frame in which the view moves, however many moves came.
  useLayoutEffect(() => {
    let frame: number | undefined;
    const onMove = () => {
      if (frame === undefined) {
        frame = requestAnimationFrame(() => {
          frame = undefined;
          paint();
        });
      }
    };
    const stop = viewports.subscribe(onMove);
    return () => {
      stop();
      if (frame !== undefined) {
        cancelAnimationFrame(frame);
      }
    };
  }, [viewports, paint]);

  // The theme changes the class of the root: read the colours again.
  useEffect(() => {
    const observer = new MutationObserver(() => {
      colors.current = null;
      paint();
    });
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class', 'data-theme'],
    });
    return () => observer.disconnect();
  }, [paint]);

  return (
    <>
      <canvas
        ref={canvasRef}
        aria-hidden="true"
        className="absolute inset-0 size-full"
      />
      {/* The theme colours, read once for each theme change. */}
      <span ref={probesRef} aria-hidden="true" className="hidden">
        <i className="text-muted-foreground/40" />
        <i className="text-primary" />
        <i className="text-primary/5" />
      </span>
    </>
  );
}

/**
 * Overview of all the nodes, with the part that the view shows. A click or a
 * drag on it moves the view, and the wheel zooms it; each of these calls
 * `onUserMove`. It shows while the view moves, fades out after
 * `MINIMAP_HIDE_DELAY_MS`, and stays while the pointer is on it. It sits at
 * the bottom left and is not shown on narrow screens.
 */
export function MiniMap({
  onUserMove,
  hidden = false,
  hideWhenFitted = false,
  persistent = false,
}: {
  onUserMove?(): void;
  /** No overview, for a canvas too small for it. */
  hidden?: boolean;
  /** No overview while every node is in view: there is nowhere to go. */
  hideWhenFitted?: boolean;
  /**
   * The overview stays on at a low opacity while the view rests: in a large
   * graph it tells where the view is, and it can be found without a move.
   */
  persistent?: boolean;
} = {}): ReactElement | null {
  const { ts } = useI18n();
  const store = useFlowStore();
  const viewport = useViewportThrottled(MINIMAP_FRAME_MS);
  const { size } = store;
  const dragging = useRef(false);
  const wheelStep = useRef(createWheelStepper());
  const [moving, setMoving] = useState(false);
  const [hovered, setHovered] = useState(false);
  const last = useRef(viewport);
  const storeRef = useRef(store);
  storeRef.current = store;
  const onUserMoveRef = useRef(onUserMove);
  onUserMoveRef.current = onUserMove;
  const [surface, setSurface] = useState<HTMLDivElement | null>(null);
  const seenMoves = useRef(store.userMoves());
  // One timer for a whole pan: it re-arms for the time left, so that a frame
  // of the pan sets no new timer. A fit or a reveal during the delay does not
  // clear it.
  const hideTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined
  );
  const lastMove = useRef(0);
  const armHide = useCallback((delay: number) => {
    hideTimer.current = setTimeout(() => {
      hideTimer.current = undefined;
      const left = MINIMAP_HIDE_DELAY_MS - (Date.now() - lastMove.current);
      if (left > 0) {
        armHide(left);
        return;
      }
      setMoving(false);
    }, delay);
  }, []);
  useEffect(
    () => () => {
      clearTimeout(hideTimer.current);
      hideTimer.current = undefined;
    },
    []
  );

  // The wheel zooms around the centre of the view. React listens as passive,
  // so this listener is native, to be able to stop the page from scrolling.
  useEffect(() => {
    if (!surface) {
      return;
    }
    const onWheel = (event: WheelEvent) => {
      event.preventDefault();
      const step = wheelStep.current(
        -event.deltaY * wheelDeltaUnit(event.deltaMode) * 0.002,
        event.timeStamp
      );
      if (step === 0) {
        return;
      }
      const flow = storeRef.current;
      const current = flow.getViewport();
      flow.noteUserMove();
      onUserMoveRef.current?.();
      flow.setViewport(
        zoomAt(
          current,
          stepZoom(current.zoom, step, flow.zoomLimits),
          { x: flow.size.width / 2, y: flow.size.height / 2 },
          flow.zoomLimits
        )
      );
    };
    surface.addEventListener('wheel', onWheel, { passive: false });
    return () => surface.removeEventListener('wheel', onWheel);
  }, [surface]);

  // Keyed on the values: an equal view set again must not cancel the fade.
  // Only a move of the user shows it: a fit or a reveal does not.
  const { x, y, zoom } = viewport;
  useEffect(() => {
    const previous = last.current;
    last.current = { x, y, zoom };
    const moves = storeRef.current.userMoves();
    const byUser = moves !== seenMoves.current;
    seenMoves.current = moves;
    if (previous.x === x && previous.y === y && previous.zoom === zoom) {
      return;
    }
    if (!byUser) {
      return;
    }
    setMoving(true);
    lastMove.current = Date.now();
    if (hideTimer.current === undefined) {
      armHide(MINIMAP_HIDE_DELAY_MS);
    }
  }, [x, y, zoom, armHide]);

  const rects = store.nodeRects();
  // The selected nodes keep the colour of the selection in the overview.
  const selectedRects = new Set(
    store.nodes().flatMap((node, index) => (node.selected ? [index] : []))
  );
  const view = visibleRect(viewport, size);
  const layout = minimapLayout(rects, view);
  if (hidden || !layout || size.width === 0) {
    return null;
  }
  if (hideWhenFitted && rects.every((rect) => within(rect, view))) {
    return null;
  }
  const { pixelWidth, pixelHeight, box } = layout;

  const centerOn = (event: ReactPointerEvent<HTMLDivElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    if (rect.width === 0) {
      return;
    }
    const fx = box.x + ((event.clientX - rect.left) / rect.width) * box.width;
    const fy = box.y + ((event.clientY - rect.top) / rect.height) * box.height;
    const current = store.getViewport();
    store.noteUserMove();
    onUserMove?.();
    store.setViewport({
      ...current,
      x: size.width / 2 - fx * current.zoom,
      y: size.height / 2 - fy * current.zoom,
    });
  };

  // A cancelled gesture, such as a system gesture on a touch screen, ends the
  // drag as a release does.
  const stopDragging = () => {
    dragging.current = false;
  };

  const visible = moving || hovered || persistent;
  return (
    <Panel
      position="bottom-left"
      className={cn(
        'hidden motion-safe:transition-opacity motion-safe:duration-300 md:block',
        visible ? 'opacity-100' : 'pointer-events-none opacity-0',
        persistent && !moving && !hovered && 'opacity-60'
      )}
    >
      <div
        ref={setSurface}
        role="img"
        aria-label={ts('Overview of the workflow')}
        data-testid="flow-minimap"
        data-flow-nowheel=""
        data-state={visible ? 'visible' : 'hidden'}
        aria-hidden={visible ? undefined : true}
        style={{ width: pixelWidth, height: pixelHeight }}
        className="relative cursor-pointer rounded-md border bg-card/90 shadow-sm"
        onPointerDown={(event) => {
          dragging.current = true;
          event.currentTarget.setPointerCapture?.(event.pointerId);
          centerOn(event);
        }}
        onPointerMove={(event) => {
          if (dragging.current) {
            centerOn(event);
          }
        }}
        onPointerUp={stopDragging}
        onPointerCancel={stopDragging}
        onLostPointerCapture={stopDragging}
        onPointerEnter={() => setHovered(true)}
        onPointerLeave={() => setHovered(false)}
      >
        <MiniMapCanvas
          rects={rects}
          selected={selectedRects}
          layout={layout}
          size={size}
          visible={visible}
        />
      </div>
    </Panel>
  );
}
