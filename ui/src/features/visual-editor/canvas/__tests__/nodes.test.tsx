// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useEffect, type ReactElement } from 'react';
import { NodeStatus } from '@/api/v1/schema';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useStepElapsed } from '@/hooks/useStepElapsed';
import type { CanvasNode } from '../../contracts';
import type { InputProvenance } from '../../run/inputProvenance';
import {
  resetRunAvailability,
  RunLinksContext,
} from '../../references/RunLinks';
import { AgentRunContext } from '../agentRunContext';
import { CanvasActionsContext, type CanvasActions } from '../context';
import { FlowProvider, useFlow, type NodeProps } from '../engine';
import { HandlerNode } from '../nodes/HandlerNode';
import { nodeTypes } from '../nodes';
import {
  NODE_HEIGHT,
  NODE_WIDTH,
  nodeSize,
  routePorts,
  routerSize,
} from '../layout';
import { AgentNode } from '../nodes/AgentNode';
import { RouterNode } from '../nodes/RouterNode';
import { TOOLBAR_LINGER_MS } from '../nodes/NodeParts';
import { StepNode } from '../nodes/StepNode';
import { ToolNode } from '../nodes/ToolNode';
import {
  routeHandleId,
  type AgentRunInfo,
  type CanvasNodeWithRoutes,
  type StepFlowNode,
} from '../types';

function makeNode(over: Partial<CanvasNode> = {}): CanvasNode {
  return {
    key: 'steps[0]',
    label: 'fetch',
    subtitle: 'curl -sSf https://example.com',
    kind: 'run',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    ...over,
  };
}

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

function nodeProps(
  node: CanvasNode,
  over: Partial<NodeProps<StepFlowNode>> = {}
): NodeProps<StepFlowNode> {
  return {
    id: node.key,
    data: { node },
    selected: false,
    isConnectable: true,
    ...over,
  };
}

function renderIn(ui: ReactElement, actions: CanvasActions = makeActions()) {
  return render(
    <FlowProvider>
      <CanvasActionsContext.Provider value={actions}>
        {ui}
      </CanvasActionsContext.Provider>
    </FlowProvider>
  );
}

/** Renders a node and hovers its card: the toolbar mounts its buttons. */
function renderHot(ui: ReactElement, actions: CanvasActions = makeActions()) {
  const view = renderIn(ui, actions);
  fireEvent.pointerEnter(
    view.getByTestId('ve-canvas-node-toolbar').parentElement!
  );
  return view;
}

/** Renders in a canvas that shows the run `agentRun`. */
function renderInRun(ui: ReactElement, agentRun: AgentRunInfo | null) {
  return renderIn(
    <AgentRunContext.Provider value={agentRun}>{ui}</AgentRunContext.Provider>
  );
}

describe('compact cards', () => {
  const compact = makeActions({ compact: true });

  it('shows the icon of a step, with its name as the tooltip and the caption', () => {
    const { getByTestId, queryByText } = renderIn(
      <StepNode {...nodeProps(makeNode())} />,
      compact
    );
    const card = getByTestId('ve-canvas-node-steps[0]');
    // The subtitle stays out of a compact card.
    expect(queryByText('curl -sSf https://example.com')).toBeNull();
    expect(card).toHaveAttribute('role', 'group');
    expect(card).toHaveAttribute('aria-label', 'fetch');
    expect(card.getAttribute('title')).toContain('fetch');
    expect(card).toHaveClass('h-14', 'w-14');
    expect(card.querySelector('svg')).not.toBeNull();
  });

  // The owner asked for the name of the step under the compact card, always.
  describe('caption', () => {
    it('shows the name under the card, on one line, centred and cut with an ellipsis', () => {
      const { getByTestId } = renderIn(
        <StepNode {...nodeProps(makeNode({ label: 'extract orders' }))} />,
        compact
      );
      const caption = getByTestId('ve-canvas-node-caption');
      expect(caption).toHaveTextContent('extract orders');
      expect(caption).toHaveAttribute('title', 'extract orders');
      expect(caption).toHaveClass(
        'truncate',
        'text-center',
        'text-xs',
        'text-foreground'
      );
      // About 120 px wide, below the 56 px card.
      expect(caption.style.width).toBe('120px');
      expect(caption.style.height).toBe('20px');
      expect(caption.style.top).toBe('66px');
      expect(getByTestId('ve-canvas-compact-frame')).toContainElement(caption);
    });

    it('is in the box that the layout reserves, for each direction', () => {
      for (const direction of ['TB', 'LR'] as const) {
        const view = renderIn(
          <StepNode {...nodeProps(makeNode())} />,
          makeActions({ compact: true, direction })
        );
        const frame = view.getByTestId('ve-canvas-compact-frame');
        const size = nodeSize(makeNode(), true, direction);
        expect(frame.style.width).toBe(`${size.width}px`);
        expect(frame.style.height).toBe(`${size.height}px`);
        const caption = view.getByTestId('ve-canvas-node-caption');
        const top = parseFloat(caption.style.top);
        // Under the card, and inside the box in a column.
        expect(top).toBeGreaterThanOrEqual(56);
        if (direction === 'TB') {
          expect(top + 20).toBeLessThanOrEqual(size.height);
        }
        view.unmount();
      }
    });

    it('has no caption on a detailed card', () => {
      const { queryByTestId } = renderIn(
        <StepNode {...nodeProps(makeNode())} />
      );
      expect(queryByTestId('ve-canvas-node-caption')).toBeNull();
      expect(queryByTestId('ve-canvas-compact-frame')).toBeNull();
    });

    it('puts the handles on the box, so that the links leave under the caption', () => {
      const { container } = renderIn(
        <StepNode {...nodeProps(makeNode())} />,
        makeActions({ compact: true, direction: 'TB' })
      );
      const frame = container.querySelector(
        '[data-testid="ve-canvas-compact-frame"]'
      )!;
      expect(frame.querySelectorAll(':scope > [data-flow-handle]').length).toBe(
        2
      );
    });
  });

  it('centres the toolbar over the card', () => {
    const { getByTestId } = renderIn(
      <StepNode {...nodeProps(makeNode())} />,
      compact
    );
    const toolbar = getByTestId('ve-canvas-node-toolbar');
    expect(toolbar.className).toContain('left-1/2');
    expect(toolbar.className).toContain('-translate-x-1/2');
    expect(toolbar.className).not.toContain('right-0');
    // It is the child of the card, not of the frame: it centres on the card.
    expect(getByTestId('ve-canvas-node-steps[0]')).toContainElement(toolbar);
  });

  it('keeps the status dot of a compact step', () => {
    const node = makeNode({
      run: {
        status: NodeStatus.Success,
        statusLabel: 'succeeded',
        retryCount: 0,
        doneCount: 1,
      },
    });
    const { getByTestId } = renderIn(
      <StepNode {...nodeProps(node)} />,
      compact
    );
    expect(getByTestId('ve-canvas-node-status')).toHaveAttribute(
      'data-status',
      String(NodeStatus.Success)
    );
  });

  it('shows only the icon in the card of a handler and of a router', () => {
    const handler = renderIn(
      <HandlerNode
        {...nodeProps(makeNode({ lane: 'handler', label: 'cleanup' }))}
      />,
      compact
    );
    // The name is the caption, not a line of the card.
    expect(
      handler.getByTestId('ve-canvas-node-steps[0]').textContent
    ).not.toContain('cleanup');
    expect(handler.getByTestId('ve-canvas-node-caption')).toHaveTextContent(
      'cleanup'
    );
    handler.unmount();
    const router = renderIn(
      <RouterNode
        {...nodeProps(
          makeNode({ kind: 'router', label: 'route-it', routes: ['a'] })
        )}
      />,
      compact
    );
    expect(router.queryByText('route-it')).toBeNull();
    expect(router.getByTestId('ve-canvas-node-steps[0]')).toHaveAttribute(
      'aria-label',
      'route-it'
    );
  });
});

describe('StepNode', () => {
  it('shows label and subtitle', () => {
    const { getByTestId, getByText } = renderIn(
      <StepNode {...nodeProps(makeNode())} />
    );
    expect(getByTestId('ve-canvas-node-steps[0]')).toBeInTheDocument();
    expect(getByText('fetch')).toHaveAttribute('title', 'fetch');
    expect(getByText('curl -sSf https://example.com')).toBeInTheDocument();
  });

  it('has one target and one source handle', () => {
    const { container } = renderIn(<StepNode {...nodeProps(makeNode())} />);
    const ids = Array.from(container.querySelectorAll('[data-flow-handle]'))
      .map((el) => el.getAttribute('data-handle-id'))
      .sort();
    expect(ids).toEqual(['source', 'target']);
  });

  it('puts handles at the left and right for LR', () => {
    const { container } = renderIn(<StepNode {...nodeProps(makeNode())} />);
    expect(
      container.querySelector('[data-handle-position="left"]')
    ).not.toBeNull();
    expect(
      container.querySelector('[data-handle-position="right"]')
    ).not.toBeNull();
  });

  it('puts handles at the top and bottom for TB', () => {
    const { container } = renderIn(
      <StepNode {...nodeProps(makeNode())} />,
      makeActions({ direction: 'TB' })
    );
    expect(
      container.querySelector('[data-handle-position="top"]')
    ).not.toBeNull();
    expect(
      container.querySelector('[data-handle-position="bottom"]')
    ).not.toBeNull();
  });

  it('marks the selected node with a primary ring', () => {
    const { getByTestId } = renderIn(
      <StepNode {...nodeProps(makeNode(), { selected: true })} />
    );
    expect(getByTestId('ve-canvas-node-steps[0]').className).toContain(
      'ring-primary'
    );
  });
});

describe('StepNode issues and run', () => {
  it('shows error and warning badges', () => {
    const { getByLabelText } = renderIn(
      <StepNode
        {...nodeProps(makeNode({ issues: { errors: 2, warnings: 1 } }))}
      />
    );
    expect(getByLabelText('Errors: 2')).toHaveTextContent('2');
    expect(getByLabelText('Warnings: 1')).toHaveTextContent('1');
  });

  it('draws the counts with the shared count badge, the error in destructive red', () => {
    const { getByLabelText } = renderIn(
      <StepNode
        {...nodeProps(makeNode({ issues: { errors: 2, warnings: 1 } }))}
      />
    );
    const shape = [
      'h-5',
      'min-w-5',
      'rounded-full',
      'text-[11px]',
      'font-semibold',
    ];
    expect(getByLabelText('Errors: 2')).toHaveClass(
      ...shape,
      'bg-destructive',
      'text-destructive-foreground'
    );
    expect(getByLabelText('Warnings: 1')).toHaveClass(...shape);
    expect(getByLabelText('Warnings: 1')).toHaveClass('bg-warning');
  });

  // parity:canvas-node-issue-badges
  it('lists the messages in the tooltips of the badges', () => {
    const node = makeNode({
      issues: { errors: 2, warnings: 1 },
      issueMessages: {
        errors: ['unknown dependency "x"', 'missing command'],
        warnings: ['no description'],
      },
    });
    const { getByRole } = renderIn(<StepNode {...nodeProps(node)} />);
    const errorText = 'Errors: 2\nunknown dependency "x"\nmissing command';
    const errors = getByRole('img', { name: errorText });
    expect(errors).toHaveAttribute('title', errorText);
    expect(errors).toHaveTextContent('2');
    const warningText = 'Warnings: 1\nno description';
    expect(getByRole('img', { name: warningText })).toHaveAttribute(
      'title',
      warningText
    );
  });

  // The owner asked for the badges on the top right corner, as the count of
  // the Cron launcher.
  it('pins the badges on the top right corner of the card, the errors on the corner', () => {
    const { getByTestId, getByLabelText } = renderIn(
      <StepNode
        {...nodeProps(makeNode({ issues: { errors: 2, warnings: 1 } }))}
      />
    );
    const group = getByTestId('ve-canvas-node-issues');
    expect(group).toHaveClass(
      'absolute',
      '-right-2',
      '-top-2',
      'flex-row-reverse'
    );
    // The card, not the title row, holds the group.
    expect(getByTestId('ve-canvas-node-steps[0]')).toContainElement(group);
    const [first, second] = Array.from(group.children);
    expect(first).toBe(getByLabelText('Errors: 2'));
    expect(second).toBe(getByLabelText('Warnings: 1'));
  });

  it('pins the badges on the same corner of a compact card', () => {
    const { getByTestId } = renderIn(
      <StepNode
        {...nodeProps(makeNode({ issues: { errors: 1, warnings: 1 } }))}
      />,
      makeActions({ compact: true })
    );
    const group = getByTestId('ve-canvas-node-issues');
    expect(group).toHaveClass('absolute', '-right-2', '-top-2');
    expect(getByTestId('ve-canvas-node-steps[0]')).toContainElement(group);
  });

  it('hides badges without issues', () => {
    const { queryByLabelText } = renderIn(
      <StepNode {...nodeProps(makeNode())} />
    );
    expect(queryByLabelText(/Errors/)).toBeNull();
    expect(queryByLabelText(/Warnings/)).toBeNull();
  });

  it('shows the status ring, dot, duration and retries', () => {
    const node = makeNode({
      run: {
        status: NodeStatus.Failed,
        statusLabel: 'failed',
        startedAt: '2026-01-01T00:00:00Z',
        finishedAt: '2026-01-01T00:02:05Z',
        retryCount: 3,
        doneCount: 1,
      },
    });
    const { getByTestId, getByText, getByLabelText } = renderIn(
      <StepNode {...nodeProps(node)} />
    );
    // The colour of the status is the border of the card, not a ring around it.
    const failedCard = getByTestId('ve-canvas-node-steps[0]');
    expect(failedCard).toHaveClass('border-destructive');
    expect(failedCard.className).not.toContain('ring-destructive');
    expect(getByTestId('status-dot').className).toContain('--status-error');
    expect(getByText('2m 5s')).toBeInTheDocument();
    expect(getByLabelText('Retried 3×')).toHaveTextContent('3');
  });

  // A step that the shown run did not execute but whose outputs it reused
  // keeps the status Skipped and reads as reused: a source icon, and the
  // source in its tooltip. The card keeps its size.
  describe('reused marker', () => {
    const skipped = (reused?: InputProvenance) =>
      makeNode({
        ...(reused ? { reuseOutline: 'success' as const } : {}),
        run: {
          status: NodeStatus.Skipped,
          statusLabel: 'skipped',
          retryCount: 0,
          doneCount: 0,
          ...(reused ? { reused } : {}),
        },
      });
    const source = (extra: Partial<InputProvenance>): InputProvenance => ({
      step: 'fetch',
      source: 'run',
      runId: 'r0abcdef12',
      stale: false,
      capturedAt: Date.parse('2026-10-01T00:00:00Z'),
      ...extra,
    });

    it('shows only for a reused step, with the card size unchanged', () => {
      const plain = renderIn(<StepNode {...nodeProps(skipped())} />);
      expect(plain.queryByTestId('ve-node-reused')).toBeNull();
      plain.unmount();

      const view = renderIn(<StepNode {...nodeProps(skipped(source({})))} />);
      const card = view.getByTestId('ve-canvas-node-steps[0]');
      // The same size and width of line; only the outline is dashed and green.
      expect(card).toHaveClass('h-16', 'w-60', 'border-[1.5px]');
      expect(view.getByTestId('ve-node-reuse-outline')).toBeInTheDocument();
      expect(card).toContainElement(view.getByTestId('ve-node-reused'));
      // The status stays Skipped, and the dot is the hatched green dot of the
      // Activity list.
      const dot = view.getByTestId('status-dot');
      expect(dot).toHaveAttribute('data-status', String(NodeStatus.Skipped));
      expect(dot).toHaveAttribute('data-reused', 'success');
      expect(dot.className).toContain('--status-success');
    });

    it.each([
      [
        { source: 'run' as const },
        'Outputs reused from run r0abcdef, 2026-10-01',
      ],
      [
        { source: 'cache' as const },
        'Cached values from run r0abcdef, 2026-10-01',
      ],
      [
        { source: 'cache' as const, runId: undefined },
        'From your cache (2026-10-0',
      ],
    ])('names the source in the label: %j', (extra, title) => {
      const { getByTestId } = renderIn(
        <StepNode {...nodeProps(skipped(source(extra)))} />
      );
      const mark = getByTestId('ve-node-reused');
      expect(mark).toHaveAttribute('data-source', extra.source);
      expect(mark.getAttribute('aria-label')).toContain(title);
      // The whole run id is in the name too.
      if ('runId' in extra && extra.runId === undefined) return;
      expect(mark.getAttribute('aria-label')).toContain('(r0abcdef12)');
    });

    // The card says it in words: a chip with the source and the short id of
    // its run, a dashed outline and the status "Reused".
    it('shows a text chip with the source run on a card that has room', () => {
      const view = renderIn(<StepNode {...nodeProps(skipped(source({})))} />);
      expect(view.getByTestId('input-mark-chip-text')).toHaveTextContent(
        'from run r0abcdef'
      );
      expect(view.getByTestId('status-dot')).toHaveAttribute(
        'aria-label',
        'Reused'
      );
      view.unmount();
      const cache = renderIn(
        <StepNode {...nodeProps(skipped(source({ source: 'cache' })))} />
      );
      expect(cache.getByTestId('input-mark-chip-text')).toHaveTextContent(
        'cache of run r0abcdef'
      );
    });

    it('shows the icon, the dashed outline and the tooltip, with no chip text, on a compact card', () => {
      const compact = renderIn(
        <StepNode {...nodeProps(skipped(source({})))} />,
        makeActions({ compact: true })
      );
      expect(compact.queryByTestId('input-mark-chip-text')).toBeNull();
      expect(
        compact.getByTestId('ve-node-reused').getAttribute('aria-label')
      ).toContain('(r0abcdef12)');
      expect(compact.getByTestId('ve-node-reuse-outline')).toBeInTheDocument();
    });

    // The outline tells what the source run gave: the success colour for a
    // step that it gave, the destructive colour for a step that it could not
    // give. A card with no `reuseOutline` (a step after the executed step, or
    // on another branch) keeps its plain outline, even when it carries outputs.
    describe('outline', () => {
      const failed = (): CanvasNode =>
        makeNode({
          reuseOutline: 'failure',
          run: {
            status: NodeStatus.Skipped,
            statusLabel: 'skipped',
            retryCount: 0,
            doneCount: 0,
            reuseFailed: { runId: 'r0abcdef12' },
          },
        });

      it('is dashed and green for a step that the source run gave', () => {
        const view = renderIn(<StepNode {...nodeProps(skipped(source({})))} />);
        const card = view.getByTestId('ve-canvas-node-steps[0]');
        const rect = view
          .getByTestId('ve-node-reuse-outline')
          .querySelector('rect');
        expect(rect).toHaveAttribute('stroke-dasharray', '6 4');
        expect(rect).toHaveClass('stroke-success');
        expect(card.getAttribute('title')).toBe(
          'Reused from run r0abcdef: succeeded'
        );
      });

      it('is dashed and red for a step that the source run could not give', () => {
        const view = renderIn(<StepNode {...nodeProps(failed())} />);
        const dot = view.getByTestId('status-dot');
        expect(dot).toHaveAttribute('data-reused', 'failed');
        expect(dot.className).toContain('--status-error');
        const card = view.getByTestId('ve-canvas-node-steps[0]');
        const rect = view
          .getByTestId('ve-node-reuse-outline')
          .querySelector('rect');
        expect(rect).toHaveAttribute('stroke-dasharray', '6 4');
        expect(rect).toHaveClass('stroke-destructive');
        expect(card.getAttribute('title')).toBe(
          'Reused from run r0abcdef: failed'
        );
      });

      // The dashes are the one line of the card: its border is transparent
      // and it has no ring, in every state. The dashes stay on hover, focus
      // and selection, as those of a launcher that is not set up do.
      it('replaces the border and the run ring with dashes that stay in every state', () => {
        const view = renderIn(<StepNode {...nodeProps(skipped(source({})))} />);
        const card = view.getByTestId('ve-canvas-node-steps[0]');
        const tokens = card.className.split(/\s+/);
        expect(tokens).toContain('border-transparent');
        expect(tokens).toContain('hover:border-transparent');
        expect(tokens).toContain('focus-within:border-transparent');
        expect(tokens).not.toContain('border-muted-foreground/60');
        expect(tokens.some((t) => t.includes('ring'))).toBe(false);
        expect(
          view.getByTestId('ve-node-reuse-outline').querySelector('rect')
        ).toHaveAttribute('stroke-dasharray', '6 4');
        view.unmount();

        // A plain skipped card has the border of its status.
        const plain = renderIn(<StepNode {...nodeProps(skipped())} />);
        expect(
          plain.getByTestId('ve-canvas-node-steps[0]').className
        ).toContain('border-[var(--status-neutral)]');
        expect(plain.queryByTestId('ve-node-reuse-outline')).toBeNull();
        plain.unmount();

        // A selected card has no ring: its dashes turn violet, the same dashes.
        const selected = renderIn(
          <StepNode {...nodeProps(skipped(source({})), { selected: true })} />
        );
        const chosen = selected.getByTestId('ve-canvas-node-steps[0]');
        expect(chosen).not.toHaveClass('ring-2');
        const rects = selected
          .getByTestId('ve-node-reuse-outline')
          .querySelectorAll('rect');
        expect(rects).toHaveLength(1);
        expect(rects[0]).toHaveAttribute('stroke-dasharray', '6 4');
        expect(rects[0]).toHaveClass('stroke-primary');
        expect(rects[0]).not.toHaveClass('stroke-success');
      });

      // A step after the executed step is skipped and gave nothing: the
      // overlay cannot tell it from a step that the source could not give, so
      // the card follows its outline, which only a previous step has.
      it('keeps a plain dot on a skipped step that is not a previous step', () => {
        const node = makeNode({
          run: {
            status: NodeStatus.Skipped,
            statusLabel: 'skipped',
            retryCount: 0,
            doneCount: 0,
            reuseFailed: { runId: 'r0abcdef12' },
          },
        });
        const view = renderIn(<StepNode {...nodeProps(node)} />);
        const dot = view.getByTestId('status-dot');
        expect(dot).not.toHaveAttribute('data-reused');
        expect(dot.className).toContain('--status-neutral');
        expect(view.queryByTestId('ve-node-reuse-outline')).toBeNull();
      });

      it('has no outline on a step that carries outputs but is not a previous step', () => {
        const node = makeNode({
          run: {
            status: NodeStatus.Skipped,
            statusLabel: 'skipped',
            retryCount: 0,
            doneCount: 1,
            reused: source({}),
          },
        });
        const view = renderIn(<StepNode {...nodeProps(node)} />);
        const card = view.getByTestId('ve-canvas-node-steps[0]');
        expect(view.queryByTestId('ve-node-reuse-outline')).toBeNull();
        expect(card.getAttribute('title')).toBeNull();
        // The chip of the source stays.
        expect(view.getByTestId('ve-node-reused')).toBeInTheDocument();
      });

      it('keeps the geometry of the card, in both outline colours and compact', () => {
        const plain = renderIn(<StepNode {...nodeProps(skipped())} />);
        expect(plain.getByTestId('ve-canvas-node-steps[0]')).toHaveClass(
          'h-16',
          'w-60',
          'border-[1.5px]'
        );
        plain.unmount();
        for (const node of [skipped(source({})), failed()]) {
          const view = renderIn(<StepNode {...nodeProps(node)} />);
          const card = view.getByTestId('ve-canvas-node-steps[0]');
          expect(card).toHaveClass('h-16', 'w-60', 'border-[1.5px]');
          view.unmount();
        }
        const compact = makeActions({ compact: true });
        const green = renderIn(
          <StepNode {...nodeProps(skipped(source({})))} />,
          compact
        );
        expect(green.getByTestId('ve-canvas-node-steps[0]')).toHaveClass(
          'h-14',
          'w-14'
        );
        expect(
          green.getByTestId('ve-node-reuse-outline').querySelector('rect')
        ).toHaveClass('stroke-success');
        green.unmount();
        const red = renderIn(<StepNode {...nodeProps(failed())} />, compact);
        const card = red.getByTestId('ve-canvas-node-steps[0]');
        expect(card).toHaveClass('h-14', 'w-14');
        expect(
          red.getByTestId('ve-node-reuse-outline').querySelector('rect')
        ).toHaveClass('stroke-destructive');
        // The tooltip of a compact card keeps its name and adds the source.
        expect(card.getAttribute('title')).toBe(
          'fetch\ncurl -sSf https://example.com\nReused from run r0abcdef: failed'
        );
      });
    });

    it('shows the time that the step took in its source run in the duration slot', () => {
      const view = renderIn(
        <StepNode
          {...nodeProps(
            skipped(
              source({
                startedAt: Date.parse('2026-10-01T00:00:00Z'),
                finishedAt: Date.parse('2026-10-01T00:00:20Z'),
              })
            )
          )}
        />
      );
      const duration = view.getByTestId('ve-node-duration');
      expect(duration).toHaveTextContent('20s');
      expect(duration).toHaveAttribute('data-reused');
      expect(duration.getAttribute('title')).toContain('r0abcdef');
    });

    // The tooltip names the run and, when the history holds it, selects it;
    // when the server dropped it, it says so.
    it('selects the source run from the tooltip when the history holds it', async () => {
      const select = vi.fn();
      const links = {
        select,
        resolve: (id: string) => (id === 'r0abcdef12' ? id : null),
        exists: async () => true,
      };
      const view = renderIn(
        <RunLinksContext.Provider value={links}>
          <StepNode {...nodeProps(skipped(source({})))} />
        </RunLinksContext.Provider>
      );
      await userEvent.hover(view.getByTestId('ve-node-reused'));
      const [show] = await screen.findAllByTestId('input-mark-show');
      await userEvent.click(show!);
      expect(select).toHaveBeenCalledWith('r0abcdef12');
    });

    it('says that a source run that the server dropped is no longer available', async () => {
      resetRunAvailability();
      const links = {
        select: vi.fn(),
        resolve: () => null,
        exists: vi.fn(async () => false),
      };
      const view = renderIn(
        <RunLinksContext.Provider value={links}>
          <StepNode {...nodeProps(skipped(source({})))} />
        </RunLinksContext.Provider>
      );
      await userEvent.hover(view.getByTestId('ve-node-reused'));
      const [gone] = await screen.findAllByTestId('input-mark-gone');
      expect(gone).toHaveTextContent('run r0abcdef (no longer available)');
      expect(links.exists).toHaveBeenCalledWith('r0abcdef12');
      expect(screen.queryByTestId('input-mark-show')).toBeNull();
    });

    it('uses the warning tone for a stale source', () => {
      const { getByTestId } = renderIn(
        <StepNode {...nodeProps(skipped(source({ stale: true })))} />
      );
      expect(getByTestId('ve-node-reused').className).toContain('text-warning');
    });

    it('shows the icon alone on a compact card, with the card size unchanged', () => {
      const compact = makeActions({ compact: true });
      const plain = renderIn(<StepNode {...nodeProps(skipped())} />, compact);
      expect(plain.getByTestId('ve-canvas-node-steps[0]')).toHaveClass(
        'h-14',
        'w-14'
      );
      plain.unmount();
      const view = renderIn(
        <StepNode {...nodeProps(skipped(source({ source: 'cache' })))} />,
        compact
      );
      const card = view.getByTestId('ve-canvas-node-steps[0]');
      expect(card).toHaveClass('h-14', 'w-14');
      const mark = view.getByTestId('ve-node-reused');
      expect(card).toContainElement(mark);
      expect(mark).toHaveTextContent('');
      expect(mark.querySelector('svg')).not.toBeNull();
    });
  });

  // The repeat marker sits in the fixed meta row: the card keeps its size.
  describe('repeat marker', () => {
    const repeat = { mode: 'while' as const, intervalSec: 10 };

    it('shows only for a repeating step, with the card size unchanged', () => {
      const plain = renderIn(<StepNode {...nodeProps(makeNode())} />);
      const plainCard = plain.getByTestId('ve-canvas-node-steps[0]');
      expect(plain.queryByTestId('repeat-badge')).toBeNull();
      const plainClass = plainCard.className;
      plain.unmount();

      const looping = renderIn(
        <StepNode {...nodeProps(makeNode({ repeat }))} />
      );
      const card = looping.getByTestId('ve-canvas-node-steps[0]');
      expect(card).toHaveClass('h-16', 'w-60');
      expect(card.className).toBe(plainClass);
      const badge = looping.getByTestId('repeat-badge');
      expect(card).toContainElement(badge);
      expect(badge).toHaveTextContent('every 10s');
      expect(badge.getAttribute('title')).toBe(
        'repeat while exit 0 · every 10s · no limit'
      );
    });

    it('shows the run count and the iteration once the step has run', () => {
      const node = makeNode({
        repeat: { ...repeat, limit: 5 },
        run: {
          status: NodeStatus.Success,
          statusLabel: 'succeeded',
          startedAt: '2026-01-01T00:00:00Z',
          finishedAt: '2026-01-01T00:00:30Z',
          retryCount: 0,
          doneCount: 3,
        },
      });
      const { getByTestId } = renderIn(<StepNode {...nodeProps(node)} />);
      const badge = getByTestId('repeat-badge');
      expect(badge).toHaveTextContent('×3');
      expect(badge.getAttribute('title')).toContain('iteration 3 of 5');
      expect(badge).not.toHaveClass('motion-safe:animate-pulse');
    });

    it('names the running iteration while the step runs', () => {
      const node = makeNode({
        repeat,
        run: {
          status: NodeStatus.Running,
          statusLabel: 'running',
          startedAt: '2026-01-01T00:00:00Z',
          retryCount: 0,
          doneCount: 2,
        },
      });
      const { getByTestId } = renderIn(<StepNode {...nodeProps(node)} />);
      expect(getByTestId('repeat-badge')).toHaveAttribute(
        'data-running',
        'true'
      );
      expect(getByTestId('repeat-badge').getAttribute('title')).toContain(
        'iteration 3'
      );
    });

    it('shows the icon and the count only on a compact card', () => {
      const node = makeNode({
        repeat,
        run: {
          status: NodeStatus.Success,
          statusLabel: 'succeeded',
          retryCount: 0,
          doneCount: 4,
        },
      });
      const { getByTestId } = renderIn(
        <StepNode {...nodeProps(node)} />,
        makeActions({ compact: true })
      );
      const badge = getByTestId('repeat-badge');
      expect(badge).toHaveTextContent(/^×4$/);
      expect(getByTestId('ve-canvas-node-steps[0]')).toHaveClass(
        'h-14',
        'w-14'
      );
      expect(getByTestId('ve-canvas-node-steps[0]')).toContainElement(badge);
    });
  });

  it('shows no run meta for an unfinished step', () => {
    const node = makeNode({
      run: {
        status: NodeStatus.Running,
        statusLabel: 'running',
        startedAt: '2026-01-01T00:00:00Z',
        retryCount: 0,
        doneCount: 0,
      },
    });
    const { queryByLabelText, getByTestId } = renderIn(
      <StepNode {...nodeProps(node)} />
    );
    expect(queryByLabelText(/Retried/)).toBeNull();
    expect(getByTestId('status-dot').className).toContain(
      'motion-safe:animate-pulse'
    );
  });

  // The card and the run list show one text for one step at one moment: the
  // duration counts up each second while the step runs, in a slot of fixed
  // width so that the card does not change its size.
  describe('running step', () => {
    afterEach(() => {
      vi.useRealTimers();
    });

    it('shows its duration live, as the shared hook does', () => {
      vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
      vi.setSystemTime(new Date('2026-01-01T00:00:10Z'));
      const run = {
        status: NodeStatus.Running,
        statusLabel: 'running',
        startedAt: '2026-01-01T00:00:00Z',
        retryCount: 0,
        doneCount: 0,
      };
      const { getByTestId } = renderIn(
        <StepNode {...nodeProps(makeNode({ run }))} />
      );
      const duration = () => getByTestId('ve-node-duration');
      expect(duration()).toHaveTextContent('10s');
      expect(duration()).toHaveClass('min-w-[7ch]', 'tabular-nums');

      act(() => {
        vi.advanceTimersByTime(3000);
      });
      expect(duration()).toHaveTextContent('13s');
      const hook = renderHook(() => useStepElapsed(run));
      expect(hook.result.current).toBe('13s');
    });

    it('keeps the slot for a finished step', () => {
      const node = makeNode({
        run: {
          status: NodeStatus.Success,
          statusLabel: 'succeeded',
          startedAt: '2026-01-01T00:00:00Z',
          finishedAt: '2026-01-01T00:00:05Z',
          retryCount: 0,
          doneCount: 0,
        },
      });
      const { getByTestId } = renderIn(<StepNode {...nodeProps(node)} />);
      expect(getByTestId('ve-node-duration')).toHaveClass('min-w-[7ch]');
      expect(getByTestId('ve-node-duration')).toHaveTextContent('5s');
    });

    // The API times have a resolution of one second: 0 ms reads "<1s".
    it('reads <1s for a step that ran between two equal whole seconds', () => {
      const node = makeNode({
        run: {
          status: NodeStatus.Success,
          statusLabel: 'succeeded',
          startedAt: '2026-01-01T00:00:05Z',
          finishedAt: '2026-01-01T00:00:05Z',
          retryCount: 0,
          doneCount: 0,
        },
      });
      const { getByTestId } = renderIn(<StepNode {...nodeProps(node)} />);
      expect(getByTestId('ve-node-duration')).toHaveTextContent('<1s');
    });
  });

  // parity:canvas-node-step-card
  it('shows no 0ms for a step that did not start', () => {
    const node = makeNode({
      run: {
        status: NodeStatus.NotStarted,
        statusLabel: 'not started',
        startedAt: '2026-01-01T00:00:00Z',
        finishedAt: '2026-01-01T00:00:00Z',
        retryCount: 0,
        doneCount: 0,
      },
    });
    const { queryByText } = renderIn(<StepNode {...nodeProps(node)} />);
    expect(queryByText('0ms')).toBeNull();
  });

  // A skipped step carries the dates of the run: it shows no duration.
  it('shows no duration for a skipped step', () => {
    const node = makeNode({
      run: {
        status: NodeStatus.Skipped,
        statusLabel: 'skipped',
        startedAt: '2026-01-01T00:00:00Z',
        finishedAt: '2026-01-01T00:00:03Z',
        retryCount: 0,
        doneCount: 0,
      },
    });
    const { queryByText } = renderIn(<StepNode {...nodeProps(node)} />);
    expect(queryByText('3s')).toBeNull();
  });

  it('gives a failed step its failure reason as a tooltip', () => {
    const node = makeNode({
      run: {
        status: NodeStatus.Failed,
        statusLabel: 'failed',
        retryCount: 0,
        doneCount: 0,
        error: 'exit status 2',
      },
    });
    const { getByTestId } = renderIn(<StepNode {...nodeProps(node)} />);
    expect(getByTestId('ve-canvas-node-steps[0]')).toHaveAttribute(
      'title',
      'exit status 2'
    );
    // The dot names the status; the reason is the tooltip of the card.
    expect(getByTestId('status-dot')).toHaveAttribute('title', 'Failed');
  });

  it('names the dot by the translated status, or by the text of the host', () => {
    const named = (statusLabel: string) => {
      const view = renderIn(
        <StepNode
          {...nodeProps(
            makeNode({
              run: {
                status: NodeStatus.PartialSuccess,
                statusLabel,
                retryCount: 0,
                doneCount: 0,
              },
            })
          )}
        />
      );
      const name = view.getByTestId('status-dot').getAttribute('title');
      view.unmount();
      return name;
    };
    // A raw API label is never shown.
    expect(named('partially_succeeded')).toBe('Partially succeeded');
    expect(named('')).toBe('Partially succeeded');
    expect(named('Reuse previous output')).toBe('Reuse previous output');
  });

  it('gives no tooltip to a step that did not fail', () => {
    const node = makeNode({
      run: {
        status: NodeStatus.Success,
        statusLabel: 'succeeded',
        retryCount: 0,
        doneCount: 0,
        error: 'old error',
      },
    });
    const { getByTestId } = renderIn(<StepNode {...nodeProps(node)} />);
    expect(getByTestId('ve-canvas-node-steps[0]')).not.toHaveAttribute('title');
  });

  it('shows no duration for a step without an end', () => {
    const node = makeNode({
      run: {
        status: NodeStatus.Failed,
        statusLabel: 'failed',
        startedAt: '2026-01-01T00:00:00Z',
        retryCount: 2,
        doneCount: 0,
      },
    });
    const { getByLabelText, queryByText } = renderIn(
      <StepNode {...nodeProps(node)} />
    );
    expect(getByLabelText('Retried 2×')).toBeInTheDocument();
    expect(queryByText(/\d+(ms|s)$/)).toBeNull();
  });

  // parity:canvas-node-step-card
  it('shows a Waiting badge next to the dot of a waiting step', () => {
    const node = makeNode({
      run: {
        status: NodeStatus.Waiting,
        statusLabel: 'waiting',
        retryCount: 0,
        doneCount: 0,
      },
    });
    const { getByTestId } = renderIn(<StepNode {...nodeProps(node)} />);
    const badge = getByTestId('ve-canvas-node-waiting');
    expect(badge).toHaveAccessibleName('Waiting');
    expect(badge).toHaveTextContent('Waiting');
    expect(badge.className).toContain('bg-warning');
    expect(badge.className).not.toContain('amber');
    expect(getByTestId('ve-canvas-node-status').nextElementSibling).toBe(badge);
    // The dot names the status in its translated text, never the API label.
    expect(getByTestId('status-dot')).toHaveAccessibleName('Waiting');
  });

  it('shows no Waiting badge for other states', () => {
    for (const status of [
      NodeStatus.Running,
      NodeStatus.Success,
      NodeStatus.NotStarted,
    ]) {
      const view = renderIn(
        <StepNode
          {...nodeProps(
            makeNode({
              run: { status, statusLabel: '', retryCount: 0, doneCount: 0 },
            })
          )}
        />
      );
      expect(view.queryByTestId('ve-canvas-node-waiting')).toBeNull();
      view.unmount();
    }
  });

  // The badges must not change the box that the layout gives to a card.
  it('keeps the size of the card with badges', () => {
    const node = makeNode({
      issues: { errors: 2, warnings: 1 },
      run: {
        status: NodeStatus.Waiting,
        statusLabel: 'waiting',
        retryCount: 1,
        doneCount: 0,
      },
    });
    const { getByTestId } = renderIn(<StepNode {...nodeProps(node)} />);
    expect(getByTestId('ve-canvas-node-steps[0]')).toHaveClass('h-16', 'w-60');
    expect(nodeSize(node)).toEqual({ width: NODE_WIDTH, height: NODE_HEIGHT });
    expect([NODE_WIDTH, NODE_HEIGHT]).toEqual([240, 64]);
  });

  it('keeps the selection ring over the run ring', () => {
    const node = makeNode({
      run: {
        status: NodeStatus.Success,
        statusLabel: 'succeeded',
        retryCount: 0,
        doneCount: 1,
      },
    });
    const { getByTestId } = renderIn(
      <StepNode {...nodeProps(node, { selected: true })} />
    );
    const cls = getByTestId('ve-canvas-node-steps[0]').className;
    expect(cls).toContain('ring-primary');
    expect(cls).toContain('border-success');
    expect(cls).not.toContain('ring-success');
  });
});

describe('StepNode toolbar', () => {
  // parity:canvas-node-toolbar
  it('orders Run step, Open step, Duplicate step, Copy step and Delete', () => {
    const { getByTestId, queryByRole } = renderHot(
      <StepNode {...nodeProps(makeNode())} />
    );
    const labels = Array.from(
      getByTestId('ve-canvas-node-toolbar').querySelectorAll('button')
    ).map((button) => button.getAttribute('aria-label'));
    expect(labels).toEqual([
      'Run step',
      'Open step',
      'Duplicate step',
      'Copy step',
      'Delete',
    ]);
    expect(queryByRole('button', { name: 'More actions' })).toBeNull();
  });

  it('duplicates and copies the step of the toolbar', () => {
    const onCommand = vi.fn();
    const { getByRole } = renderHot(
      <StepNode {...nodeProps(makeNode())} />,
      makeActions({ onCommand })
    );
    const duplicate = getByRole('button', { name: 'Duplicate step' });
    const copy = getByRole('button', { name: 'Copy step' });
    fireEvent.click(duplicate);
    fireEvent.click(copy);
    expect(onCommand).toHaveBeenNthCalledWith(1, {
      kind: 'duplicate',
      step: 'steps[0]',
    });
    expect(onCommand).toHaveBeenNthCalledWith(2, {
      kind: 'copy',
      step: 'steps[0]',
    });
  });

  it('keeps Copy step but not Duplicate step when read-only', () => {
    const { getByRole, queryByRole } = renderHot(
      <StepNode {...nodeProps(makeNode())} />,
      makeActions({ editable: false })
    );
    expect(getByRole('button', { name: 'Copy step' })).toBeInTheDocument();
    expect(queryByRole('button', { name: 'Duplicate step' })).toBeNull();
  });

  it('calls the actions of the buttons', () => {
    const onCommand = vi.fn();
    const onDeleteStep = vi.fn();
    const { getByRole } = renderHot(
      <StepNode {...nodeProps(makeNode())} />,
      makeActions({ onCommand, onDeleteStep })
    );
    fireEvent.click(getByRole('button', { name: 'Run step' }));
    fireEvent.click(getByRole('button', { name: 'Open step' }));
    fireEvent.click(getByRole('button', { name: 'Delete' }));
    expect(onCommand).toHaveBeenNthCalledWith(1, {
      kind: 'runStep',
      step: 'steps[0]',
    });
    expect(onCommand).toHaveBeenNthCalledWith(2, {
      kind: 'openInspector',
      step: 'steps[0]',
    });
    expect(onDeleteStep).toHaveBeenCalledWith('steps[0]');
  });

  it('makes the buttons 24 px icon buttons that do not drag or pan', () => {
    const { getByTestId } = renderHot(<StepNode {...nodeProps(makeNode())} />);
    const buttons = getByTestId('ve-canvas-node-toolbar').querySelectorAll(
      'button'
    );
    expect(buttons).toHaveLength(5);
    for (const button of buttons) {
      expect(button).toHaveClass('nodrag', 'nopan', 'size-6');
    }
  });

  // The tooltip text has the key of the key table.
  it.each([
    ['Open step', 'Enter'],
    ['Duplicate step', 'Ctrl/Cmd+D'],
    ['Copy step', 'Ctrl/Cmd+C'],
    ['Delete', 'Delete'],
  ])('shows the key of %s in its tooltip', async (name, key) => {
    const { getByRole } = renderHot(<StepNode {...nodeProps(makeNode())} />);
    act(() => getByRole('button', { name, hidden: true }).focus());
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      `${name}${key}`
    );
  });

  it('swaps the icon of Copy step for a Check after a click', () => {
    vi.useFakeTimers();
    try {
      const onCommand = vi.fn();
      const { getByRole } = renderHot(
        <StepNode {...nodeProps(makeNode())} />,
        makeActions({ onCommand })
      );
      const idle = getByRole('button', { name: 'Copy step' }).innerHTML;
      fireEvent.click(getByRole('button', { name: 'Copy step' }));
      expect(onCommand).toHaveBeenCalledWith({
        kind: 'copy',
        step: 'steps[0]',
      });
      const done = getByRole('button', { name: 'Copied' });
      expect(done.innerHTML).not.toBe(idle);
      expect(done.querySelector('svg')).toHaveClass('text-success');
      act(() => {
        vi.advanceTimersByTime(2000);
      });
      expect(getByRole('button', { name: 'Copy step' }).innerHTML).toBe(idle);
    } finally {
      vi.useRealTimers();
    }
  });

  it('hides Delete but keeps Run and Open when read-only', () => {
    const { queryByRole, getByRole } = renderHot(
      <StepNode {...nodeProps(makeNode())} />,
      makeActions({ editable: false })
    );
    expect(queryByRole('button', { name: 'Delete' })).toBeNull();
    expect(getByRole('button', { name: 'Run step' })).toBeInTheDocument();
    expect(getByRole('button', { name: 'Open step' })).toBeInTheDocument();
  });

  // The toolbar never sits beside the node: it would cover the next sibling
  // and put Delete over it.
  it.each(['LR', 'TB'] as const)(
    'puts the toolbar above the node in %s',
    (direction) => {
      const { getByTestId } = renderHot(
        <StepNode {...nodeProps(makeNode())} />,
        makeActions({ direction })
      );
      const cls = getByTestId('ve-canvas-node-toolbar').className;
      expect(cls).toContain('bottom-full');
      expect(cls).not.toContain('left-full');
    }
  );

  // In dark mode the border of a card was close to the canvas.
  it('has a brighter border in dark mode', () => {
    const { getByTestId } = renderHot(<StepNode {...nodeProps(makeNode())} />);
    expect(getByTestId('ve-canvas-node-steps[0]').className).toContain(
      'dark:border-muted-foreground/40'
    );
  });

  // axe/screen reader: the buttons have the same names on every step, so the
  // step name is their description; only the selected step is a tab stop.
  it('names the step as the description of its buttons', () => {
    const { getByRole } = renderHot(<StepNode {...nodeProps(makeNode())} />);
    const open = getByRole('button', { name: 'Open step', hidden: true });
    expect(open).toHaveAttribute('aria-description', 'fetch');
  });

  it('puts only the toolbar of the selected step in the tab order', () => {
    const idle = renderHot(<StepNode {...nodeProps(makeNode())} />);
    for (const button of idle
      .getByTestId('ve-canvas-node-toolbar')
      .querySelectorAll('button')) {
      expect(button).toHaveAttribute('tabindex', '-1');
    }
    idle.unmount();
    const selected = renderHot(
      <StepNode {...nodeProps(makeNode(), { selected: true })} />
    );
    for (const button of selected
      .getByTestId('ve-canvas-node-toolbar')
      .querySelectorAll('button')) {
      expect(button).toHaveAttribute('tabindex', '0');
    }
  });

  // The buttons of 100 nodes made a pan step twice as slow: only a hot node
  // (hovered, focused or selected) mounts them. They stay mounted for a while
  // after the pointer leaves, hidden by CSS, so that a return mounts nothing.
  it('mounts the buttons only for a hovered, focused or selected node', () => {
    vi.useFakeTimers();
    try {
      const view = renderIn(<StepNode {...nodeProps(makeNode())} />);
      const card = view.getByTestId('ve-canvas-node-steps[0]');
      const buttons = () =>
        view.getByTestId('ve-canvas-node-toolbar').querySelectorAll('button');
      expect(buttons()).toHaveLength(0);
      fireEvent.pointerEnter(card);
      expect(buttons()).toHaveLength(5);
      fireEvent.pointerLeave(card);
      act(() => {
        vi.advanceTimersByTime(TOOLBAR_LINGER_MS);
      });
      expect(buttons()).toHaveLength(0);
      fireEvent.focusIn(card);
      expect(buttons()).toHaveLength(5);
      fireEvent.focusOut(card);
      act(() => {
        vi.advanceTimersByTime(TOOLBAR_LINGER_MS);
      });
      expect(buttons()).toHaveLength(0);
      view.unmount();
      const selected = renderIn(
        <StepNode {...nodeProps(makeNode(), { selected: true })} />
      );
      expect(
        selected
          .getByTestId('ve-canvas-node-toolbar')
          .querySelectorAll('button')
      ).toHaveLength(5);
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows the toolbar when selected', () => {
    const { getByTestId } = renderHot(
      <StepNode {...nodeProps(makeNode(), { selected: true })} />
    );
    expect(getByTestId('ve-canvas-node-toolbar').className).toContain(
      'opacity-100'
    );
  });
});

describe('HandlerNode border', () => {
  // The dashed border was close to the colour of the canvas in the light theme.
  it('has a visible border in the light theme', () => {
    const { getByTestId } = renderIn(
      <HandlerNode {...nodeProps(makeNode({ kind: 'run' }))} />
    );
    expect(getByTestId('ve-canvas-node-steps[0]').className).toContain(
      'border-muted-foreground/60'
    );
  });
});

describe('RouterNode', () => {
  const router = (routes: string[]) =>
    makeNode({
      key: 'steps[1]',
      label: 'route',
      kind: 'router',
      action: 'router.route',
      routes,
    });

  it('has one port for each route', () => {
    const { container, getAllByTestId } = renderIn(
      <RouterNode {...nodeProps(router(['ok', 'fail*', 'default']))} />
    );
    expect(getAllByTestId('ve-canvas-route')).toHaveLength(3);
    const ids = Array.from(container.querySelectorAll('[data-flow-handle]'))
      .map((el) => el.getAttribute('data-handle-id'))
      .sort();
    expect(ids).toEqual(
      [
        'source',
        'target',
        routeHandleId('ok'),
        routeHandleId('fail*'),
        routeHandleId('default'),
      ].sort()
    );
  });

  it('shows the pattern with a title', () => {
    const { getByText } = renderIn(
      <RouterNode {...nodeProps(router(['ok']))} />
    );
    expect(getByText('ok')).toHaveAttribute('title', 'ok');
  });

  // parity:canvas-node-issue-badges
  it('lists the messages in the tooltips of its badges', () => {
    const node = {
      ...router(['ok']),
      issues: { errors: 1, warnings: 1 },
      issueMessages: { errors: ['no default route'], warnings: ['unused'] },
    };
    const { getByRole } = renderIn(<RouterNode {...nodeProps(node)} />);
    expect(
      getByRole('img', { name: 'Errors: 1\nno default route' })
    ).toHaveTextContent('1');
    expect(
      getByRole('img', { name: 'Warnings: 1\nunused' })
    ).toBeInTheDocument();
  });

  // jsdom has no hit testing: the nearest pointer-events class decides
  // whether a hover reaches the element and shows its title.
  it('lets a hover reach its tooltips', () => {
    const node = {
      ...router(['ok']),
      routeValue: '${steps.produce.outputs.code}',
      issues: { errors: 1, warnings: 0 },
    };
    const { getByRole, getByTitle } = renderIn(
      <RouterNode {...nodeProps(node)} />
    );
    const hoverable = (element: HTMLElement) => {
      const gate = element.closest(
        '.pointer-events-none, .pointer-events-auto'
      );
      return !gate || gate.classList.contains('pointer-events-auto');
    };
    expect(hoverable(getByTitle('route'))).toBe(true);
    expect(hoverable(getByTitle(node.routeValue))).toBe(true);
    expect(hoverable(getByTitle('ok'))).toBe(true);
    expect(hoverable(getByRole('img', { name: 'Errors: 1' }))).toBe(true);
  });

  // The router is a square turned by 45 degrees, so its box is a square.
  it('is a square box that grows with the route count', () => {
    expect(routerSize(0)).toBe(160);
    expect(routerSize(1)).toBe(160);
    expect(routerSize(3)).toBe(160);
    expect(routerSize(5)).toBe(224);
    const { getByTestId } = renderIn(
      <RouterNode {...nodeProps(router(['a', 'b', 'c', 'd', 'e']))} />
    );
    const box = getByTestId('ve-canvas-node-steps[1]');
    expect(box.style.width).toBe('224px');
    expect(box.style.height).toBe('224px');
  });

  it('draws the four vertices of the square at the middle of its sides', () => {
    const { container } = renderIn(
      <RouterNode {...nodeProps(router(['a']))} />
    );
    expect(container.querySelector('polygon')).toHaveAttribute(
      'points',
      '80,0 160,80 80,160 0,80'
    );
  });

  it('keeps the toolbar and the badges', () => {
    const node = router(['a']);
    node.issues = { errors: 1, warnings: 0 };
    const { getByRole, getByLabelText } = renderHot(
      <RouterNode {...nodeProps(node)} />
    );
    expect(getByRole('button', { name: 'Run step' })).toBeInTheDocument();
    expect(getByLabelText('Errors: 1')).toBeInTheDocument();
  });
});

describe('shapes of the Graph view', () => {
  it('draws a sub-DAG as a subroutine, with its target', () => {
    const node = makeNode({
      kind: 'call',
      action: 'dag.run',
      subDag: 'greeter',
      subtitle: '\u2192 greeter',
    });
    const { getByTestId, getByText } = renderIn(
      <StepNode {...nodeProps(node)} />
    );
    expect(getByTestId('ve-canvas-node-steps[0]')).toHaveAttribute(
      'data-shape',
      'subroutine'
    );
    expect(getByText('\u2192 greeter')).toBeInTheDocument();
  });

  it('draws a fan-out of sub-DAGs as a stack', () => {
    const node = makeNode({ kind: 'call', subDag: 'greeter', parallel: true });
    const { getByTestId } = renderIn(<StepNode {...nodeProps(node)} />);
    expect(getByTestId('ve-canvas-node-steps[0]')).toHaveAttribute(
      'data-shape',
      'stack'
    );
  });

  it('keeps a plain step and a parallel step that is no sub-DAG a card', () => {
    const plain = renderIn(<StepNode {...nodeProps(makeNode())} />);
    expect(plain.getByTestId('ve-canvas-node-steps[0]')).toHaveAttribute(
      'data-shape',
      'card'
    );
    plain.unmount();
    const fanOut = renderIn(
      <StepNode
        {...nodeProps(makeNode({ kind: 'parallel', parallel: true }))}
      />
    );
    expect(fanOut.getByTestId('ve-canvas-node-steps[0]')).toHaveAttribute(
      'data-shape',
      'card'
    );
  });

  it('draws a router as a diamond with what it routes on', () => {
    const node = makeNode({
      key: 'steps[1]',
      label: 'route',
      kind: 'router',
      routes: ['ok', 'fail*'],
      routeValue: '${steps.check.outputs.state}',
    });
    const { getByTestId, getByText, container } = renderIn(
      <RouterNode {...nodeProps(node)} />
    );
    expect(getByTestId('ve-canvas-node-steps[1]')).toHaveAttribute(
      'data-shape',
      'diamond'
    );
    expect(container.querySelector('polygon')).not.toBeNull();
    expect(getByText('${steps.check.outputs.state}')).toBeInTheDocument();
  });

  // The expression of the diamond wraps on two lines and is not cut.
  it('wraps the expression of a router on two lines', () => {
    const node = makeNode({
      key: 'steps[1]',
      label: 'route',
      kind: 'router',
      routes: ['ok'],
      routeValue: '${steps.produce.outputs.code}',
    });
    const { getByText } = renderIn(<RouterNode {...nodeProps(node)} />);
    const expression = getByText('${steps.produce.outputs.code}');
    expect(expression).toHaveClass('line-clamp-2', 'break-all');
    expect(expression).not.toHaveClass('truncate');
    expect(expression).toHaveAttribute(
      'title',
      '${steps.produce.outputs.code}'
    );
  });

  it('shows the pattern of a route that has no target, not of one with a target', () => {
    const node = makeNode({
      key: 'steps[1]',
      label: 'route',
      kind: 'router',
      routes: ['ok', 'fail'],
      routeTargets: { ok: ['steps[2]'], fail: [] },
    });
    const { getAllByTestId, queryByText } = renderIn(
      <RouterNode {...nodeProps(node)} />
    );
    expect(getAllByTestId('ve-canvas-route')).toHaveLength(1);
    expect(queryByText('fail')).not.toBeNull();
    expect(queryByText('ok')).toBeNull();
  });

  it('puts the route ports on the edges of the diamond', () => {
    const size = routerSize(3);
    // The right edges of the diamond in LR: x = size - |y - size/2|.
    for (const port of routePorts(3, size, 'LR')) {
      expect(port.x).toBeCloseTo(size - Math.abs(port.y - size / 2));
      expect(port.x).toBeGreaterThan(size / 2);
      expect(port.x).toBeLessThanOrEqual(size);
    }
    // The lower edges in TB.
    for (const port of routePorts(2, routerSize(2), 'TB')) {
      expect(port.y).toBeCloseTo(
        routerSize(2) - Math.abs(port.x - routerSize(2) / 2)
      );
      expect(port.y).toBeGreaterThan(routerSize(2) / 2);
    }
  });

  it('keeps the middle free for the default output when the count is odd', () => {
    for (const count of [1, 3, 5]) {
      const size = routerSize(count);
      for (const port of routePorts(count, size, 'LR')) {
        expect(Math.abs(port.y - size / 2)).toBeGreaterThan(1);
      }
    }
    expect(routePorts(0, 160, 'LR')).toEqual([]);
  });
});

describe('AgentNode', () => {
  const agent = (over: Partial<CanvasNode> = {}) =>
    makeNode({
      key: 'agent',
      label: 'Agent',
      kind: 'agent',
      taskCount: 3,
      ...over,
    });

  it('shows the agent with its number of tasks', () => {
    const { getByTestId, getByText } = renderIn(
      <AgentNode {...nodeProps(agent())} />
    );
    expect(getByTestId('ve-canvas-node-agent')).toHaveAttribute(
      'data-shape',
      'agent'
    );
    expect(getByText('Agent')).toBeInTheDocument();
    expect(getByText('3 tasks')).toBeInTheDocument();
  });

  it('says one task in the singular, and nothing without a count', () => {
    const one = renderIn(<AgentNode {...nodeProps(agent({ taskCount: 1 }))} />);
    expect(one.getByText('1 task')).toBeInTheDocument();
    one.unmount();
    const none = renderIn(
      <AgentNode {...nodeProps(agent({ taskCount: undefined }))} />
    );
    expect(none.queryByText(/task/)).toBeNull();
  });

  it('has an output but no input, and nothing to edit', () => {
    const { container, queryByRole } = renderIn(
      <AgentNode {...nodeProps(agent())} />
    );
    const handles = Array.from(
      container.querySelectorAll('[data-flow-handle]')
    );
    expect(handles.map((el) => el.getAttribute('data-handle-type'))).toEqual([
      'source',
    ]);
    expect(handles[0]).toHaveAttribute('data-connectable', 'false');
    expect(queryByRole('button')).toBeNull();
  });

  it('shows the state of the run of the agent', () => {
    const { getByTestId } = renderIn(
      <AgentNode
        {...nodeProps(
          agent({
            run: {
              status: NodeStatus.Running,
              statusLabel: 'running',
              retryCount: 0,
              doneCount: 0,
            },
          })
        )}
      />
    );
    expect(getByTestId('ve-canvas-node-status')).toHaveAttribute(
      'data-status',
      String(NodeStatus.Running)
    );
  });
});

describe('HandlerNode', () => {
  const handler = () =>
    makeNode({
      key: 'handler_on.failure',
      label: 'onFailure',
      lane: 'handler',
    });

  it('shows the Handler chip and no handles', () => {
    const { getByText, container } = renderIn(
      <HandlerNode {...nodeProps(handler())} />
    );
    expect(getByText('Handler')).toBeInTheDocument();
    expect(getByText('onFailure')).toBeInTheDocument();
    expect(container.querySelectorAll('[data-flow-handle]')).toHaveLength(0);
  });

  it('has a dashed border', () => {
    const { getByTestId } = renderIn(<HandlerNode {...nodeProps(handler())} />);
    expect(
      getByTestId('ve-canvas-node-handler_on.failure').className
    ).toContain('border-dashed');
  });

  it('has Open and Delete but no Run step', () => {
    const onCommand = vi.fn();
    const onDeleteStep = vi.fn();
    const { getByRole, queryByRole } = renderHot(
      <HandlerNode {...nodeProps(handler())} />,
      makeActions({ onCommand, onDeleteStep })
    );
    expect(queryByRole('button', { name: 'Run step' })).toBeNull();
    expect(getByRole('button', { name: 'Duplicate step' })).toBeInTheDocument();
    expect(getByRole('button', { name: 'Copy step' })).toBeInTheDocument();
    fireEvent.click(getByRole('button', { name: 'Open step' }));
    fireEvent.click(getByRole('button', { name: 'Delete' }));
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openInspector',
      step: 'handler_on.failure',
    });
    expect(onDeleteStep).toHaveBeenCalledWith('handler_on.failure');
  });
});

describe('nodeTypes', () => {
  it('registers one component for each node type', () => {
    expect(nodeTypes.step).toBe(StepNode);
    expect(nodeTypes.router).toBe(RouterNode);
    expect(nodeTypes.handler).toBe(HandlerNode);
    expect(nodeTypes.agent).toBe(AgentNode);
    expect(Object.keys(nodeTypes).sort()).toEqual([
      'agent',
      'handler',
      'router',
      'step',
      'tool',
    ]);
  });
});

describe('AgentNode hub', () => {
  const hubNode = (hub: CanvasNodeWithRoutes['hub'], over = {}) =>
    makeNode({
      key: 'agent',
      label: 'Agent',
      kind: 'agent',
      taskCount: hub?.tasks.length,
      ...over,
      hub,
    } as Partial<CanvasNode>);

  it('shows the model, the first line of the prompt and the limit', () => {
    const { getByTestId } = renderIn(
      <AgentNode
        {...nodeProps(
          hubNode({
            provider: 'anthropic',
            model: 'claude-opus-5',
            systemFirstLine: 'You ship a small release.',
            maxIterations: 30,
            tasks: [],
          })
        )}
      />
    );
    expect(getByTestId('ve-canvas-hub-model')).toHaveTextContent(
      'anthropic / claude-opus-5'
    );
    expect(getByTestId('ve-canvas-hub-system')).toHaveTextContent(
      'You ship a small release.'
    );
    expect(getByTestId('ve-canvas-hub-limit')).toHaveTextContent(
      'Up to 30 tool calls'
    );
  });

  it('lists the tasks with their description as a tooltip', () => {
    const { getAllByTestId, getByRole } = renderIn(
      <AgentNode
        {...nodeProps(
          hubNode({
            tasks: [
              { name: 'planned', description: 'Finished when planned.' },
              { name: 'shipped' },
            ],
          })
        )}
      />
    );
    expect(getByRole('list', { name: 'Tasks' })).toBeInTheDocument();
    const rows = getAllByTestId('ve-canvas-hub-task');
    expect(rows.map((row) => row.textContent)).toEqual(['planned', 'shipped']);
    expect(rows[0]).toHaveAttribute('title', 'planned\nFinished when planned.');
    expect(rows[1]).toHaveAttribute('title', 'shipped');
  });

  it('counts the tasks that it does not list', () => {
    const tasks = Array.from({ length: 15 }, (_, i) => ({ name: `t${i}` }));
    const { getAllByTestId, getByText } = renderIn(
      <AgentNode {...nodeProps(hubNode({ tasks }))} />
    );
    expect(getAllByTestId('ve-canvas-hub-task')).toHaveLength(12);
    expect(getByText('+3 more')).toHaveAttribute('title', 't12\nt13\nt14');
  });

  it('shows the turns of a run and which tasks are settled', () => {
    const { getByTestId, getAllByTestId, getByLabelText } = renderInRun(
      <AgentNode
        {...nodeProps(
          hubNode({
            tasks: [{ name: 'a' }, { name: 'b' }, { name: 'c' }, { name: 'd' }],
          })
        )}
      />,
      {
        iterations: 7,
        calls: {},
        // The task b is not in the run: it is still open.
        tasks: { a: 'completed', c: 'skipped', d: 'failed' },
      }
    );
    expect(getByTestId('ve-canvas-hub-iterations')).toHaveTextContent(
      'Iterations: 7'
    );
    expect(getByTestId('ve-canvas-hub-settled')).toHaveTextContent(
      '3 of 4 tasks settled'
    );
    expect(
      getAllByTestId('ve-canvas-hub-task').map((row) => row.dataset.state)
    ).toEqual(['completed', 'open', 'skipped', 'failed']);
    expect(getByLabelText('completed')).toBeInTheDocument();
    expect(getByLabelText('failed')).toBeInTheDocument();
  });

  it('shows no run facts without a run', () => {
    const { queryByTestId, getAllByTestId } = renderIn(
      <AgentNode {...nodeProps(hubNode({ tasks: [{ name: 'a' }] }))} />
    );
    expect(queryByTestId('ve-canvas-hub-iterations')).toBeNull();
    expect(queryByTestId('ve-canvas-hub-settled')).toBeNull();
    expect(getAllByTestId('ve-canvas-hub-task')[0]).not.toHaveAttribute(
      'data-state'
    );
  });
});

describe('ToolNode', () => {
  const toolNode = (tool: CanvasNodeWithRoutes['tool'], over = {}) =>
    makeNode({
      label: 'build',
      subtitle: 'dag.run',
      action: 'dag.run',
      kind: 'action',
      ...over,
      tool,
    } as Partial<CanvasNode>);

  it('shows the name, the description and an action icon', () => {
    const { getByTestId, getByText } = renderIn(
      <ToolNode
        {...nodeProps(toolNode({ description: 'Build the artifacts.' }))}
      />
    );
    const card = getByTestId('ve-canvas-node-steps[0]');
    expect(card).toHaveAttribute('data-shape', 'tool');
    expect(card.querySelector('svg')).not.toBeNull();
    expect(getByText('build')).toBeInTheDocument();
    expect(getByText('Build the artifacts.')).toBeInTheDocument();
    expect(card).toHaveAttribute('title', 'build\nBuild the artifacts.');
  });

  // The cards of the grid are 10 px apart: a toolbar beside or above a card
  // would hide its neighbour, so it sits on the card itself.
  it('carries its toolbar on the card, not over a neighbour', () => {
    const { getByTestId } = renderIn(<ToolNode {...nodeProps(toolNode({}))} />);
    const toolbar = getByTestId('ve-canvas-node-toolbar');
    expect(toolbar.className).toContain('right-1');
    expect(toolbar.className).not.toContain('left-full');
    expect(toolbar.className).not.toContain('bottom-full');
  });

  // A click on the middle of a card selects it: the toolbar that shows on
  // hover must not sit on the middle.
  it('keeps the middle of the card free of the toolbar', () => {
    const { getByTestId } = renderIn(<ToolNode {...nodeProps(toolNode({}))} />);
    const cls = getByTestId('ve-canvas-node-toolbar').className;
    expect(cls).not.toContain('top-1/2');
    expect(cls).toContain('top-0');
  });

  it('falls back to the action when there is no description', () => {
    const { getByText } = renderIn(<ToolNode {...nodeProps(toolNode({}))} />);
    expect(getByText('dag.run')).toBeInTheDocument();
  });

  it('has no handle: the agent chooses the tool', () => {
    const { container } = renderIn(<ToolNode {...nodeProps(toolNode({}))} />);
    expect(container.querySelectorAll('[data-flow-handle]')).toHaveLength(0);
  });

  it('keeps the editing buttons of a step', () => {
    const onCommand = vi.fn();
    const onDeleteStep = vi.fn();
    const { getByRole } = renderHot(
      <ToolNode {...nodeProps(toolNode({}))} />,
      makeActions({ onCommand, onDeleteStep })
    );
    fireEvent.click(getByRole('button', { name: 'Open step' }));
    fireEvent.click(getByRole('button', { name: 'Delete' }));
    expect(onCommand).toHaveBeenCalledWith({
      kind: 'openInspector',
      step: 'steps[0]',
    });
    expect(onDeleteStep).toHaveBeenCalledWith('steps[0]');
  });

  it('shows the number of calls, and dims a tool that was never called', () => {
    const named = { serverName: 'build' };
    const called = renderInRun(
      <ToolNode {...nodeProps(toolNode({}, named))} />,
      { iterations: 4, calls: { build: 3 }, tasks: {} }
    );
    expect(called.getByTestId('ve-canvas-tool-calls')).toHaveTextContent('3');
    expect(called.getByTestId('ve-canvas-node-steps[0]')).not.toHaveAttribute(
      'data-dimmed'
    );
    called.unmount();
    const never = renderInRun(
      <ToolNode {...nodeProps(toolNode({}, named))} />,
      { iterations: 4, calls: { other: 1 }, tasks: {} }
    );
    expect(never.queryByTestId('ve-canvas-tool-calls')).toBeNull();
    expect(never.getByTestId('ve-canvas-node-steps[0]')).toHaveAttribute(
      'data-dimmed',
      'true'
    );
    never.unmount();
    // Without a run nothing is dimmed and nothing is counted.
    const none = renderIn(<ToolNode {...nodeProps(toolNode({}, named))} />);
    expect(none.getByTestId('ve-canvas-node-steps[0]')).not.toHaveAttribute(
      'data-dimmed'
    );
    expect(none.queryByTestId('ve-canvas-tool-calls')).toBeNull();
  });

  it('colours the card by the last status of the tool', () => {
    const { getByTestId } = renderIn(
      <ToolNode
        {...nodeProps(
          toolNode(
            {},
            {
              run: {
                status: NodeStatus.Failed,
                statusLabel: 'failed',
                retryCount: 0,
                doneCount: 0,
              },
            }
          )
        )}
      />
    );
    expect(getByTestId('ve-canvas-node-steps[0]')).toHaveClass(
      'border-destructive'
    );
  });

  it('lights the cards that match the filter and dims the others', () => {
    const match = renderIn(
      <ToolNode
        {...nodeProps(toolNode({}), {
          data: { node: toolNode({}), filterMatch: true },
        })}
      />
    );
    const lit = match.getByTestId('ve-canvas-node-steps[0]');
    expect(lit).toHaveAttribute('data-filter-match', 'true');
    expect(lit).not.toHaveAttribute('data-dimmed');
    match.unmount();
    const miss = renderIn(
      <ToolNode
        {...nodeProps(toolNode({}), {
          data: { node: toolNode({}), filterMatch: false },
        })}
      />
    );
    // A tool that does not match the filter dims.
    expect(miss.getByTestId('ve-canvas-node-steps[0]')).toHaveAttribute(
      'data-dimmed',
      'true'
    );
  });
});

function SetZoom({ zoom }: { zoom: number }) {
  const { setViewport } = useFlow();
  useEffect(() => {
    setViewport({ x: 0, y: 0, zoom });
  }, [setViewport, zoom]);
  return null;
}

// The box of the canvas and of the node, for a layout that jsdom lacks.
function stubBoxes(canvas: DOMRect, node: DOMRect) {
  return vi
    .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
    .mockImplementation(function (this: HTMLElement) {
      if (this.hasAttribute('data-flow-root')) return canvas;
      if (this.hasAttribute('data-flow-node')) return node;
      return new DOMRect(0, 0, 0, 0);
    });
}

function renderPanned(selected: boolean) {
  let pan: (view: { x: number; y: number; zoom: number }) => void = () => {};
  function Grab() {
    pan = useFlow().setViewport;
    return null;
  }
  const view = render(
    <FlowProvider>
      <CanvasActionsContext.Provider value={makeActions()}>
        <Grab />
        <div data-flow-root="">
          <div data-flow-node="steps[0]" className="group">
            <StepNode {...nodeProps(makeNode(), { selected })} />
          </div>
        </div>
      </CanvasActionsContext.Provider>
    </FlowProvider>
  );
  return {
    pan: (v: { x: number; y: number; zoom: number }) => pan(v),
    unmount: view.unmount,
  };
}

describe('node toolbar at a zoom and at the edge', () => {
  function renderAt(zoom: number, selected = false) {
    return render(
      <FlowProvider>
        <CanvasActionsContext.Provider value={makeActions()}>
          <SetZoom zoom={zoom} />
          <div data-flow-root="">
            <div data-flow-node="steps[0]" className="group">
              <StepNode {...nodeProps(makeNode(), { selected })} />
            </div>
          </div>
        </CanvasActionsContext.Provider>
      </FlowProvider>
    );
  }
  const scaleOf = (el: HTMLElement) =>
    Number(/scale\(([\d.]+)\)/.exec(el.style.transform)?.[1]);

  // The toolbar keeps its size on screen between 60 % and 150 %.
  it.each([
    [0.3, 1 / 0.6],
    [0.6, 1 / 0.6],
    [1, 1],
    [1.5, 1 / 1.5],
    [2, 1 / 1.5],
  ])('scales the toolbar at zoom %s', (zoom, scale) => {
    const { getByTestId } = renderAt(zoom);
    expect(scaleOf(getByTestId('ve-canvas-node-toolbar'))).toBeCloseTo(
      scale,
      3
    );
  });

  // A toolbar that is on screen is placed; one that nobody sees is not
  // measured, so that a pan of a large graph forces no layout.
  it('stays above a node with room, and flips below one at the top edge', () => {
    const spy = stubBoxes(
      new DOMRect(0, 0, 800, 600),
      new DOMRect(300, 200, 160, 60)
    );
    try {
      const { getByTestId, unmount } = renderAt(1, true);
      expect(getByTestId('ve-canvas-node-toolbar')).toHaveAttribute(
        'data-side',
        'above'
      );
      unmount();
      spy.mockRestore();
      stubBoxes(new DOMRect(0, 0, 800, 600), new DOMRect(300, 6, 160, 60));
      const flipped = renderAt(1, true);
      const toolbar = flipped.getByTestId('ve-canvas-node-toolbar');
      expect(toolbar).toHaveAttribute('data-side', 'below');
      expect(toolbar.className).toContain('top-full');
      expect(toolbar.className).not.toContain('bottom-full');
    } finally {
      vi.restoreAllMocks();
    }
  });

  it('shifts left when the node ends at the right edge', () => {
    stubBoxes(new DOMRect(0, 0, 800, 600), new DOMRect(700, 200, 160, 60));
    try {
      const { getByTestId } = renderAt(1, true);
      expect(getByTestId('ve-canvas-node-toolbar').style.transform).toContain(
        'translateX(-64px)'
      );
    } finally {
      vi.restoreAllMocks();
    }
  });

  // A pan must not measure the layout for each node: only a toolbar on
  // screen follows the view.
  it('measures no layout at a pan for a node that nobody points at', () => {
    const spy = stubBoxes(
      new DOMRect(0, 0, 800, 600),
      new DOMRect(300, 200, 160, 60)
    );
    try {
      const idle = renderPanned(false);
      const calls = spy.mock.calls.length;
      act(() => idle.pan({ x: 40, y: 10, zoom: 1 }));
      expect(spy.mock.calls.length).toBe(calls);
      idle.unmount();
      const shown = renderPanned(true);
      const before = spy.mock.calls.length;
      act(() => shown.pan({ x: 40, y: 10, zoom: 1 }));
      expect(spy.mock.calls.length).toBeGreaterThan(before);
    } finally {
      vi.restoreAllMocks();
    }
  });
});
