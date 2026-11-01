// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render } from '@testing-library/react';
import type { ReactElement } from 'react';
import { NodeStatus } from '@/api/v1/schema';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CanvasEdge } from '../../contracts';
import {
  CanvasAffordancesContext,
  type CanvasAffordances,
} from '../affordances';
import { CanvasActionsContext, type CanvasActions } from '../context';
import {
  EDGE_HOVER_RELEASE_MS,
  EDGE_HOVER_SETTLE_MS,
} from '../engine/edgeParts';
import {
  Flow,
  FlowProvider,
  Position,
  type EdgeProps,
  type FlowNode,
} from '../engine';
import { DependencyEdge } from '../edges/DependencyEdge';
import { edgeTypes } from '../edges';
import { LockedEdge } from '../edges/LockedEdge';
import type { CanvasEdgeData, CanvasFlowEdge } from '../types';

function makeActions(over: Partial<CanvasActions> = {}): CanvasActions {
  return {
    direction: 'LR',
    editable: true,
    onCommand: () => {},
    onDeleteStep: () => {},
    onInsertOnEdge: () => {},
    ...over,
  };
}

function edgeProps(
  edge: Partial<CanvasEdge>,
  extra: Partial<CanvasEdgeData> = {},
  over: Partial<EdgeProps<CanvasFlowEdge>> = {}
): EdgeProps<CanvasFlowEdge> {
  const full: CanvasEdge = {
    id: 'e1',
    source: 'steps[0]',
    target: 'steps[1]',
    kind: 'depends',
    ...edge,
  };
  return {
    id: full.id,
    data: { edge: full, lockReason: null, insertable: false, ...extra },
    selected: false,
    sourceX: 0,
    sourceY: 0,
    targetX: 200,
    targetY: 100,
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    ...over,
  };
}

function makeAffordances(): CanvasAffordances {
  return {
    openPalette: vi.fn(),
    deleteEdge: vi.fn(),
    restoreEdge: vi.fn(),
  };
}

/** The edge needs a mounted flow: the label renderer finds its element there. */
function renderEdge(
  ui: ReactElement,
  actions: CanvasActions = makeActions(),
  affordances: CanvasAffordances | null = null
) {
  return render(
    <FlowProvider>
      <CanvasActionsContext.Provider value={actions}>
        <CanvasAffordancesContext.Provider value={affordances}>
          <Flow nodes={[]} edges={[]} nodeTypes={{}} edgeTypes={{}}>
            <svg>{ui}</svg>
          </Flow>
        </CanvasAffordancesContext.Provider>
      </CanvasActionsContext.Provider>
    </FlowProvider>
  );
}

// The buttons and the tooltip of an edge mount after a short hover.
beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

const interactionOf = (view: { container: HTMLElement }) =>
  view.container.querySelector('path[data-flow-edge-interaction]')!;

/** The pointer enters the line of the edge and stays long enough. */
function hoverEdge(view: { container: HTMLElement }) {
  fireEvent.mouseEnter(interactionOf(view));
  act(() => vi.advanceTimersByTime(EDGE_HOVER_SETTLE_MS));
}

function edgePath(container: HTMLElement): SVGPathElement {
  return container.querySelector<SVGPathElement>('path[data-flow-edge-path]')!;
}

describe('DependencyEdge', () => {
  it('draws a path', () => {
    const { container } = renderEdge(<DependencyEdge {...edgeProps({})} />);
    const path = container.querySelector('svg path[data-flow-edge-path]');
    expect(path?.getAttribute('d')).toMatch(/^M/);
  });

  // The owner asked for smooth curves between the steps, as n8n draws them:
  // a straight line only when the steps are aligned.
  it('draws one straight line between aligned steps, ending on the dot', () => {
    const { container } = renderEdge(
      <DependencyEdge
        {...edgeProps(
          {},
          {},
          {
            sourceX: 120,
            sourceY: 64,
            targetX: 120,
            targetY: 160,
            sourcePosition: Position.Bottom,
            targetPosition: Position.Top,
          }
        )}
      />
    );
    // The tip stops on the outer edge of the entry dot (4 px), not at its centre.
    expect(edgePath(container).getAttribute('d')).toBe('M120,64 L120,156');
  });

  it('draws offset steps with a smooth curve, vertical tangents in TB', () => {
    const { container } = renderEdge(
      <DependencyEdge
        {...edgeProps(
          {},
          {},
          {
            sourceX: 0,
            sourceY: 0,
            targetX: 300,
            targetY: 100,
            sourcePosition: Position.Bottom,
            targetPosition: Position.Top,
          }
        )}
      />
    );
    const d = edgePath(container).getAttribute('d')!;
    expect(d).toMatch(/^M0,0 C0,\d+(\.\d+)? /);
    expect(d).not.toMatch(/[AL]/);
    expect(d.endsWith(' 300,96')).toBe(true);
  });

  it('draws offset steps with a smooth curve, horizontal tangents in LR', () => {
    const { container } = renderEdge(
      <DependencyEdge
        {...edgeProps(
          {},
          {},
          { sourceX: 0, sourceY: 0, targetX: 300, targetY: 100 }
        )}
      />
    );
    const d = edgePath(container).getAttribute('d')!;
    expect(d).toMatch(/^M0,0 C\d+(\.\d+)?,0 /);
    expect(d).not.toMatch(/[AL]/);
    expect(d.endsWith(' 296,100')).toBe(true);
  });

  // One shared component: the "+" on a link looks like the "+" at the end of
  // the workflow, and the trash has the same size and border.
  it('gives the insert and delete buttons the border of the end "+"', () => {
    const view = renderEdge(
      <DependencyEdge {...edgeProps({}, { insertable: true })} />,
      makeActions(),
      makeAffordances()
    );
    hoverEdge(view);
    const { getByRole } = view;
    for (const name of ['Insert a step here', 'Remove dependency']) {
      expect(getByRole('button', { name })).toHaveClass(
        'h-6',
        'w-6',
        'rounded-full',
        'border-[1.5px]',
        'border-muted-foreground',
        'bg-card'
      );
    }
  });

  // The owner asked for a third button, with the icon of router.route turned
  // to the right: it adds a step beside the target of the link.
  it('adds a parallel step button, the router icon turned right', () => {
    const affordances = makeAffordances();
    const view = renderEdge(
      <DependencyEdge {...edgeProps({ id: 'e5' }, { insertable: true })} />,
      makeActions(),
      affordances
    );
    hoverEdge(view);
    const { getByRole } = view;
    // Order on the link: insert, parallel, remove.
    const labels = Array.from(
      getByRole('button', { name: 'Add a parallel step' }).parentElement!
        .children
    ).map((button) => button.getAttribute('aria-label'));
    expect(labels).toEqual([
      'Insert a step here',
      'Add a parallel step',
      'Remove dependency',
    ]);
    const parallel = getByRole('button', { name: 'Add a parallel step' });
    expect(parallel).toHaveAttribute('title', 'Add a parallel step');
    expect(parallel).toHaveClass('h-6', 'w-6', 'rounded-full', 'nodrag');
    // The stem of the router icon is down: a quarter turn points it right.
    expect(parallel.querySelector('svg')).toHaveClass('rotate-90');
    expect(
      getByRole('button', { name: 'Insert a step here' }).querySelector('svg')
    ).not.toHaveClass('rotate-90');
    fireEvent.click(parallel);
    expect(affordances.openPalette).toHaveBeenCalledWith({
      mode: 'parallel',
      edgeId: 'e5',
    });
  });

  it('has no parallel button on a route edge or without affordances', () => {
    const route = renderEdge(
      <DependencyEdge {...edgeProps({ kind: 'route', label: 'ok' })} />,
      makeActions(),
      makeAffordances()
    );
    hoverEdge(route);
    expect(
      route.queryByRole('button', { name: 'Add a parallel step' })
    ).toBeNull();
    route.unmount();
    const bare = renderEdge(
      <DependencyEdge {...edgeProps({}, { insertable: true })} />
    );
    hoverEdge(bare);
    expect(
      bare.queryByRole('button', { name: 'Add a parallel step' })
    ).toBeNull();
  });

  // Two routes of one router leave side by side: their labels go to the outer
  // sides of the curves, and never on each other.
  it('puts the pattern on the outer side of the curve in TB', () => {
    const place = (targetX: number) => {
      const { getByTestId, unmount } = renderEdge(
        <DependencyEdge
          {...edgeProps(
            { kind: 'route', label: 'ok' },
            {},
            {
              sourceX: 100,
              sourceY: 0,
              targetX,
              targetY: 96,
              sourcePosition: Position.Bottom,
              targetPosition: Position.Top,
            }
          )}
        />,
        makeActions({ direction: 'TB' })
      );
      const transform = getByTestId('ve-canvas-edge-label').style.transform;
      unmount();
      return transform;
    };
    expect(place(20)).toMatch(/^translate\(-100%, -50%\)/);
    expect(place(180)).toMatch(/^translate\(0(px)?, -50%\)/);
  });

  it('shows the pattern of a route edge', () => {
    const { getByTestId } = renderEdge(
      <DependencyEdge {...edgeProps({ kind: 'route', label: 'fail*' })} />
    );
    expect(getByTestId('ve-canvas-edge-label')).toHaveTextContent('fail*');
  });

  // In a left-to-right layout a long pattern at the middle of a short edge
  // sat on the diamond of the router: the label stays between the nodes.
  it('keeps the pattern label clear of the source node in LR', () => {
    const pattern = 're:^[^2]\\d\\d$';
    const place = (targetX: number) => {
      const { getByTestId, unmount } = renderEdge(
        <DependencyEdge
          {...edgeProps(
            { kind: 'route', label: pattern },
            {},
            { sourceX: 100, targetX }
          )}
        />
      );
      const match = /translate\(-50%, -100%\) translate\((-?[\d.]+)px/.exec(
        getByTestId('ve-canvas-edge-label').style.transform
      );
      unmount();
      return Number(match?.[1]);
    };
    const half = pattern.length * 3 + 6;
    // The label starts to the right of the source, with a gap.
    expect(place(260) - half).toBeGreaterThanOrEqual(100);
    // And ends to the left of the target.
    expect(place(260) + half).toBeLessThanOrEqual(260);
  });

  it('shows no label on a depends edge', () => {
    const { queryByTestId } = renderEdge(
      <DependencyEdge {...edgeProps({ label: 'ignored' })} />
    );
    expect(queryByTestId('ve-canvas-edge-label')).toBeNull();
  });

  it('draws a selected edge thicker in the primary colour', () => {
    const plain = renderEdge(<DependencyEdge {...edgeProps({})} />);
    const before = plain.container.querySelector<SVGPathElement>(
      'path[data-flow-edge-path]'
    )!;
    expect(before.style.stroke).not.toBe('var(--primary)');
    const width = Number(before.style.strokeWidth);
    plain.unmount();

    const { container } = renderEdge(
      <DependencyEdge {...edgeProps({}, {}, { selected: true })} />
    );
    const after = container.querySelector<SVGPathElement>(
      'path[data-flow-edge-path]'
    )!;
    expect(after.style.stroke).toBe('var(--primary)');
    expect(Number(after.style.strokeWidth)).toBeGreaterThan(width);
  });

  it('has no insert button when the edge is not insertable', () => {
    const view = renderEdge(<DependencyEdge {...edgeProps({})} />);
    hoverEdge(view);
    expect(
      view.queryByRole('button', { name: 'Insert a step here' })
    ).toBeNull();
  });

  // parity:canvas-edge-insert
  it('calls onInsertOnEdge with the edge id', () => {
    const onInsertOnEdge = vi.fn();
    const view = renderEdge(
      <DependencyEdge {...edgeProps({ id: 'e7' }, { insertable: true })} />,
      makeActions({ onInsertOnEdge })
    );
    hoverEdge(view);
    const { getByRole } = view;
    const button = getByRole('button', { name: 'Insert a step here' });
    expect(button).toHaveClass('nodrag', 'nopan');
    fireEvent.click(button);
    expect(onInsertOnEdge).toHaveBeenCalledWith('e7');
  });

  // A pointer that crosses the edge mounts nothing; a pointer that stays does.
  it('mounts the insert button after a short hover or on selection only', () => {
    const hovered = renderEdge(
      <DependencyEdge {...edgeProps({}, { insertable: true })} />
    );
    const button = () =>
      hovered.queryByRole('button', { name: 'Insert a step here' });
    expect(button()).toBeNull();
    fireEvent.mouseEnter(interactionOf(hovered));
    act(() => vi.advanceTimersByTime(EDGE_HOVER_SETTLE_MS - 1));
    expect(button()).toBeNull();
    act(() => vi.advanceTimersByTime(1));
    expect(button()).not.toBeNull();
    // The pointer needs a moment to reach the button: it stays that long.
    fireEvent.mouseLeave(interactionOf(hovered));
    act(() => vi.advanceTimersByTime(EDGE_HOVER_RELEASE_MS - 1));
    expect(button()).not.toBeNull();
    act(() => vi.advanceTimersByTime(1));
    expect(button()).toBeNull();
    hovered.unmount();

    const crossed = renderEdge(
      <DependencyEdge {...edgeProps({}, { insertable: true })} />
    );
    fireEvent.mouseEnter(interactionOf(crossed));
    act(() => vi.advanceTimersByTime(EDGE_HOVER_SETTLE_MS / 2));
    fireEvent.mouseLeave(interactionOf(crossed));
    act(() => vi.advanceTimersByTime(EDGE_HOVER_SETTLE_MS * 3));
    expect(
      crossed.queryByRole('button', { name: 'Insert a step here' })
    ).toBeNull();
    crossed.unmount();

    const selected = renderEdge(
      <DependencyEdge
        {...edgeProps({}, { insertable: true }, { selected: true })}
      />
    );
    expect(
      selected.getByRole('button', { name: 'Insert a step here' })
    ).toBeInTheDocument();
  });

  // The buttons keep the hover: the pointer on a button is on the edge.
  it('keeps the buttons while the pointer is on them', () => {
    const view = renderEdge(
      <DependencyEdge {...edgeProps({}, { insertable: true })} />
    );
    hoverEdge(view);
    fireEvent.mouseLeave(interactionOf(view));
    fireEvent.mouseEnter(view.getByTestId('ve-canvas-edge-buttons'));
    act(() => vi.advanceTimersByTime(EDGE_HOVER_RELEASE_MS * 3));
    expect(
      view.getByRole('button', { name: 'Insert a step here' })
    ).toBeInTheDocument();
  });

  // axe target-size: a 24 px target on screen, whatever the fit zoom is.
  it('gives the edge buttons a 24 px target', () => {
    const view = renderEdge(
      <DependencyEdge {...edgeProps({}, { insertable: true })} />
    );
    hoverEdge(view);
    const button = view.getByRole('button', { name: 'Insert a step here' });
    expect(button).toHaveClass('h-6', 'w-6');
    expect(button.parentElement?.style.transform).toContain('scale(');
  });

  it('hides the insert button when the canvas is not editable', () => {
    const view = renderEdge(
      <DependencyEdge {...edgeProps({}, { insertable: true })} />,
      makeActions({ editable: false })
    );
    hoverEdge(view);
    expect(
      view.queryByRole('button', { name: 'Insert a step here' })
    ).toBeNull();
  });

  it('removes the link with the trash button shown on hover', () => {
    const affordances = makeAffordances();
    const view = renderEdge(
      <DependencyEdge {...edgeProps({ id: 'e9' }, { insertable: true })} />,
      makeActions(),
      affordances
    );
    expect(
      view.queryByRole('button', { name: 'Remove dependency' })
    ).toBeNull();
    hoverEdge(view);
    const trash = view.getByRole('button', { name: 'Remove dependency' });
    expect(trash).toHaveClass('nodrag', 'nopan');
    expect(
      view.getByRole('button', { name: 'Insert a step here' })
    ).toBeInTheDocument();
    fireEvent.click(trash);
    expect(affordances.deleteEdge).toHaveBeenCalledWith('e9');
  });

  it('shows the trash on a route edge, which has no insert button', () => {
    const { getByRole, queryByRole } = renderEdge(
      <DependencyEdge
        {...edgeProps({ kind: 'route', label: 'ok' }, {}, { selected: true })}
      />,
      makeActions(),
      makeAffordances()
    );
    expect(
      getByRole('button', { name: 'Remove dependency' })
    ).toBeInTheDocument();
    expect(queryByRole('button', { name: 'Insert a step here' })).toBeNull();
  });

  it('has no trash on a locked edge or without affordances', () => {
    const locked = renderEdge(
      <DependencyEdge
        {...edgeProps({}, { lockReason: 'Edges cannot change.' })}
      />,
      makeActions(),
      makeAffordances()
    );
    hoverEdge(locked);
    expect(
      locked.queryByRole('button', { name: 'Remove dependency' })
    ).toBeNull();
    locked.unmount();
    // A read-only or frozen canvas gives no affordances.
    const readOnly = renderEdge(
      <DependencyEdge {...edgeProps({}, { insertable: true })} />,
      makeActions({ editable: false })
    );
    hoverEdge(readOnly);
    expect(readOnly.queryAllByRole('button')).toHaveLength(0);
  });

  // parity:run-graph-render
  it.each([
    [NodeStatus.Success, 'stroke-success'],
    [NodeStatus.Running, 'stroke-[var(--status-running)]'],
    [NodeStatus.Retrying, 'stroke-warning'],
    [NodeStatus.Waiting, 'stroke-warning'],
    [NodeStatus.PartialSuccess, 'stroke-warning'],
    [NodeStatus.Failed, 'stroke-destructive'],
    [NodeStatus.Rejected, 'stroke-destructive'],
    [NodeStatus.Aborted, 'stroke-[var(--status-aborted)]'],
    [NodeStatus.NotStarted, 'stroke-[var(--status-neutral)]'],
    [NodeStatus.Skipped, 'stroke-[var(--status-neutral)]'],
  ])('strokes with the source status %s as %s', (status, token) => {
    const { container } = renderEdge(
      <DependencyEdge {...edgeProps({}, { sourceStatus: status })} />
    );
    const path = edgePath(container);
    expect(path).toHaveClass(token);
    // The inline colour would hide the class.
    expect(path.style.stroke).toBe('');
  });

  // The edge into a skipped step is not a path that ran: it stays neutral.
  it.each([NodeStatus.Skipped, NodeStatus.NotStarted])(
    'draws an edge into a target with status %s in the neutral colour',
    (targetStatus) => {
      const { container } = renderEdge(
        <DependencyEdge
          {...edgeProps({}, { sourceStatus: NodeStatus.Success, targetStatus })}
        />
      );
      const path = edgePath(container);
      expect(path).toHaveClass('stroke-[var(--status-neutral)]');
      expect(path).not.toHaveClass('stroke-success');
    }
  );

  it('keeps the source colour into a target that ran', () => {
    const { container } = renderEdge(
      <DependencyEdge
        {...edgeProps(
          {},
          { sourceStatus: NodeStatus.Success, targetStatus: NodeStatus.Failed }
        )}
      />
    );
    expect(edgePath(container)).toHaveClass('stroke-success');
  });

  it('keeps the selection colour over the status', () => {
    const { container } = renderEdge(
      <DependencyEdge
        {...edgeProps(
          {},
          { sourceStatus: NodeStatus.Failed },
          { selected: true }
        )}
      />
    );
    const path = edgePath(container);
    expect(path).not.toHaveClass('stroke-destructive');
    expect(path.style.stroke).toBe('var(--primary)');
  });

  it('keeps the muted colour without a run', () => {
    const { container } = renderEdge(<DependencyEdge {...edgeProps({})} />);
    const path = edgePath(container);
    // The hover colour comes from the group of the edge, by CSS.
    expect(path).toHaveClass(
      'group-data-[hovered]/edge:stroke-primary!',
      'group-data-[hovered]/edge:[stroke-width:2.5]!'
    );
    expect(path.style.stroke).toBe('var(--muted-foreground)');
  });
});

describe('LockedEdge', () => {
  const REASON =
    'Inferred from a step reference. Remove the reference to remove this edge.';

  it('shows the lock reason in a tooltip on hover', () => {
    const { container, getByRole, queryByRole } = renderEdge(
      <LockedEdge
        {...edgeProps({ kind: 'inferred' }, { lockReason: REASON })}
      />
    );
    expect(queryByRole('tooltip')).toBeNull();
    const line = container.querySelector('path[data-flow-edge-interaction]')!;
    fireEvent.mouseEnter(line);
    act(() => vi.advanceTimersByTime(EDGE_HOVER_SETTLE_MS));
    expect(getByRole('tooltip')).toHaveTextContent(REASON);
    fireEvent.mouseLeave(line);
    act(() => vi.advanceTimersByTime(EDGE_HOVER_RELEASE_MS));
    expect(queryByRole('tooltip')).toBeNull();
  });

  it('shows the lock reason on a read-only canvas too', () => {
    const view = renderEdge(
      <LockedEdge {...edgeProps({ kind: 'order' }, { lockReason: REASON })} />,
      makeActions({ editable: false })
    );
    hoverEdge(view);
    expect(view.getByRole('tooltip')).toHaveTextContent(REASON);
  });

  it('has no tooltip without a reason', () => {
    const view = renderEdge(
      <LockedEdge {...edgeProps({ kind: 'inferred' })} />
    );
    hoverEdge(view);
    expect(view.queryByRole('tooltip')).toBeNull();
  });

  it('draws inferred dashed and order dotted', () => {
    const inferred = renderEdge(
      <LockedEdge {...edgeProps({ kind: 'inferred' })} />
    );
    const dash = (c: HTMLElement) =>
      c.querySelector<SVGPathElement>('path[data-flow-edge-path]')!.style
        .strokeDasharray;
    const inferredDash = dash(inferred.container);
    inferred.unmount();
    const order = renderEdge(<LockedEdge {...edgeProps({ kind: 'order' })} />);
    expect(inferredDash).toBe('6 4');
    expect(dash(order.container)).toBe('2 4');
  });

  it('draws a smooth curve, like the other links', () => {
    const { container } = renderEdge(
      <LockedEdge {...edgeProps({ kind: 'inferred' })} />
    );
    const d = edgePath(container).getAttribute('d')!;
    expect(d).toMatch(/C/);
    expect(d).not.toMatch(/[AL]/);
    expect(d.endsWith(' 196,100')).toBe(true);
  });

  // The dashes stay dashes: a selected or a hovered link is not solid.
  it('keeps the dashes of an inferred link when it is hovered or selected', () => {
    const dash = (c: HTMLElement) =>
      c.querySelector<SVGPathElement>('path[data-flow-edge-path]')!.style
        .strokeDasharray;
    // The hover colour comes from the group of the edge, by CSS: the dashes
    // are in the style, which the hover does not change.
    const hovered = renderEdge(
      <LockedEdge {...edgeProps({ kind: 'inferred' })} />
    );
    expect(dash(hovered.container)).toBe('6 4');
    expect(edgePath(hovered.container)).toHaveClass(
      'group-data-[hovered]/edge:stroke-primary!'
    );
    hovered.unmount();
    const selected = renderEdge(
      <LockedEdge
        {...edgeProps({ kind: 'inferred' }, {}, { selected: true })}
      />
    );
    expect(dash(selected.container)).toBe('6 4');
    selected.unmount();
    const order = renderEdge(
      <LockedEdge {...edgeProps({ kind: 'order' }, {}, { selected: true })} />
    );
    expect(dash(order.container)).toBe('2 4');
  });

  describe('buttons of an inferred link', () => {
    const inferred = (extra: Partial<CanvasEdgeData> = {}) =>
      edgeProps(
        { id: 'inferred:a->b', kind: 'inferred' },
        {
          lockReason: REASON,
          restorable: true,
          insertable: true,
          ...extra,
        }
      );

    const hover = hoverEdge;

    // The restore control is a round button like the "+" of a link, on the
    // line with the others, and not a box with its own button.
    it('has the insert, parallel and restore buttons, and no trash', () => {
      const affordances = makeAffordances();
      const view = renderEdge(
        <LockedEdge {...inferred()} />,
        makeActions(),
        affordances
      );
      hover(view);
      const labels = Array.from(
        view.getByTestId('ve-canvas-edge-buttons').children
      ).map((button) => button.getAttribute('aria-label'));
      expect(labels).toEqual([
        'Insert a step here',
        'Add a parallel step',
        'Restore the link',
      ]);
      expect(
        view.queryByRole('button', { name: 'Remove dependency' })
      ).toBeNull();
      const plus = view.getByRole('button', { name: 'Insert a step here' });
      const restore = view.getByRole('button', { name: 'Restore the link' });
      // The same size, border and hover as the "+".
      for (const name of [
        'h-6',
        'w-6',
        'rounded-full',
        'border-[1.5px]',
        'border-muted-foreground',
        'bg-card',
        'hover:border-primary',
        'hover:bg-primary',
      ]) {
        expect(plus).toHaveClass(name);
        expect(restore).toHaveClass(name);
      }
      fireEvent.click(restore);
      expect(affordances.restoreEdge).toHaveBeenCalledWith('inferred:a->b');
    });

    it('gives the restore button the explanation as its title', () => {
      const view = renderEdge(
        <LockedEdge {...inferred()} />,
        makeActions(),
        makeAffordances()
      );
      hover(view);
      const restore = view.getByRole('button', { name: 'Restore the link' });
      expect(restore.getAttribute('title')).toContain('Restore the link');
      expect(restore.getAttribute('title')).toContain(REASON);
    });

    it('shows the message as a tooltip only, with no button in it', () => {
      const view = renderEdge(
        <LockedEdge {...inferred()} />,
        makeActions(),
        makeAffordances()
      );
      hover(view);
      const tip = view.getByTestId('ve-canvas-edge-lock-reason');
      expect(tip).toHaveAttribute('role', 'tooltip');
      expect(tip).toHaveTextContent(REASON);
      expect(tip).toHaveClass('pointer-events-none');
      expect(tip.querySelector('button')).toBeNull();
    });

    it('adds a step in parallel and inserts a step on the link', () => {
      const onInsertOnEdge = vi.fn();
      const affordances = makeAffordances();
      const view = renderEdge(
        <LockedEdge {...inferred()} />,
        makeActions({ onInsertOnEdge }),
        affordances
      );
      hover(view);
      fireEvent.click(
        view.getByRole('button', { name: 'Add a parallel step' })
      );
      expect(affordances.openPalette).toHaveBeenCalledWith({
        mode: 'parallel',
        edgeId: 'inferred:a->b',
      });
      fireEvent.click(view.getByRole('button', { name: 'Insert a step here' }));
      expect(onInsertOnEdge).toHaveBeenCalledWith('inferred:a->b');
    });

    it('has only the restore button when the link cannot take a step', () => {
      const view = renderEdge(
        <LockedEdge {...inferred({ insertable: false })} />,
        makeActions(),
        makeAffordances()
      );
      hover(view);
      expect(
        view.getAllByRole('button').map((b) => b.getAttribute('aria-label'))
      ).toEqual(['Restore the link']);
    });

    it('has no button when the link cannot be restored', () => {
      const view = renderEdge(
        <LockedEdge {...inferred({ restorable: false, insertable: false })} />,
        makeActions(),
        makeAffordances()
      );
      hover(view);
      expect(view.queryByRole('button')).toBeNull();
      expect(view.getByRole('tooltip')).toHaveTextContent(REASON);
    });

    it('has no button without affordances or on a read-only canvas', () => {
      const bare = renderEdge(<LockedEdge {...inferred()} />);
      hover(bare);
      expect(
        bare.queryByRole('button', { name: 'Restore the link' })
      ).toBeNull();
      bare.unmount();
      const readOnly = renderEdge(
        <LockedEdge {...inferred()} />,
        makeActions({ editable: false }),
        null
      );
      hover(readOnly);
      expect(readOnly.queryAllByRole('button')).toHaveLength(0);
    });
  });

  it('has no insert button', () => {
    const view = renderEdge(
      <LockedEdge {...edgeProps({ kind: 'order' }, { insertable: true })} />
    );
    hoverEdge(view);
    expect(view.queryByRole('button')).toBeNull();
  });
});

describe('edgeTypes', () => {
  it('registers one component for each edge kind', () => {
    expect(edgeTypes.depends).toBe(DependencyEdge);
    expect(edgeTypes.route).toBe(DependencyEdge);
    expect(edgeTypes.inferred).toBe(LockedEdge);
    expect(edgeTypes.order).toBe(LockedEdge);
    expect(edgeTypes.agent).toBe(LockedEdge);
    expect(Object.keys(edgeTypes).sort()).toEqual([
      'agent',
      'depends',
      'inferred',
      'order',
      'route',
    ]);
  });
});

// Two edges that leave one step share the start of their path. The one that
// is selected or hovered is drawn over the other, in the selection colour,
// so that the shared part never takes its colour from the drawing order.
describe('overlapping edges', () => {
  const node = (id: string, x: number, y: number): FlowNode => ({
    id,
    type: 'box',
    position: { x, y },
    width: 100,
    height: 40,
    data: {},
  });
  const nodes = [node('a', 0, 0), node('b', 200, 0), node('c', 200, 100)];
  const dep = (
    id: string,
    target: string,
    selected = false
  ): CanvasFlowEdge => ({
    id,
    source: 'a',
    target,
    type: 'depends',
    data: {
      edge: { id, source: 'a', target, kind: 'depends' },
      lockReason: null,
      insertable: false,
    },
    selected,
  });

  function renderFlow(edges: CanvasFlowEdge[]) {
    return render(
      <FlowProvider>
        <CanvasActionsContext.Provider value={makeActions()}>
          <CanvasAffordancesContext.Provider value={null}>
            <Flow
              nodes={nodes}
              edges={edges}
              nodeTypes={{ box: () => <div /> }}
              edgeTypes={edgeTypes}
            />
          </CanvasAffordancesContext.Provider>
        </CanvasActionsContext.Provider>
      </FlowProvider>
    );
  }

  const order = (container: HTMLElement) =>
    Array.from(container.querySelectorAll('[data-flow-edge]')).map((g) =>
      g.getAttribute('data-flow-edge')
    );
  const strokeOf = (container: HTMLElement, id: string) =>
    container.querySelector<SVGPathElement>(
      `[data-flow-edge="${id}"] path[data-flow-edge-path]`
    )!.style.stroke;

  it('draws the selected edge last, whichever is first in the list', () => {
    const first = renderFlow([dep('e1', 'b', true), dep('e2', 'c')]);
    expect(order(first.container)).toEqual(['e2', 'e1']);
    first.unmount();
    const second = renderFlow([dep('e1', 'b'), dep('e2', 'c', true)]);
    expect(order(second.container)).toEqual(['e1', 'e2']);
  });

  it('draws the hovered edge last and puts it back when the pointer leaves', () => {
    const { container } = renderFlow([dep('e1', 'b'), dep('e2', 'c')]);
    expect(order(container)).toEqual(['e1', 'e2']);
    const line = (id: string) =>
      container.querySelector(
        `[data-flow-edge="${id}"] path[data-flow-edge-interaction]`
      )!;
    fireEvent.mouseEnter(line('e1'));
    expect(order(container)).toEqual(['e2', 'e1']);
    fireEvent.mouseLeave(line('e1'));
    expect(order(container)).toEqual(['e1', 'e2']);
  });

  // The line takes its hover colour from the attribute, by CSS: the hover is
  // an attribute on an existing element and draws no component.
  it('marks the hovered edge by an attribute and keeps the other one plain', () => {
    const { container } = renderFlow([dep('e1', 'b'), dep('e2', 'c')]);
    const group = (id: string) =>
      container.querySelector(`[data-flow-edge="${id}"]`)!;
    const line = group('e1').querySelector('path[data-flow-edge-interaction]')!;
    expect(group('e1')).toHaveClass('group/edge');
    fireEvent.mouseEnter(line);
    expect(group('e1')).toHaveAttribute('data-hovered');
    expect(group('e2')).not.toHaveAttribute('data-hovered');
    expect(strokeOf(container, 'e2')).toBe('var(--muted-foreground)');
    fireEvent.mouseLeave(line);
    expect(group('e1')).not.toHaveAttribute('data-hovered');
  });

  // A hover that changes no edge draws and moves nothing.
  it('does not touch the DOM when the hovered edge does not change', () => {
    const { container } = renderFlow([dep('e1', 'b'), dep('e2', 'c')]);
    const line = container.querySelector(
      '[data-flow-edge="e1"] path[data-flow-edge-interaction]'
    )!;
    fireEvent.mouseEnter(line);
    const seen: MutationRecord[] = [];
    const observer = new MutationObserver((records) => seen.push(...records));
    observer.observe(container, {
      subtree: true,
      childList: true,
      attributes: true,
    });
    fireEvent.mouseEnter(line);
    fireEvent.mouseEnter(line);
    observer.takeRecords().forEach((record) => seen.push(record));
    observer.disconnect();
    expect(seen).toHaveLength(0);
  });

  it('a selected edge keeps the top over the other edge, which stays plain', () => {
    const { container } = renderFlow([dep('e1', 'b'), dep('e2', 'c', true)]);
    expect(strokeOf(container, 'e2')).toBe('var(--primary)');
    expect(strokeOf(container, 'e1')).toBe('var(--muted-foreground)');
  });
});

// The owner asked that the buttons of a link lie in a row across a vertical
// link, so that the ranks of a top-to-bottom graph can be closer.
describe('EdgeButtons orientation', () => {
  function buttonsOf(direction: 'LR' | 'TB', parallel = true) {
    const view = renderEdge(
      <DependencyEdge
        {...edgeProps(
          {},
          { insertable: true },
          direction === 'TB'
            ? {
                sourcePosition: Position.Bottom,
                targetPosition: Position.Top,
              }
            : {}
        )}
      />,
      makeActions({ direction }),
      parallel ? makeAffordances() : null
    );
    hoverEdge(view);
    return view.getByTestId('ve-canvas-edge-buttons');
  }

  it('lays the buttons in a row in a top-to-bottom layout', () => {
    const row = buttonsOf('TB');
    expect(row).toHaveClass('flex-row');
    expect(row).not.toHaveClass('flex-col');
    // Insert, parallel and delete, side by side.
    expect(row.children).toHaveLength(3);
  });

  it('keeps the row of a left-to-right layout', () => {
    const row = buttonsOf('LR');
    expect(row).toHaveClass('flex-row');
    expect(row).not.toHaveClass('flex-col');
  });

  it('centres the row on the middle of the link', () => {
    const row = buttonsOf('TB');
    expect(row.style.transform).toMatch(/^translate\(-50%, -50%\) translate\(/);
  });

  it('puts the restore button of an inferred link in the same row', () => {
    const view = renderEdge(
      <LockedEdge
        {...edgeProps(
          { id: 'inferred:a->b', kind: 'inferred' },
          { lockReason: 'why', restorable: true, insertable: true },
          { sourcePosition: Position.Bottom, targetPosition: Position.Top }
        )}
      />,
      makeActions({ direction: 'TB' }),
      makeAffordances()
    );
    hoverEdge(view);
    expect(view.getByTestId('ve-canvas-edge-buttons')).toHaveClass('flex-row');
  });
});
