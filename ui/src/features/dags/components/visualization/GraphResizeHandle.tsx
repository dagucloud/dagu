// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { I18nProps } from '@/i18n/I18nProps';
import { writeLocalStorage } from '@/lib/local-storage-migration';
import { GripHorizontal } from 'lucide-react';
import React from 'react';
import { GRAPH_DEFAULT_HEIGHT, GRAPH_MIN_HEIGHT } from './graphViewport';

const GRAPH_HEIGHT_STORAGE_KEY = 'dagu.graph.height';
const KEYBOARD_STEP_PX = 40;

type GraphResizeHandleProps = {
  height: number | undefined;
  onPointerDown: React.PointerEventHandler<HTMLDivElement>;
  onPointerMove: React.PointerEventHandler<HTMLDivElement>;
  onPointerUp: React.PointerEventHandler<HTMLDivElement>;
  onPointerCancel: React.PointerEventHandler<HTMLDivElement>;
  onKeyDown: React.KeyboardEventHandler<HTMLDivElement>;
  onDoubleClick: React.MouseEventHandler<HTMLDivElement>;
};

type Drag = {
  pointerId: number;
  startY: number;
  startHeight: number;
  moved: boolean;
};

function maxGraphHeight(): number {
  return Math.max(GRAPH_MIN_HEIGHT, window.innerHeight);
}

function clampGraphHeight(value: number): number {
  return Math.round(
    Math.min(maxGraphHeight(), Math.max(GRAPH_MIN_HEIGHT, value))
  );
}

function readStoredGraphHeight(): number | undefined {
  try {
    const stored = window.localStorage.getItem(GRAPH_HEIGHT_STORAGE_KEY);
    const value = stored ? Number(stored) : NaN;
    return Number.isFinite(value) ? clampGraphHeight(value) : undefined;
  } catch {
    return undefined;
  }
}

function storeGraphHeight(value: number | undefined): void {
  if (value !== undefined) {
    writeLocalStorage(GRAPH_HEIGHT_STORAGE_KEY, String(value));
    return;
  }
  try {
    window.localStorage.removeItem(GRAPH_HEIGHT_STORAGE_KEY);
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
  }
}

function keyboardHeight(key: string, current: number): number | undefined {
  switch (key) {
    case 'ArrowUp':
      return clampGraphHeight(current - KEYBOARD_STEP_PX);
    case 'ArrowDown':
      return clampGraphHeight(current + KEYBOARD_STEP_PX);
    case 'Home':
      return GRAPH_MIN_HEIGHT;
    case 'End':
      return maxGraphHeight();
    default:
      return undefined;
  }
}

/**
 * Height for a user-resizable graph. `height` stays undefined, letting the
 * graph size itself, until the user resizes it; the chosen height then
 * applies to every graph and is remembered across visits. A double-click on
 * the handle returns to the automatic size.
 *
 * Attach `graphBoxRef` to the element that wraps the graph so a resize
 * starts from its current height.
 */
export function useResizableGraphHeight() {
  const [height, setHeight] = React.useState(readStoredGraphHeight);
  const graphBoxRef = React.useRef<HTMLDivElement>(null);
  const dragRef = React.useRef<Drag | null>(null);

  const currentHeight = () =>
    height ??
    graphBoxRef.current?.getBoundingClientRect().height ??
    GRAPH_DEFAULT_HEIGHT;

  const saveHeight = (value: number | undefined) => {
    setHeight(value);
    storeGraphHeight(value);
  };

  const endDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) {
      return;
    }
    dragRef.current = null;
    // A press without movement, such as half of a double-click, keeps the
    // automatic size.
    if (drag.moved) {
      saveHeight(
        clampGraphHeight(drag.startHeight + event.clientY - drag.startY)
      );
    }
  };

  const handleProps: GraphResizeHandleProps = {
    height,
    onPointerDown: (event) => {
      if (event.button !== 0) {
        return;
      }
      event.preventDefault();
      event.currentTarget.setPointerCapture(event.pointerId);
      dragRef.current = {
        pointerId: event.pointerId,
        startY: event.clientY,
        startHeight: currentHeight(),
        moved: false,
      };
    },
    onPointerMove: (event) => {
      const drag = dragRef.current;
      if (drag?.pointerId !== event.pointerId) {
        return;
      }
      drag.moved ||= event.clientY !== drag.startY;
      if (drag.moved) {
        setHeight(
          clampGraphHeight(drag.startHeight + event.clientY - drag.startY)
        );
      }
    },
    onPointerUp: endDrag,
    onPointerCancel: endDrag,
    onKeyDown: (event) => {
      const next = keyboardHeight(event.key, currentHeight());
      if (next !== undefined) {
        event.preventDefault();
        saveHeight(next);
      }
    },
    onDoubleClick: () => saveHeight(undefined),
  };

  return { height, graphBoxRef, handleProps };
}

/** Grip below a graph that resizes it by drag, arrow keys, Home or End. */
export function GraphResizeHandle({
  height,
  ...handlers
}: GraphResizeHandleProps): React.JSX.Element {
  return (
    <I18nProps>
      <div
        role="separator"
        aria-label="Resize graph"
        aria-orientation="horizontal"
        aria-valuemin={GRAPH_MIN_HEIGHT}
        aria-valuemax={maxGraphHeight()}
        aria-valuenow={height}
        tabIndex={0}
        title="Resize graph"
        className="flex w-full cursor-row-resize touch-none select-none items-center justify-center py-2 outline-none transition-colors hover:bg-muted/50 focus-visible:bg-muted/50"
        {...handlers}
      >
        <GripHorizontal className="h-4 w-4 text-muted-foreground/50" />
      </div>
    </I18nProps>
  );
}
