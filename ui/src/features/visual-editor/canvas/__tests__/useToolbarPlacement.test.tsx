// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  useToolbarPlacement,
  type ToolbarPlacement,
} from '../nodes/toolbarPlacement';

type Rect = { left: number; top: number; right: number; bottom: number };

// The boxes on screen, by the `data-box` name of each element. jsdom lays
// nothing out, so every other element has an empty box.
const boxes: Record<string, Rect> = {};
const EMPTY: Rect = { left: 0, top: 0, right: 0, bottom: 0 };

let placement: ToolbarPlacement;

function Node({ handles }: { handles: ('top' | 'bottom')[] }) {
  const toolbar = useToolbarPlacement(true);
  placement = toolbar.placement;
  return (
    <div data-flow-root="" data-box="canvas">
      <div data-flow-node="a" data-box="node">
        <div data-box="card">
          <div ref={toolbar.ref} data-box="bar" />
          {handles.map((side) => (
            <div
              key={side}
              data-flow-handle=""
              data-handle-position={side}
              data-box={side}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

beforeEach(() => {
  Object.assign(boxes, {
    canvas: { left: 0, top: 0, right: 800, bottom: 600 },
    // The node box is wider than its card.
    node: { left: 200, top: 200, right: 520, bottom: 260 },
    card: { left: 300, top: 200, right: 420, bottom: 260 },
    bar: { left: 308, top: 168, right: 420, bottom: 200 },
    // The handles: 8 px dots at the centre of the top and the bottom edge.
    top: { left: 356, top: 196, right: 364, bottom: 204 },
    bottom: { left: 356, top: 256, right: 364, bottom: 264 },
  });
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(
    function (this: Element) {
      const rect = boxes[this.getAttribute('data-box') ?? ''] ?? EMPTY;
      return {
        ...rect,
        x: rect.left,
        y: rect.top,
        width: rect.right - rect.left,
        height: rect.bottom - rect.top,
        toJSON: () => rect,
      };
    }
  );
});

afterEach(() => vi.restoreAllMocks());

describe('useToolbarPlacement', () => {
  // The bar is measured against the card: against the wider node box it
  // would end at 520 px, clear of the handles, and would not move.
  it('moves right of the handles at the top and the bottom of the card', () => {
    render(<Node handles={['top', 'bottom']} />);
    expect(placement.below).toBe(false);
    const left = boxes.card!.right + placement.shift - 112;
    expect(left).toBeGreaterThanOrEqual(boxes.top!.right);
    expect(left).toBeLessThan(boxes.card!.right - 24);
  });

  it('goes under the card when only the top has a handle', () => {
    render(<Node handles={['top']} />);
    expect(placement).toEqual({ below: true, shift: 0 });
  });

  it('stays in place without a handle at the top or the bottom', () => {
    render(<Node handles={[]} />);
    expect(placement).toEqual({ below: false, shift: 0 });
  });
});
