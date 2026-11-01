// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactElement,
  type ReactNode,
} from 'react';
import {
  boundsOf,
  fitViewport,
  stepZoom,
  zoomAt,
  type FitOptions,
  type PlacedHandle,
  type Position,
  type Rect,
} from './geometry';
import type { FlowNode, HandleType, Viewport, XY } from './types';

/** A handle with its place inside its node. */
export interface RelativeHandle {
  type: HandleType;
  position: Position;
  /** Center of the handle from the top-left corner of the node. */
  x: number;
  y: number;
  connectable: boolean;
}

export interface FlowApi {
  /** Shows all the nodes. Does nothing while the canvas has no size. */
  fitView(options?: FitOptions): void;
  zoomIn(): void;
  zoomOut(): void;
  setViewport(viewport: Viewport): void;
}

/** What the parts of the flow share: view, size, nodes and handle places. */
export interface FlowStore extends FlowApi {
  /** The current view. Read it in an event; `useViewport` draws on change. */
  getViewport(): Viewport;
  size: { width: number; height: number };
  setSize(size: { width: number; height: number }): void;
  /** Set by the flow on each render, read by fit, minimap and connections. */
  setNodes(nodes: readonly FlowNode[]): void;
  nodes(): readonly FlowNode[];
  nodeRects(): Rect[];
  /** Registers a handle. Returns true when something changed. */
  setHandle(nodeId: string, handleId: string | null, h: RelativeHandle): void;
  removeHandle(nodeId: string, handleId: string | null, type: HandleType): void;
  /** Every registered handle in flow coordinates, in registration order. */
  placedHandles(): PlacedHandle[];
  placedHandle(
    nodeId: string,
    handleId: string | null,
    type: HandleType
  ): PlacedHandle | null;
  /** Changes with each handle change, so that edges draw again. */
  handleVersion: number;
  /** Counts the pans and zooms of the user; a fit or a reveal is not one. */
  userMoves(): number;
  noteUserMove(): void;
  zoomLimits: { min: number; max: number };
}

/**
 * The view lives outside React state: a pan or a zoom changes it at each
 * frame, and no component draws again for it unless the component listens.
 */
export interface ViewportStore {
  get(): Viewport;
  /** Calls `listener` after each change. Returns the function that stops it. */
  subscribe(listener: () => void): () => void;
}

const FlowStoreContext = createContext<FlowStore | null>(null);
/** True while the user pans: the parts that follow the view get their own layer. */
export const PanningContext = createContext(false);
const FALLBACK_VIEWPORT: Viewport = { x: 0, y: 0, zoom: 1 };
const FALLBACK_VIEWPORT_STORE: ViewportStore = {
  get: () => FALLBACK_VIEWPORT,
  subscribe: () => () => undefined,
};
const ViewportStoreContext = createContext<ViewportStore>(
  FALLBACK_VIEWPORT_STORE
);
// The commands do not change, so a user of them draws again for no pan or zoom.
const FlowApiContext = createContext<FlowApi | null>(null);

function handleKey(
  nodeId: string,
  handleId: string | null,
  type: HandleType
): string {
  return `${nodeId}\u0000${handleId ?? ''}\u0000${type}`;
}

export function FlowProvider({
  children,
  minZoom = 0.1,
  maxZoom = 2,
}: {
  children: ReactNode;
  minZoom?: number;
  maxZoom?: number;
}): ReactElement {
  const [size, setSizeState] = useState({ width: 0, height: 0 });
  const [handleVersion, setHandleVersion] = useState(0);
  const nodesRef = useRef<readonly FlowNode[]>([]);
  const handlesRef = useRef(
    new Map<
      string,
      { nodeId: string; handleId: string | null } & RelativeHandle
    >()
  );
  const viewportRef = useRef<Viewport>({ x: 0, y: 0, zoom: 1 });
  const viewportListeners = useRef(new Set<() => void>());
  const viewportStore = useMemo<ViewportStore>(
    () => ({
      get: () => viewportRef.current,
      subscribe: (listener) => {
        viewportListeners.current.add(listener);
        return () => {
          viewportListeners.current.delete(listener);
        };
      },
    }),
    []
  );
  const sizeRef = useRef(size);
  sizeRef.current = size;
  const limits = useMemo(
    () => ({ min: minZoom, max: maxZoom }),
    [minZoom, maxZoom]
  );

  const setViewport = useCallback((next: Viewport) => {
    const current = viewportRef.current;
    if (
      current.x === next.x &&
      current.y === next.y &&
      current.zoom === next.zoom
    ) {
      return;
    }
    viewportRef.current = next;
    viewportListeners.current.forEach((listener) => listener());
  }, []);
  const setSize = useCallback((next: { width: number; height: number }) => {
    setSizeState((current) =>
      current.width === next.width && current.height === next.height
        ? current
        : next
    );
  }, []);

  const nodeRects = useCallback(
    (): Rect[] =>
      nodesRef.current.map((node) => ({
        x: node.position.x,
        y: node.position.y,
        width: node.width,
        height: node.height,
      })),
    []
  );

  const fitView = useCallback(
    (options: FitOptions = {}) => {
      const bounds = boundsOf(nodeRects());
      const { width, height } = sizeRef.current;
      if (!bounds || width === 0 || height === 0) {
        return;
      }
      setViewport(
        fitViewport(
          bounds,
          { width, height },
          { minZoom: limits.min, maxZoom: limits.max, ...options }
        )
      );
    },
    [nodeRects, setViewport, limits]
  );
  const zoomBy = useCallback(
    (direction: 1 | -1) => {
      const { width, height } = sizeRef.current;
      const center: XY = { x: width / 2, y: height / 2 };
      const current = viewportRef.current;
      setViewport(
        zoomAt(
          current,
          stepZoom(current.zoom, direction, limits),
          center,
          limits
        )
      );
    },
    [setViewport, limits]
  );
  const zoomIn = useCallback(() => zoomBy(1), [zoomBy]);
  const zoomOut = useCallback(() => zoomBy(-1), [zoomBy]);

  const setNodes = useCallback((nodes: readonly FlowNode[]) => {
    nodesRef.current = nodes;
  }, []);
  const nodes = useCallback(() => nodesRef.current, []);

  const setHandle = useCallback(
    (nodeId: string, handleId: string | null, h: RelativeHandle) => {
      const key = handleKey(nodeId, handleId, h.type);
      const previous = handlesRef.current.get(key);
      if (
        previous &&
        previous.x === h.x &&
        previous.y === h.y &&
        previous.position === h.position &&
        previous.connectable === h.connectable
      ) {
        return;
      }
      handlesRef.current.set(key, { nodeId, handleId, ...h });
      setHandleVersion((v) => v + 1);
    },
    []
  );
  const removeHandle = useCallback(
    (nodeId: string, handleId: string | null, type: HandleType) => {
      if (handlesRef.current.delete(handleKey(nodeId, handleId, type))) {
        setHandleVersion((v) => v + 1);
      }
    },
    []
  );

  const placedHandle = useCallback(
    (nodeId: string, handleId: string | null, type: HandleType) => {
      const rel = handlesRef.current.get(handleKey(nodeId, handleId, type));
      const node = nodesRef.current.find((n) => n.id === nodeId);
      if (!rel || !node) {
        return null;
      }
      return {
        nodeId,
        handleId,
        type,
        position: rel.position,
        x: node.position.x + rel.x,
        y: node.position.y + rel.y,
      };
    },
    []
  );
  const placedHandles = useCallback((): PlacedHandle[] => {
    const placed: PlacedHandle[] = [];
    const byId = new Map(nodesRef.current.map((n) => [n.id, n]));
    for (const rel of handlesRef.current.values()) {
      if (!rel.connectable) {
        continue;
      }
      const node = byId.get(rel.nodeId);
      if (node) {
        placed.push({
          nodeId: rel.nodeId,
          handleId: rel.handleId,
          type: rel.type,
          position: rel.position,
          x: node.position.x + rel.x,
          y: node.position.y + rel.y,
        });
      }
    }
    return placed;
  }, []);

  const getViewport = useCallback(() => viewportRef.current, []);
  const userMovesRef = useRef(0);
  const userMoves = useCallback(() => userMovesRef.current, []);
  const noteUserMove = useCallback(() => {
    userMovesRef.current += 1;
  }, []);

  const store = useMemo<FlowStore>(
    () => ({
      getViewport,
      size,
      setSize,
      setNodes,
      nodes,
      nodeRects,
      setHandle,
      removeHandle,
      placedHandles,
      placedHandle,
      handleVersion,
      userMoves,
      noteUserMove,
      zoomLimits: limits,
      fitView,
      zoomIn,
      zoomOut,
      setViewport,
    }),
    [
      getViewport,
      size,
      setSize,
      setNodes,
      nodes,
      nodeRects,
      setHandle,
      removeHandle,
      placedHandles,
      placedHandle,
      handleVersion,
      userMoves,
      noteUserMove,
      limits,
      fitView,
      zoomIn,
      zoomOut,
      setViewport,
    ]
  );

  const api = useMemo<FlowApi>(
    () => ({ fitView, zoomIn, zoomOut, setViewport }),
    [fitView, zoomIn, zoomOut, setViewport]
  );

  return (
    <FlowApiContext.Provider value={api}>
      <FlowStoreContext.Provider value={store}>
        <ViewportStoreContext.Provider value={viewportStore}>
          {children}
        </ViewportStoreContext.Provider>
      </FlowStoreContext.Provider>
    </FlowApiContext.Provider>
  );
}

/** The store of the nearest `FlowProvider`. */
export function useFlowStore(): FlowStore {
  const store = useContext(FlowStoreContext);
  if (!store) {
    throw new Error('The flow parts need a FlowProvider above them.');
  }
  return store;
}

/** The view store of the nearest `FlowProvider`, to read and listen outside render. */
export function useViewportStore(): ViewportStore {
  return useContext(ViewportStoreContext);
}

/** True while the user drags the view. */
export function usePanning(): boolean {
  return useContext(PanningContext);
}

const NEVER: ViewportStore['subscribe'] = () => () => undefined;

/** The current view, drawn again at each pan and zoom. */
export function useViewport(): Viewport {
  const store = useViewportStore();
  return useSyncExternalStore(store.subscribe, store.get);
}

/**
 * The view, drawn again at each pan and zoom only while `listening` is true.
 * Without it, the value is the view of the last render of the component. For
 * a part that nobody sees, such as a hidden toolbar, which reads the view when
 * it appears.
 */
export function useViewportWhile(listening: boolean): Viewport {
  const store = useViewportStore();
  return useSyncExternalStore(listening ? store.subscribe : NEVER, store.get);
}

/**
 * The view, drawn again at most once for each `intervalMs`, with a last draw
 * for the end of a move. For a part that costs a layout at each draw, such as
 * an SVG, and that a human reads at a lower rate than the pan.
 */
export function useViewportThrottled(intervalMs: number): Viewport {
  const store = useViewportStore();
  const [view, setView] = useState(store.get);
  useEffect(() => {
    let last = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const draw = () => {
      timer = undefined;
      last = Date.now();
      setView(store.get());
    };
    const onChange = () => {
      if (timer !== undefined) {
        return;
      }
      const wait = last + intervalMs - Date.now();
      if (wait <= 0) {
        draw();
      } else {
        timer = setTimeout(draw, wait);
      }
    };
    // A change between the draw and the listener.
    if (store.get() !== view) {
      onChange();
    }
    const stop = store.subscribe(onChange);
    return () => {
      stop();
      clearTimeout(timer);
    };
    // `view` is only the first value to compare with.
  }, [store, intervalMs]);
  return view;
}

/** A value of the view, drawn again only when this value changes. */
export function useViewportValue<T extends number | string | boolean>(
  select: (viewport: Viewport) => T
): T {
  const store = useViewportStore();
  return useSyncExternalStore(store.subscribe, () => select(store.get()));
}

/**
 * Runs `apply` with the view after the first draw and after each change,
 * without a new draw of the component. For a node of the DOM that follows the
 * view by its style.
 */
export function useViewportEffect(apply: (viewport: Viewport) => void): void {
  const store = useViewportStore();
  const latest = useRef(apply);
  latest.current = apply;
  useLayoutEffect(() => {
    const run = () => latest.current(store.get());
    run();
    return store.subscribe(run);
  }, [store]);
}

/** The commands of the nearest flow: fit the view, zoom. */
export function useFlow(): FlowApi {
  const api = useContext(FlowApiContext);
  if (!api) {
    throw new Error('useFlow needs a FlowProvider above it.');
  }
  return api;
}
