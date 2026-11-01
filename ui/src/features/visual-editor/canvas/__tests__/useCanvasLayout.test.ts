// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { CanvasDirection, CanvasGraph } from '../../contracts';
import {
  detailsArraySteps,
  modelArraySteps,
  namesArraySteps,
} from '../../testing/fixtures';
import { buildCanvasGraph } from '../graph';
import { layoutGraph } from '../layout';
import { useCanvasLayout } from '../useCanvasLayout';

const graph = buildCanvasGraph({
  model: modelArraySteps,
  names: namesArraySteps,
  details: detailsArraySteps,
  issues: { byStep: {}, dag: [] },
});

function setup() {
  return renderHook(
    (props: { graph: CanvasGraph; direction: CanvasDirection }) =>
      useCanvasLayout(props.graph, props.direction),
    { initialProps: { graph, direction: 'LR' } }
  );
}

describe('useCanvasLayout', () => {
  it('starts from the auto layout', () => {
    const { result } = setup();
    expect(result.current.positions).toEqual(layoutGraph(graph, 'LR'));
  });

  it('keeps moves while the shape stays the same', () => {
    const { result, rerender } = setup();
    act(() => result.current.moveNodes({ 'steps[0]': { x: 500, y: 400 } }));
    expect(result.current.positions['steps[0]']).toEqual({ x: 500, y: 400 });

    const renamed = {
      ...graph,
      nodes: graph.nodes.map((node) => ({ ...node, label: `${node.label}!` })),
    };
    rerender({ graph: renamed, direction: 'LR' });
    expect(result.current.positions['steps[0]']).toEqual({ x: 500, y: 400 });
  });

  it('returns to the auto layout on realign, with the long edges', () => {
    const { result } = setup();
    const waypoints = result.current.waypoints;
    act(() => result.current.moveNodes({ 'steps[0]': { x: 500, y: 400 } }));
    expect(result.current.positions['steps[0]']).toEqual({ x: 500, y: 400 });
    act(() => result.current.realign());
    expect(result.current.positions).toEqual(layoutGraph(graph, 'LR'));
    expect(result.current.waypoints).toEqual(waypoints);
  });

  it('drops moves when the shape changes', () => {
    const { result, rerender } = setup();
    act(() => result.current.moveNodes({ 'steps[0]': { x: 500, y: 400 } }));
    rerender({ graph, direction: 'TB' });
    expect(result.current.positions).toEqual(layoutGraph(graph, 'TB'));
  });
});
