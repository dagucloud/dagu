// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  memo,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ComponentType,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactElement,
  type ReactNode,
} from 'react';
import { cn } from '@/lib/utils';
import {
  createEdgeHoverStore,
  EdgeHoverContext,
  EdgeLabelRootContext,
  NodeBoxesContext,
  type NodeBox,
} from './edgeParts';
import {
  getCurvePath,
  nearestHandle,
  Position,
  screenToFlow,
  boundsOf,
  clampPanToContent,
  createWheelStepper,
  stepZoom,
  wheelDeltaUnit,
  zoomAt,
  type FitOptions,
  type PlacedHandle,
} from './geometry';
import { NodeContext } from './Handle';
import {
  PanningContext,
  useFlowStore,
  useViewportEffect,
  useViewportStore,
} from './store';
import type {
  Connection,
  ConnectionEndState,
  EdgeProps,
  FlowEdge,
  FlowNode,
  HandleType,
  NodeProps,
  Viewport,
  XY,
} from './types';

/** Pointer travel in px that turns a click into a drag. */
const DRAG_THRESHOLD = 4;
/** Distance in px from a drop to a handle that still connects. */
const CONNECT_RADIUS = 20;
const WHEEL_ZOOM_SPEED = 0.002;
const PINCH_ZOOM_SPEED = 0.01;
/** Idle time in ms after which a wheel event reads the place of the canvas again. */
const WHEEL_PLACE_MS = 250;
/** A touch held this long, in ms, opens the menu as a right-click does. */
export const LONG_PRESS_MS = 500;

/** Client coordinates where a menu opens. */
export interface MenuPoint {
  clientX: number;
  clientY: number;
}

export interface FlowProps<N extends FlowNode, E extends FlowEdge> {
  nodes: N[];
  edges: E[];
  nodeTypes: Record<string, ComponentType<NodeProps<N>>>;
  edgeTypes: Record<string, ComponentType<EdgeProps<E>>>;
  onNodeClick?(id: string): void;
  onNodeDoubleClick?(id: string): void;
  onEdgeClick?(id: string): void;
  onPaneClick?(): void;
  /**
   * Right-click, touch long-press or a bubbling native `contextmenu` event
   * on a node, an edge or the pane. With a handler, the browser menu stays
   * closed.
   */
  onNodeContextMenu?(id: string, point: MenuPoint): void;
  onEdgeContextMenu?(id: string, point: MenuPoint): void;
  onPaneContextMenu?(point: MenuPoint): void;
  /** New top-left positions of nodes that the user drags. */
  onNodesMove?(moved: Record<string, XY>): void;
  /** The user pans or zooms. Fits and other commands do not call it. */
  onUserMove?(): void;
  onConnect?(connection: Connection): void;
  /** A drag from a handle was dropped. A cancelled drag calls nothing. */
  onConnectEnd?(end: ConnectionEndState): void;
  isValidConnection?(
    connection: Pick<Connection, 'source' | 'target'>
  ): boolean;
  nodesDraggable?: boolean;
  nodesConnectable?: boolean;
  /** Fits the view once, when the nodes first have a place on screen. */
  fitOnInit?: boolean;
  fitOptions?: FitOptions;
  /**
   * What the wheel does. 'zoom' (default) zooms around the pointer. 'pan'
   * moves the view, and a wheel with Ctrl or Cmd (a pinch) still zooms.
   */
  wheel?: 'zoom' | 'pan';
  className?: string;
  /** Panel, Background, Controls and MiniMap. */
  children?: ReactNode;
}

type Gesture =
  | {
      kind: 'pan';
      start: XY;
      origin: XY;
      moved: boolean;
    }
  | {
      kind: 'node';
      id: string;
      start: XY;
      origin: XY;
      moved: boolean;
    }
  | {
      kind: 'connect';
      from: PlacedHandle;
      start: XY;
      moved: boolean;
    };

type MenuTarget =
  | { kind: 'node'; id: string }
  | { kind: 'edge'; id: string }
  | { kind: 'pane' };

/**
 * Where a menu opens. A keyboard or a script often gives no coordinates:
 * then the menu opens at the bottom-left corner of the element.
 */
function menuPoint(
  event: { clientX: number; clientY: number },
  element: Element
): MenuPoint {
  if (event.clientX !== 0 || event.clientY !== 0) {
    return { clientX: event.clientX, clientY: event.clientY };
  }
  const rect = element.getBoundingClientRect();
  return { clientX: rect.left, clientY: rect.bottom };
}

interface ConnectionLine {
  from: PlacedHandle;
  to: XY;
}

/**
 * The canvas of a graph of nodes and edges, with pan, zoom, drag of nodes
 * and links drawn between handles. The caller owns all the state: it gets
 * events and gives new props. A `FlowProvider` must be above it.
 */
export function Flow<N extends FlowNode, E extends FlowEdge>(
  props: FlowProps<N, E>
): ReactElement {
  return <FlowView {...props} />;
}

function FlowView<N extends FlowNode, E extends FlowEdge>({
  nodes,
  edges,
  nodeTypes,
  edgeTypes,
  onNodeClick,
  onNodeDoubleClick,
  onEdgeClick,
  onPaneClick,
  onNodeContextMenu,
  onEdgeContextMenu,
  onPaneContextMenu,
  onNodesMove,
  onUserMove,
  onConnect,
  onConnectEnd,
  isValidConnection,
  nodesDraggable = true,
  nodesConnectable = true,
  fitOnInit = true,
  fitOptions,
  wheel = 'zoom',
  className,
  children,
}: FlowProps<N, E>): ReactElement {
  const store = useFlowStore();
  const { size, handleVersion } = store;
  const rootRef = useRef<HTMLDivElement>(null);
  const wheelStep = useRef(createWheelStepper());
  const [labelRoot, setLabelRoot] = useState<HTMLElement | null>(null);
  const [connection, setConnection] = useState<ConnectionLine | null>(null);
  const [panning, setPanning] = useState(false);
  const gesture = useRef<Gesture | null>(null);
  const swallowClick = useRef(false);
  const fitted = useRef(false);
  const arrowId = useId();

  store.setNodes(nodes);

  // Latest props for the listeners that live longer than one render.
  const live = useRef({
    store,
    nodes,
    onNodesMove,
    onUserMove,
    onConnect,
    onConnectEnd,
    isValidConnection,
    onNodeContextMenu,
    onEdgeContextMenu,
    onPaneContextMenu,
    wheel,
  });
  live.current = {
    store,
    nodes,
    onNodesMove,
    onUserMove,
    onConnect,
    onConnectEnd,
    isValidConnection,
    onNodeContextMenu,
    onEdgeContextMenu,
    onPaneContextMenu,
    wheel,
  };

  // The size of the canvas, for fit and zoom commands.
  useLayoutEffect(() => {
    const element = rootRef.current;
    if (!element) {
      return;
    }
    const measure = () =>
      store.setSize({
        width: element.clientWidth,
        height: element.clientHeight,
      });
    measure();
    if (typeof ResizeObserver === 'undefined') {
      return;
    }
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
    // The store setter is stable.
  }, [store.setSize]);

  // The first fit, once there is something to fit and room for it.
  useEffect(() => {
    if (
      fitted.current ||
      !fitOnInit ||
      nodes.length === 0 ||
      size.width === 0 ||
      size.height === 0
    ) {
      return;
    }
    fitted.current = true;
    store.fitView(fitOptions);
  }, [fitOnInit, fitOptions, nodes.length, size, store]);

  const pointOf = useCallback((event: { clientX: number; clientY: number }) => {
    const rect = rootRef.current?.getBoundingClientRect();
    return {
      x: event.clientX - (rect?.left ?? 0),
      y: event.clientY - (rect?.top ?? 0),
    };
  }, []);

  // The wheel zooms around the pointer, or pans the view. React listens as passive, so this
  // listener is native, to be able to stop the page from scrolling. The events
  // of one frame change the view one time, at the next frame, and they share
  // one read of the place of the canvas.
  useEffect(() => {
    const element = rootRef.current;
    if (!element) {
      return;
    }
    let pending: Viewport | null = null;
    let frame: number | undefined;
    let place: DOMRect | null = null;
    let lastEvent = -Infinity;
    const commit = () => {
      frame = undefined;
      if (pending) {
        const next = pending;
        pending = null;
        live.current.store.setViewport(next);
      }
    };
    const change = (next: Viewport) => {
      pending = next;
      if (frame === undefined) {
        frame = requestAnimationFrame(commit);
      }
    };
    const onWheel = (event: WheelEvent) => {
      if ((event.target as HTMLElement).closest('[data-flow-nowheel]')) {
        return;
      }
      event.preventDefault();
      const { store: s, onUserMove: moved, wheel: mode } = live.current;
      const unit = wheelDeltaUnit(event.deltaMode);
      const view = pending ?? s.getViewport();
      if (mode === 'pan' && !event.ctrlKey && !event.metaKey) {
        // The content stays in reach: the wheel never scrolls it away.
        change(
          clampPanToContent(
            {
              ...view,
              x: view.x - event.deltaX * unit,
              y: view.y - event.deltaY * unit,
            },
            boundsOf(s.nodeRects()),
            s.size
          )
        );
        s.noteUserMove();
        moved?.();
        return;
      }
      // One step of the ladder for each notch of the wheel, or for each
      // pinch of the same size.
      const speed = event.ctrlKey ? PINCH_ZOOM_SPEED : WHEEL_ZOOM_SPEED;
      const step = wheelStep.current(
        -event.deltaY * unit * speed,
        event.timeStamp
      );
      if (step === 0) {
        return;
      }
      // A new burst of wheel events reads the place again: the canvas may
      // have moved since the last one.
      if (!place || event.timeStamp - lastEvent > WHEEL_PLACE_MS) {
        place = element.getBoundingClientRect();
      }
      lastEvent = event.timeStamp;
      const anchor = {
        x: event.clientX - place.left,
        y: event.clientY - place.top,
      };
      change(
        zoomAt(
          view,
          stepZoom(view.zoom, step, s.zoomLimits),
          anchor,
          s.zoomLimits
        )
      );
      s.noteUserMove();
      moved?.();
    };
    element.addEventListener('wheel', onWheel, { passive: false });
    return () => {
      element.removeEventListener('wheel', onWheel);
      if (frame !== undefined) {
        cancelAnimationFrame(frame);
      }
    };
  }, []);

  const endGesture = useCallback(
    (event: PointerEvent) => {
      const g = gesture.current;
      gesture.current = null;
      setPanning(false);
      window.removeEventListener('pointermove', listeners.current.move);
      window.removeEventListener('pointerup', listeners.current.end);
      window.removeEventListener('pointercancel', listeners.current.end);
      if (!g) {
        return;
      }
      swallowClick.current = g.moved;
      if (g.kind !== 'connect') {
        return;
      }
      setConnection(null);
      // A cancelled drag is no drop: it neither links nor reports an end.
      if (!g.moved || event.type === 'pointercancel') {
        return;
      }
      const {
        store: s,
        onConnect: connect,
        onConnectEnd: ended,
      } = live.current;
      const valid = live.current.isValidConnection;
      const view = s.getViewport();
      const flowPoint = screenToFlow(pointOf(event), view);
      const opposite: HandleType =
        g.from.type === 'source' ? 'target' : 'source';
      const target = nearestHandle(
        s.placedHandles().filter((h) => h.type === opposite),
        flowPoint,
        CONNECT_RADIUS / view.zoom
      );
      const link: Connection | null = target
        ? g.from.type === 'source'
          ? {
              source: g.from.nodeId,
              sourceHandle: g.from.handleId,
              target: target.nodeId,
              targetHandle: target.handleId,
            }
          : {
              source: target.nodeId,
              sourceHandle: target.handleId,
              target: g.from.nodeId,
              targetHandle: g.from.handleId,
            }
        : null;
      const isValid = link ? (valid ? valid(link) : true) : null;
      if (link && isValid) {
        connect?.(link);
      }
      ended?.({
        isValid,
        fromNode: { id: g.from.nodeId },
        fromHandle: { type: g.from.type, id: g.from.handleId },
        toNode: target ? { id: target.nodeId } : null,
      });
    },
    [pointOf]
  );

  const move = useCallback(
    (event: PointerEvent) => {
      const g = gesture.current;
      if (!g) {
        return;
      }
      const dx = event.clientX - g.start.x;
      const dy = event.clientY - g.start.y;
      if (!g.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD) {
        return;
      }
      const { store: s } = live.current;
      if (g.kind === 'pan') {
        if (!g.moved) {
          live.current.onUserMove?.();
          setPanning(true);
        }
        // Each frame is a move of the user: the overview stays while it lasts.
        s.noteUserMove();
        g.moved = true;
        s.setViewport({
          ...s.getViewport(),
          x: g.origin.x + dx,
          y: g.origin.y + dy,
        });
      } else if (g.kind === 'node') {
        g.moved = true;
        live.current.onNodesMove?.({
          [g.id]: {
            x: Math.round(g.origin.x + dx / s.getViewport().zoom),
            y: Math.round(g.origin.y + dy / s.getViewport().zoom),
          },
        });
      } else {
        g.moved = true;
        setConnection({
          from: g.from,
          to: screenToFlow(pointOf(event), s.getViewport()),
        });
      }
    },
    [pointOf]
  );

  // The window holds the same listener functions from the start of a gesture
  // to its end, and they call the latest handlers.
  const handlers = useRef({ move, end: endGesture });
  handlers.current = { move, end: endGesture };
  const listeners = useRef({
    move: (event: PointerEvent) => handlers.current.move(event),
    end: (event: PointerEvent) => handlers.current.end(event),
  });

  const begin = useCallback((next: Gesture) => {
    gesture.current = next;
    swallowClick.current = false;
    window.addEventListener('pointermove', listeners.current.move);
    window.addEventListener('pointerup', listeners.current.end);
    window.addEventListener('pointercancel', listeners.current.end);
  }, []);

  const longPress = useRef<{
    timer: ReturnType<typeof setTimeout>;
    start: XY;
  } | null>(null);
  const cancelLongPress = useCallback(() => {
    if (longPress.current) {
      clearTimeout(longPress.current.timer);
      longPress.current = null;
    }
  }, []);

  useEffect(() => {
    const held = listeners.current;
    return () => {
      cancelLongPress();
      window.removeEventListener('pointermove', held.move);
      window.removeEventListener('pointerup', held.end);
      window.removeEventListener('pointercancel', held.end);
    };
  }, [cancelLongPress]);

  const openMenu = useCallback((target: MenuTarget, point: MenuPoint) => {
    const handlers = live.current;
    if (target.kind === 'node') {
      handlers.onNodeContextMenu?.(target.id, point);
    } else if (target.kind === 'edge') {
      handlers.onEdgeContextMenu?.(target.id, point);
    } else {
      handlers.onPaneContextMenu?.(point);
    }
  }, []);

  const hasMenu = (target: MenuTarget): boolean =>
    target.kind === 'node'
      ? !!onNodeContextMenu
      : target.kind === 'edge'
        ? !!onEdgeContextMenu
        : !!onPaneContextMenu;

  // A touch held still opens the menu; the drag or pan that it began stops.
  const armLongPress = (target: HTMLElement, start: XY) => {
    cancelLongPress();
    if (target.closest('[data-flow-overlay], [data-flow-handle]')) {
      return;
    }
    const node = target.closest<HTMLElement>('[data-flow-node]');
    const edge = target.closest<HTMLElement>('[data-flow-edge]');
    const menu: MenuTarget = node
      ? { kind: 'node', id: node.dataset.flowNode ?? '' }
      : edge
        ? { kind: 'edge', id: edge.dataset.flowEdge ?? '' }
        : { kind: 'pane' };
    if (!hasMenu(menu)) {
      return;
    }
    const timer = setTimeout(() => {
      longPress.current = null;
      gesture.current = null;
      setPanning(false);
      setConnection(null);
      window.removeEventListener('pointermove', listeners.current.move);
      window.removeEventListener('pointerup', listeners.current.end);
      window.removeEventListener('pointercancel', listeners.current.end);
      swallowClick.current = true;
      openMenu(menu, { clientX: start.x, clientY: start.y });
    }, LONG_PRESS_MS);
    longPress.current = { timer, start };
  };

  const onPointerMoveRoot = (event: ReactPointerEvent<HTMLDivElement>) => {
    const pending = longPress.current;
    if (
      pending &&
      Math.hypot(
        event.clientX - pending.start.x,
        event.clientY - pending.start.y
      ) >= DRAG_THRESHOLD
    ) {
      cancelLongPress();
    }
  };

  const onContextMenuRoot = (event: ReactMouseEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement;
    if (
      !onPaneContextMenu ||
      target.closest('[data-flow-overlay], [data-flow-node], [data-flow-edge]')
    ) {
      return;
    }
    event.preventDefault();
    cancelLongPress();
    onPaneContextMenu(menuPoint(event, event.currentTarget));
  };

  const onNodeMenu = useCallback(
    (id: string, event: ReactMouseEvent<HTMLElement>) => {
      if (!live.current.onNodeContextMenu) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();
      cancelLongPress();
      openMenu({ kind: 'node', id }, menuPoint(event, event.target as Element));
    },
    [cancelLongPress, openMenu]
  );

  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) {
      return;
    }
    const target = event.target as HTMLElement;
    const start = { x: event.clientX, y: event.clientY };
    swallowClick.current = false;
    if (event.pointerType === 'touch') {
      armLongPress(target, start);
    }
    if (target.closest('[data-flow-overlay]')) {
      return;
    }
    const handle = target.closest<HTMLElement>('[data-flow-handle]');
    if (handle) {
      if (!nodesConnectable || handle.dataset.connectable !== 'true') {
        return;
      }
      const from = store.placedHandle(
        handle.dataset.nodeId ?? '',
        handle.dataset.handleId ?? null,
        handle.dataset.handleType as HandleType
      );
      if (from) {
        event.preventDefault();
        begin({ kind: 'connect', from, start, moved: false });
      }
      return;
    }
    if (target.closest('.nodrag, .nopan')) {
      return;
    }
    const nodeElement = target.closest<HTMLElement>('[data-flow-node]');
    if (nodeElement) {
      const id = nodeElement.dataset.flowNode ?? '';
      const node = nodes.find((n) => n.id === id);
      if (node && nodesDraggable) {
        begin({ kind: 'node', id, start, origin: node.position, moved: false });
      }
      return;
    }
    if (target.closest('[data-flow-edge]')) {
      return;
    }
    begin({
      kind: 'pan',
      start,
      origin: {
        x: store.getViewport().x,
        y: store.getViewport().y,
      },
      moved: false,
    });
  };

  const onClickCapture = (event: ReactMouseEvent<HTMLDivElement>) => {
    if (swallowClick.current) {
      swallowClick.current = false;
      event.stopPropagation();
    }
  };

  const onClick = (event: ReactMouseEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement;
    if (
      target.closest(
        '[data-flow-node], [data-flow-edge], [data-flow-overlay], [data-flow-handle]'
      )
    ) {
      return;
    }
    onPaneClick?.();
  };

  const nodeById = useMemo(
    () => new Map(nodes.map((node) => [node.id, node])),
    [nodes]
  );
  const connectable = nodesConnectable;

  const drawnEdges = useMemo(
    () => {
      void handleVersion;
      return edges.flatMap((edge) => {
        const sourceNode = nodeById.get(edge.source);
        const targetNode = nodeById.get(edge.target);
        const Component = edgeTypes[edge.type];
        if (!sourceNode || !targetNode || !Component) {
          return [];
        }
        const source =
          store.placedHandle(
            edge.source,
            edge.sourceHandle ?? null,
            'source'
          ) ?? store.placedHandle(edge.source, null, 'source');
        const target =
          store.placedHandle(
            edge.target,
            edge.targetHandle ?? null,
            'target'
          ) ?? store.placedHandle(edge.target, null, 'target');
        // The handles tell their place after the first draw of their node.
        const sourcePoint = source ?? {
          x: sourceNode.position.x + sourceNode.width,
          y: sourceNode.position.y + sourceNode.height / 2,
          position: Position.Right,
        };
        const targetPoint = target ?? {
          x: targetNode.position.x,
          y: targetNode.position.y + targetNode.height / 2,
          position: Position.Left,
        };
        return [{ edge, Component, sourcePoint, targetPoint }];
      });
    },
    // The store reads refs that change with the version.
    [edges, nodeById, edgeTypes, store, handleVersion]
  );

  // The preview follows the pointer and ends on it: no gap before the end.
  const connectionPath = connection
    ? getCurvePath({
        sourceX: connection.from.x,
        sourceY: connection.from.y,
        sourcePosition: connection.from.position,
        targetX: connection.to.x,
        targetY: connection.to.y,
        targetPosition: oppositeOf(connection.from.position),
        targetGap: 0,
      })[0]
    : null;

  const boxes = useMemo<NodeBox[]>(
    () =>
      nodes.map((node) => ({
        id: node.id,
        x: node.position.x,
        y: node.position.y,
        width: node.width,
        height: node.height,
      })),
    [nodes]
  );
  // The pointer on an edge is a change of this store, not of a state: no
  // component draws again. The hovered edge gets a `data-hovered` attribute,
  // which its line reads by CSS, and moves after the other edges.
  const [hover] = useState(createEdgeHoverStore);
  const edgesRef = useRef<SVGSVGElement>(null);
  useEffect(() => {
    let lit: {
      group: Element;
      parent: Node;
      next: ChildNode | null;
    } | null = null;
    const clear = () => {
      if (!lit) {
        return;
      }
      lit.group.removeAttribute('data-hovered');
      // Back to its place, unless a draw has moved the neighbours.
      if (
        lit.next &&
        lit.next.parentNode === lit.parent &&
        lit.group.parentNode === lit.parent
      ) {
        lit.parent.insertBefore(lit.group, lit.next);
      }
      lit = null;
    };
    const apply = () => {
      const id = hover.hovered();
      if (lit && lit.group.getAttribute('data-flow-edge') === id) {
        return;
      }
      clear();
      const groups = Array.from(edgesRef.current?.children ?? []).filter(
        (child) => child.hasAttribute('data-flow-edge')
      );
      const group = groups.find(
        (child) => child.getAttribute('data-flow-edge') === id
      );
      const last = groups[groups.length - 1];
      if (!group || !last || !group.parentNode) {
        return;
      }
      lit = {
        group,
        parent: group.parentNode,
        next: group.nextSibling,
      };
      group.setAttribute('data-hovered', '');
      // Two edges that share the start of their path look the same whichever
      // one the user points at: the hovered one is drawn over the others.
      if (last !== group) {
        group.parentNode.insertBefore(group, last.nextSibling);
      }
    };
    const stop = hover.subscribe(apply);
    return () => {
      stop();
      clear();
    };
  }, [hover]);
  // The selected edge is drawn over the others.
  const stacked = useMemo(() => {
    const level = (edge: FlowEdge) => (edge.selected ? 1 : 0);
    return drawnEdges
      .map((drawn, index) => ({ drawn, index }))
      .sort(
        (a, b) => level(a.drawn.edge) - level(b.drawn.edge) || a.index - b.index
      )
      .map(({ drawn }) => drawn);
  }, [drawnEdges]);

  return (
    <EdgeLabelRootContext.Provider value={labelRoot}>
      <NodeBoxesContext.Provider value={boxes}>
        <EdgeHoverContext.Provider value={hover}>
          <PanningContext.Provider value={panning}>
            <div
              ref={rootRef}
              data-testid="flow"
              data-flow-root=""
              className={cn(
                // overflow-clip, not hidden: a focus on a node outside the frame must
                // not scroll the frame and move the toolbars under the header.
                'relative h-full w-full touch-none select-none overflow-clip',
                panning ? 'cursor-grabbing' : 'cursor-grab',
                className
              )}
              onPointerDown={onPointerDown}
              onPointerMove={onPointerMoveRoot}
              onPointerUp={cancelLongPress}
              onPointerCancel={cancelLongPress}
              onContextMenu={onContextMenuRoot}
              onClickCapture={onClickCapture}
              onClick={onClick}
            >
              <Layer panning={panning}>
                <svg
                  ref={edgesRef}
                  className="pointer-events-none absolute left-0 top-0 overflow-visible"
                  width={1}
                  height={1}
                  aria-hidden="true"
                >
                  <defs>
                    <Arrow id={`${arrowId}-arrow`} />
                  </defs>
                  {stacked.map(
                    ({ edge, Component, sourcePoint, targetPoint }) => (
                      <g
                        key={edge.id}
                        data-flow-edge={edge.id}
                        className="group/edge"
                        onClick={() => onEdgeClick?.(edge.id)}
                        onContextMenu={(event) => {
                          if (!onEdgeContextMenu) {
                            return;
                          }
                          event.preventDefault();
                          event.stopPropagation();
                          cancelLongPress();
                          onEdgeContextMenu(
                            edge.id,
                            menuPoint(event, event.target as Element)
                          );
                        }}
                      >
                        <Component
                          id={edge.id}
                          data={edge.data}
                          selected={!!edge.selected}
                          sourceX={sourcePoint.x}
                          sourceY={sourcePoint.y}
                          targetX={targetPoint.x}
                          targetY={targetPoint.y}
                          sourcePosition={sourcePoint.position}
                          targetPosition={targetPoint.position}
                          markerEnd={`url(#${arrowId}-arrow)`}
                        />
                      </g>
                    )
                  )}
                  {connectionPath && (
                    <path
                      d={connectionPath}
                      fill="none"
                      strokeWidth={1.5}
                      strokeDasharray="5 4"
                      style={{ stroke: 'var(--primary)' }}
                      data-testid="flow-connection"
                    />
                  )}
                </svg>
                {nodes.map((node) => {
                  const Component = nodeTypes[node.type];
                  if (!Component) {
                    return null;
                  }
                  return (
                    <FlowNodeBox
                      key={node.id}
                      node={node}
                      // The node box passes the props that the node type gives.
                      Component={Component as ComponentType<NodeProps>}
                      connectable={connectable}
                      onClick={onNodeClick}
                      onDoubleClick={onNodeDoubleClick}
                      onContextMenu={onNodeMenu}
                    />
                  );
                })}
                <div ref={setLabelRoot} className="absolute left-0 top-0" />
              </Layer>
              {children}
            </div>
          </PanningContext.Provider>
        </EdgeHoverContext.Provider>
      </NodeBoxesContext.Provider>
    </EdgeLabelRootContext.Provider>
  );
}

function layerTransform(viewport: { x: number; y: number; zoom: number }) {
  return `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.zoom})`;
}

/**
 * The layer that moves and zooms with the view. It follows the view by its
 * style, not by a draw: a pan or a zoom never draws the nodes again. While the
 * view pans, the layer has its own compositor layer, so that a frame moves it
 * without a paint.
 */
function Layer({
  panning,
  children,
}: {
  panning: boolean;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const viewports = useViewportStore();
  useViewportEffect((viewport) => {
    if (ref.current) {
      ref.current.style.transform = layerTransform(viewport);
    }
  });
  return (
    <div
      ref={ref}
      data-testid="flow-layer"
      className="absolute left-0 top-0 z-[1] origin-top-left"
      style={{
        transform: layerTransform(viewports.get()),
        willChange: panning ? 'transform' : undefined,
      }}
    >
      {children}
    </div>
  );
}

function oppositeOf(position: Position): Position {
  switch (position) {
    case Position.Left:
      return Position.Right;
    case Position.Right:
      return Position.Left;
    case Position.Top:
      return Position.Bottom;
    case Position.Bottom:
      return Position.Top;
  }
}

/**
 * Arrowhead of an edge. Its tip is on the end of the path, which is the
 * handle of the target, and it turns with the last segment. It takes the
 * stroke of the edge it ends, so that a status or a selection colours both.
 */
function Arrow({ id }: { id: string }) {
  return (
    <marker
      id={id}
      viewBox="0 0 10 10"
      refX={10}
      refY={5}
      markerWidth={9}
      markerHeight={9}
      markerUnits="userSpaceOnUse"
      orient="auto"
    >
      {/* The attribute is the fallback of a browser without context-stroke. */}
      <path
        d="M 0 0 L 10 5 L 0 10 z"
        fill="var(--muted-foreground)"
        style={{ fill: 'context-stroke' }}
      />
    </marker>
  );
}

// A node draws again only when its own props change: a hover on an edge or a
// new handle place does not draw every node.
const FlowNodeBox = memo(function FlowNodeBox({
  node,
  Component,
  connectable,
  onClick,
  onDoubleClick,
  onContextMenu,
}: {
  node: FlowNode;
  Component: ComponentType<NodeProps<FlowNode>>;
  connectable: boolean;
  onClick?: (id: string) => void;
  onDoubleClick?: (id: string) => void;
  onContextMenu?: (id: string, event: ReactMouseEvent<HTMLElement>) => void;
}) {
  const value = useMemo(() => ({ id: node.id }), [node.id]);
  return (
    <div
      data-flow-node={node.id}
      className={cn(
        // A host lights the node with data-spotlit; the card reads it.
        'group/node absolute left-0 top-0 hover:z-10 focus-within:z-10',
        node.selected && 'z-10'
      )}
      style={{
        width: node.width,
        height: node.height,
        transform: `translate(${node.position.x}px, ${node.position.y}px)`,
      }}
      onClick={() => onClick?.(node.id)}
      onDoubleClick={() => onDoubleClick?.(node.id)}
      onContextMenu={(event) => onContextMenu?.(node.id, event)}
    >
      <NodeContext.Provider value={value}>
        <Component
          id={node.id}
          data={node.data}
          selected={!!node.selected}
          isConnectable={connectable}
        />
      </NodeContext.Provider>
    </div>
  );
});
