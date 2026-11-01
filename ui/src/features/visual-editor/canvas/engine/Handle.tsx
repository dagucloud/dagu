// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createContext,
  useContext,
  useLayoutEffect,
  useRef,
  type CSSProperties,
  type ReactElement,
} from 'react';
import { cn } from '@/lib/utils';
import { Position } from './geometry';
import { useFlowStore } from './store';
import type { HandleType } from './types';

/** The node that holds a handle; the flow sets it around each node. */
export const NodeContext = createContext<{ id: string } | null>(null);

// The handle is 8 px wide. A negative margin centers it on its side without a
// transform, so that its offsets give its center.
const SIDE_CLASS: Record<Position, string> = {
  [Position.Left]: 'left-0 top-1/2',
  [Position.Right]: 'left-full top-1/2',
  [Position.Top]: 'left-1/2 top-0',
  [Position.Bottom]: 'left-1/2 top-full',
};

export interface HandleProps {
  id?: string | null;
  type: HandleType;
  position: Position;
  isConnectable?: boolean;
  className?: string;
  style?: CSSProperties;
}

/**
 * Centre of the handle in the coordinates of its node box, in flow px. A
 * browser gives exact boxes: the rectangles are divided by the zoom, so that
 * the borders of the card and sub-pixel sizes shift nothing. Without layout
 * (no rectangles), the offsets of the ancestors give the place, with the
 * border of each ancestor, which an offset does not include.
 */
function centreInNode(element: HTMLElement): { x: number; y: number } {
  const node = element.closest<HTMLElement>('[data-flow-node]');
  if (node) {
    const box = node.getBoundingClientRect();
    if (box.width > 0 && node.offsetWidth > 0) {
      const scale = box.width / node.offsetWidth;
      const rect = element.getBoundingClientRect();
      // Rounded to 0.01 px, so that a change of zoom never looks like a move.
      const flat = (value: number) => Math.round(value * 100) / 100;
      return {
        x: flat((rect.left + rect.width / 2 - box.left) / scale),
        y: flat((rect.top + rect.height / 2 - box.top) / scale),
      };
    }
  }
  let x = element.offsetWidth / 2;
  let y = element.offsetHeight / 2;
  let current: HTMLElement | null = element;
  while (current && current.dataset.flowNode === undefined) {
    x += current.offsetLeft;
    y += current.offsetTop;
    current = current.offsetParent as HTMLElement | null;
    if (current) {
      x += current.clientLeft;
      y += current.clientTop;
    }
  }
  return { x, y };
}

/**
 * A point of a node where a link starts (`source`) or ends (`target`). It
 * tells the flow its place, so that edges and connections can use it.
 */
export function Handle({
  id = null,
  type,
  position,
  isConnectable = true,
  className,
  style,
}: HandleProps): ReactElement {
  const node = useContext(NodeContext);
  const { setHandle, removeHandle } = useFlowStore();
  const ref = useRef<HTMLDivElement>(null);
  const nodeId = node?.id ?? null;

  // The place can change with any render of the node, so it is measured on
  // each one; the store ignores an unchanged place.
  useLayoutEffect(() => {
    const element = ref.current;
    if (!element || nodeId === null) {
      return;
    }
    const { x, y } = centreInNode(element);
    setHandle(nodeId, id, {
      type,
      position,
      x,
      y,
      connectable: isConnectable,
    });
  });
  useLayoutEffect(() => {
    if (nodeId === null) {
      return;
    }
    return () => removeHandle(nodeId, id, type);
  }, [nodeId, id, type, removeHandle]);

  return (
    <div
      ref={ref}
      data-flow-handle=""
      data-node-id={nodeId ?? undefined}
      data-handle-id={id ?? undefined}
      data-handle-type={type}
      data-handle-position={position}
      data-connectable={isConnectable ? 'true' : 'false'}
      className={cn(
        'nodrag nopan absolute -ml-1 -mt-1 h-2 w-2',
        SIDE_CLASS[position],
        isConnectable ? 'cursor-crosshair' : 'pointer-events-none',
        // A larger area to hit than the dot.
        "before:absolute before:-inset-1.5 before:content-['']",
        className
      )}
      style={style}
    />
  );
}
