// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactElement,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';

/** The element over the nodes where edge labels go; the flow sets it. */
export const EdgeLabelRootContext = createContext<HTMLElement | null>(null);

/** Box of a node in flow coordinates, for the edges that route around it. */
export interface NodeBox {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

const NO_BOXES: readonly NodeBox[] = [];

/** The boxes of the nodes; the flow sets them. */
export const NodeBoxesContext = createContext<readonly NodeBox[]>(NO_BOXES);

/** The boxes of all the nodes of the flow, in flow coordinates. */
export function useNodeBoxes(): readonly NodeBox[] {
  return useContext(NodeBoxesContext);
}

/** The edge under the pointer. A change of it draws no component. */
export interface EdgeHoverStore {
  hovered(): string | null;
  /** Does nothing when the edge is already the hovered one. */
  enter(id: string): void;
  /** Clears the hover only when the edge is still the hovered one. */
  leave(id: string): void;
  subscribe(listener: () => void): () => void;
}

export function createEdgeHoverStore(): EdgeHoverStore {
  let hovered: string | null = null;
  const listeners = new Set<() => void>();
  const set = (next: string | null) => {
    if (next === hovered) {
      return;
    }
    hovered = next;
    listeners.forEach((listener) => listener());
  };
  return {
    hovered: () => hovered,
    enter: (id) => set(id),
    leave: (id) => {
      if (hovered === id) {
        set(null);
      }
    },
    subscribe: (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
}

/** The hover store of the flow, so that the flow can draw the hovered edge last. */
export const EdgeHoverContext = createContext<EdgeHoverStore | null>(null);

/** Time in ms of a hover before the buttons and the tooltip of an edge mount. */
export const EDGE_HOVER_SETTLE_MS = 120;
/**
 * Time in ms that they stay after the pointer leaves the edge, so that the
 * pointer can move from the line to a button that has just mounted.
 */
export const EDGE_HOVER_RELEASE_MS = 150;

/**
 * Hover state of an edge, and its setter. The line follows the hover by a
 * `data-hovered` attribute that the flow sets on the group of the edge: no
 * component draws for it. The returned flag is for what is mounted only
 * while the edge is hovered, such as buttons and a tooltip. It turns on
 * after `EDGE_HOVER_SETTLE_MS` of hover, so that a pointer that crosses
 * edges mounts nothing, and turns off `EDGE_HOVER_RELEASE_MS` after the
 * pointer leaves. Without a flow the store is local.
 */
export function useEdgeHover(
  id: string
): [boolean, (hovered: boolean) => void] {
  const shared = useContext(EdgeHoverContext);
  const local = useRef<EdgeHoverStore | null>(null);
  const store = shared ?? (local.current ??= createEdgeHoverStore());
  const [settled, setSettled] = useState(false);
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    let current = false;
    const apply = (next: boolean) => {
      current = next;
      setSettled(next);
    };
    const sync = () => {
      clearTimeout(timer);
      timer = undefined;
      const on = store.hovered() === id;
      if (on === current) {
        return;
      }
      timer = setTimeout(
        () => apply(on),
        on ? EDGE_HOVER_SETTLE_MS : EDGE_HOVER_RELEASE_MS
      );
    };
    sync();
    const stop = store.subscribe(sync);
    return () => {
      stop();
      clearTimeout(timer);
    };
  }, [store, id]);
  const set = useCallback(
    (hovered: boolean) => {
      if (hovered) {
        store.enter(id);
      } else {
        store.leave(id);
      }
    },
    [store, id]
  );
  return [settled, set];
}

/**
 * Puts HTML, such as a label or a button, in the flow above the nodes. It
 * moves and zooms with the flow. Place it with an absolute `transform`.
 */
export function EdgeLabelRenderer({
  children,
}: {
  children: ReactNode;
}): ReactElement | null {
  const root = useContext(EdgeLabelRootContext);
  return root ? createPortal(children, root) : null;
}

/**
 * The line of an edge. A second, invisible and wider line takes the pointer,
 * so that a thin edge is easy to hit; its width is `interactionWidth`.
 */
export function BaseEdge({
  id,
  path,
  markerEnd,
  style,
  className,
  interactionWidth = 20,
}: {
  id?: string;
  path: string;
  markerEnd?: string;
  style?: CSSProperties;
  className?: string;
  interactionWidth?: number;
}): ReactElement {
  return (
    <>
      <path
        id={id}
        d={path}
        fill="none"
        markerEnd={markerEnd}
        data-flow-edge-path=""
        className={className}
        style={style}
      />
      {interactionWidth > 0 && (
        <path
          d={path}
          fill="none"
          stroke="transparent"
          strokeWidth={interactionWidth}
          data-flow-edge-interaction=""
          style={{ pointerEvents: 'stroke' }}
        />
      )}
    </>
  );
}
