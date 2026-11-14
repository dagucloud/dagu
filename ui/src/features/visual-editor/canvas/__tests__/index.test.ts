// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { describe, expect, expectTypeOf, it } from 'vitest';
import type {
  CanvasDirection,
  CanvasGraph,
  CanvasGraphInput,
  StepKey,
  VisualCanvasProps,
} from '../../contracts';
import { modelArraySteps, namesArraySteps } from '../../testing/fixtures';
import * as canvas from '..';

describe('canvas entry points', () => {
  it('keep the contract signatures', () => {
    expectTypeOf(canvas.buildCanvasGraph).toEqualTypeOf<
      (input: CanvasGraphInput) => CanvasGraph
    >();
    expectTypeOf(canvas.VisualCanvas).toEqualTypeOf<
      (props: VisualCanvasProps) => ReactElement
    >();
    expectTypeOf(canvas.layoutGraph).toEqualTypeOf<
      (
        graph: CanvasGraph,
        direction: CanvasDirection,
        columns?: number,
        compact?: boolean
      ) => Record<StepKey, { x: number; y: number }>
    >();
  });

  it('export the working graph projection', () => {
    const graph = canvas.buildCanvasGraph({
      model: modelArraySteps,
      names: namesArraySteps,
      issues: { byStep: {}, dag: [] },
    });
    expect(graph.nodes).toHaveLength(3);
    expect(Object.keys(canvas.layoutGraph(graph, 'LR'))).toHaveLength(3);
  });
});
