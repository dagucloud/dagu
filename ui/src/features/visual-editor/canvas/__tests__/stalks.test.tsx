// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render } from '@testing-library/react';
import type { ReactElement } from 'react';
import { describe, expect, it, vi } from 'vitest';
import type { CanvasDirection, CanvasNode } from '../../contracts';
import {
  CanvasAffordancesContext,
  type CanvasAffordances,
} from '../affordances';
import { CanvasActionsContext, type CanvasActions } from '../context';
import { FlowProvider, type NodeProps } from '../engine';
import { routePorts, routerSize } from '../layout';
import { RouterNode } from '../nodes/RouterNode';
import { StepNode } from '../nodes/StepNode';
import type { StepFlowNode, StepNodeData } from '../types';

function makeNode(over: Partial<CanvasNode> = {}): CanvasNode {
  return {
    key: 'steps[0]',
    label: 'fetch',
    kind: 'run',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    ...over,
  };
}

function nodeProps(
  node: CanvasNode,
  extra: Partial<StepNodeData> = {}
): NodeProps<StepFlowNode> {
  return {
    id: node.key,
    data: { node, ...extra },
    selected: false,
    isConnectable: true,
  };
}

function makeAffordances(): CanvasAffordances {
  return {
    openPalette: vi.fn(),
    deleteEdge: vi.fn(),
    restoreEdge: vi.fn(),
  };
}

function renderIn(
  ui: ReactElement,
  affordances: CanvasAffordances | null,
  direction: CanvasDirection = 'LR'
) {
  const actions: CanvasActions = {
    direction,
    editable: affordances !== null,
    onCommand: () => {},
    onDeleteStep: () => {},
    onInsertOnEdge: () => {},
  };
  return render(
    <FlowProvider>
      <CanvasActionsContext.Provider value={actions}>
        <CanvasAffordancesContext.Provider value={affordances}>
          {ui}
        </CanvasAffordancesContext.Provider>
      </CanvasActionsContext.Provider>
    </FlowProvider>
  );
}

const router = (routes: string[]) =>
  makeNode({ key: 'steps[1]', label: 'route', kind: 'router', routes });

describe('step stalk', () => {
  it('adds a child of a sink step', () => {
    const affordances = makeAffordances();
    const { getByRole } = renderIn(
      <StepNode {...nodeProps(makeNode(), { isSink: true })} />,
      affordances
    );
    const button = getByRole('button', { name: 'Add a step' });
    expect(button).toHaveClass('nodrag', 'nopan');
    fireEvent.click(button);
    expect(affordances.openPalette).toHaveBeenCalledWith({
      mode: 'child',
      source: 'steps[0]',
    });
  });

  it('goes beyond the source handle: right for LR, down for TB', () => {
    const lr = renderIn(
      <StepNode {...nodeProps(makeNode(), { isSink: true })} />,
      makeAffordances()
    );
    expect(lr.getByTestId('ve-canvas-stalk').style.left).toBe('100%');
    lr.unmount();
    const tb = renderIn(
      <StepNode {...nodeProps(makeNode(), { isSink: true })} />,
      makeAffordances(),
      'TB'
    );
    expect(tb.getByTestId('ve-canvas-stalk').style.top).toBe('100%');
  });

  // The line looks like a dependency edge: 1.5 px of the muted foreground,
  // and it reaches from the handle to the button.
  it('draws a line like the dependency edges, in both layouts', () => {
    const lr = renderIn(
      <StepNode {...nodeProps(makeNode(), { isSink: true })} />,
      makeAffordances()
    );
    const lrLine = lr.getByTestId('ve-canvas-stalk-line');
    expect(lrLine).toHaveClass('bg-muted-foreground', 'h-[1.5px]', 'w-7');
    expect(lrLine).not.toHaveClass('bg-border');
    expect(lr.getByTestId('ve-canvas-stalk')).not.toHaveClass('pl-2');
    lr.unmount();
    const tb = renderIn(
      <StepNode {...nodeProps(makeNode(), { isSink: true })} />,
      makeAffordances(),
      'TB'
    );
    const tbLine = tb.getByTestId('ve-canvas-stalk-line');
    expect(tbLine).toHaveClass('bg-muted-foreground', 'w-[1.5px]', 'h-7');
    expect(tb.getByTestId('ve-canvas-stalk')).not.toHaveClass('pt-2');
  });

  // The "+" of the end of the workflow and the "+" of a link are one
  // component: same size, same border, same hover.
  it('uses the round button of the links', () => {
    const { getByRole } = renderIn(
      <StepNode {...nodeProps(makeNode(), { isSink: true })} />,
      makeAffordances()
    );
    expect(getByRole('button', { name: 'Add a step' })).toHaveClass(
      'h-6',
      'w-6',
      'rounded-full',
      'border-[1.5px]',
      'border-muted-foreground',
      'bg-card',
      'hover:bg-primary'
    );
  });

  it('is absent on a step with children', () => {
    const { queryByTestId } = renderIn(
      <StepNode {...nodeProps(makeNode(), { isSink: false })} />,
      makeAffordances()
    );
    expect(queryByTestId('ve-canvas-stalk')).toBeNull();
  });

  it('is absent when the canvas cannot edit', () => {
    const { queryByTestId } = renderIn(
      <StepNode {...nodeProps(makeNode(), { isSink: true })} />,
      null
    );
    expect(queryByTestId('ve-canvas-stalk')).toBeNull();
  });
});

describe('router stalks', () => {
  it('adds a route target on each port without targets', () => {
    const affordances = makeAffordances();
    const { getAllByTestId, getByRole, queryByRole } = renderIn(
      <RouterNode
        {...nodeProps(router(['ok', 'fail']), {
          isSink: false,
          openPorts: ['fail'],
        })}
      />,
      affordances
    );
    expect(getAllByTestId('ve-canvas-stalk')).toHaveLength(1);
    expect(
      queryByRole('button', { name: 'Add a step to route ok' })
    ).toBeNull();
    fireEvent.click(getByRole('button', { name: 'Add a step to route fail' }));
    expect(affordances.openPalette).toHaveBeenCalledWith({
      mode: 'route',
      router: 'steps[1]',
      port: 'fail',
    });
  });

  // The stalk is 8 px of gap, a 20 px line and a 20 px button from the port.
  it('moves the pattern of an open port out of the stalk', () => {
    const ports = routePorts(2, routerSize(2), 'LR');
    const lr = renderIn(
      <RouterNode
        {...nodeProps(router(['ok', 'fail']), { openPorts: ['fail'] })}
      />,
      makeAffordances()
    );
    const [ok, fail] = lr.getAllByTestId('ve-canvas-route');
    expect(parseFloat(ok!.style.left)).toBe(ports[0]!.x + 8);
    expect(parseFloat(fail!.style.left)).toBeGreaterThan(ports[1]!.x + 48);
    lr.unmount();

    const tb = renderIn(
      <RouterNode
        {...nodeProps(router(['ok', 'fail']), { openPorts: ['fail'] })}
      />,
      makeAffordances(),
      'TB'
    );
    const [okTb, failTb] = tb.getAllByTestId('ve-canvas-route');
    expect(okTb).toHaveClass('-translate-x-1/2');
    expect(failTb).not.toHaveClass('-translate-x-1/2');
  });

  it('adds a child of a router with no route that ends its branch', () => {
    const affordances = makeAffordances();
    const { getByRole } = renderIn(
      <RouterNode {...nodeProps(router([]), { isSink: true })} />,
      affordances
    );
    fireEvent.click(getByRole('button', { name: 'Add a step' }));
    expect(affordances.openPalette).toHaveBeenCalledWith({
      mode: 'child',
      source: 'steps[1]',
    });
  });

  it('shows no stalk when the canvas cannot edit', () => {
    const { queryByTestId } = renderIn(
      <RouterNode
        {...nodeProps(router(['ok']), { isSink: true, openPorts: ['ok'] })}
      />,
      null
    );
    expect(queryByTestId('ve-canvas-stalk')).toBeNull();
  });
});
