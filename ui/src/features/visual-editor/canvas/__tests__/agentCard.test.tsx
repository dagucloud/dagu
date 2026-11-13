// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import { NodeStatus } from '@/api/v1/schema';
import { describe, expect, it, vi } from 'vitest';
import { useQuery } from '@/hooks/api';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  AGENT_CARD_PARTS,
  AGENT_CARD_WIDTH,
  agentCardHeight,
  agentChips,
  chipRows,
  readAgentFacts,
  retryLabel,
} from '../agentCard';
import { buildCanvasGraph } from '../graph';
import { CanvasActionsContext, type CanvasActions } from '../context';
import { FlowProvider, type NodeProps } from '../engine';
import { cardSize, chatPlan, nodeSize } from '../layout';
import { StepNode } from '../nodes/StepNode';
import { ToolRunContext } from '../toolRun';
import type { CanvasNodeWithRoutes, StepFlowNode } from '../types';
import { makeModel, makeNames, makeStep } from './fixtures';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));

const english = (text: string, values: Record<string, string | number> = {}) =>
  text.replace(/\{(\w+)\}/g, (_, key: string) => String(values[key] ?? ''));

const STEP = {
  action: 'agent.run',
  timeout_sec: 1800,
  retry_policy: { limit: 12, interval_sec: 300, exit_code: [75] },
  secrets: ['GIT_TOKEN', 'API_KEY'],
  output_schema: {
    type: 'object',
    properties: { verdict: { type: 'string' }, summary: { type: 'string' } },
  },
  with: {
    provider: 'claude',
    endpoint: { name: 'cle1', model: 'sonnet' },
    prompt: '\n  Fix the test.\nSecond line',
    system: 'Be careful.',
  },
};

function graphOf(value: Record<string, unknown>, name = 'code') {
  const model = makeModel([
    makeStep({
      key: 'steps[0]',
      explicitName: name,
      kind: 'action',
      action: 'agent.run',
      value,
    }),
  ]);
  return buildCanvasGraph({
    model,
    names: makeNames(model),
    issues: { byStep: {}, dag: [] },
  });
}

describe('readAgentFacts', () => {
  it('reads the retry policy, secrets, timeout, schema and first lines', () => {
    expect(readAgentFacts(STEP, 'agent')).toEqual({
      trust: 'agent',
      retry: { limit: 12, intervalSec: 300, exitCodes: [75] },
      secrets: ['GIT_TOKEN', 'API_KEY'],
      timeoutSec: 1800,
      schema: ['verdict', 'summary'],
      promptLine: 'Fix the test.',
      systemLine: 'Be careful.',
    });
  });

  it('leaves out a key of the wrong type and keeps a reference', () => {
    expect(
      readAgentFacts(
        {
          retry_policy: 'x',
          secrets: 'x',
          timeout_sec: '${T}',
          output_schema: 3,
        },
        'agent'
      )
    ).toEqual({ trust: 'agent', timeoutSec: '${T}' });
    expect(readAgentFacts({ output_schema: {} }, 'agent').schema).toEqual([]);
  });
});

describe('the chips of an agent card', () => {
  it('writes the retry policy as one short text', () => {
    const retry = { limit: 12, intervalSec: 300, exitCodes: [75] };
    expect(retryLabel(english, retry)).toBe('retry ×12 · 5m · exit 75');
    expect(retryLabel(english, retry, 3)).toBe('retry 3/12 · 5m · exit 75');
  });

  it('counts the rows that the chips need, more for more chips', () => {
    const none = { agent: { trust: 'agent' as const } };
    expect(chipRows(none, 336)).toBe(0);
    const some = {
      untrusted: true,
      agent: readAgentFacts(STEP, 'agent'),
    };
    expect(chipRows(some, 336)).toBeGreaterThanOrEqual(2);
    expect(chipRows(some, 336)).toBeLessThanOrEqual(3);
    expect(chipRows(some, 100)).toBeGreaterThan(chipRows(some, 336));
    expect(agentChips(english, some).map((chip) => chip.kind)).toEqual([
      'untrusted',
      'retry',
      'timeout',
      'secrets',
      'schema',
    ]);
  });

  it('grows the card with the rows and with a run', () => {
    expect(agentCardHeight(3, false)).toBeGreaterThan(
      agentCardHeight(1, false)
    );
    expect(agentCardHeight(1, true)).toBeGreaterThan(agentCardHeight(1, false));
  });
});

describe('an agent step in the graph', () => {
  it('has facts, and a card sized for them in both directions', () => {
    const node = graphOf(STEP).nodes[0] as CanvasNodeWithRoutes;
    expect(node.agent?.retry?.limit).toBe(12);
    expect(node.agent?.chipRows).toBeGreaterThan(0);
    const size = cardSize(node, false);
    expect(size.width).toBe(AGENT_CARD_WIDTH);
    expect(size.height).toBe(agentCardHeight(node.agent!.chipRows, false));
    // A compact card is the tile of a chat step, whatever the facts.
    expect(cardSize(node, true).width).toBeLessThan(100);
    expect(nodeSize(node, false, 'LR').width).toBe(AGENT_CARD_WIDTH);
  });

  it('keeps the card of a chat step as it was', () => {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'action',
        action: 'chat.completion',
        value: { action: 'chat.completion', with: { model: 'm', prompt: 'x' } },
      }),
    ]);
    const node = buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: { byStep: {}, dag: [] },
    }).nodes[0] as CanvasNodeWithRoutes;
    expect(node.agent).toBeUndefined();
    expect(cardSize(node, false).width).toBe(336);
  });

  it('gives the card a row for the run', () => {
    const node = graphOf(STEP).nodes[0] as CanvasNodeWithRoutes;
    const running = {
      ...node,
      run: {
        status: NodeStatus.Running,
        statusLabel: '',
        retryCount: 0,
        doneCount: 0,
      },
    };
    expect(cardSize(running, false).height).toBeGreaterThan(
      cardSize(node, false).height
    );
    expect(chatPlan(running, false, 'TB')).toBeNull();
  });
});

const appBar = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

function view(node: CanvasNodeWithRoutes, compact = false, calls = {}) {
  (useQuery as unknown as ReturnType<typeof vi.fn>).mockReturnValue({
    data: undefined,
  });
  const actions: CanvasActions = {
    direction: 'TB',
    editable: true,
    compact,
    onCommand: () => {},
    onDeleteStep: () => {},
    onInsertOnEdge: () => {},
  };
  const props: NodeProps<StepFlowNode> = {
    id: node.key,
    data: { node },
    selected: false,
    isConnectable: false,
  };
  return render(
    <AppBarContext.Provider value={appBar}>
      <FlowProvider>
        <CanvasActionsContext.Provider value={actions}>
          <ToolRunContext.Provider value={{ dagName: 'd', runId: 'r1', calls }}>
            <StepNode {...props} />
          </ToolRunContext.Provider>
        </CanvasActionsContext.Provider>
      </FlowProvider>
    </AppBarContext.Provider>
  );
}

describe('the agent card', () => {
  const base = graphOf(STEP, 'a_very_long_name_'.repeat(4))
    .nodes[0] as CanvasNodeWithRoutes;

  it('shows the whole subtitle and a chip for each fact', () => {
    const { getByTestId, queryByTestId } = view(base);
    expect(getByTestId('ve-agent-subtitle')).toHaveTextContent(
      'agent.run · claude'
    );
    expect(getByTestId('ve-agent-retry')).toHaveTextContent(
      'retry ×12 · 5m · exit 75'
    );
    expect(getByTestId('ve-agent-timeout')).toHaveTextContent('timeout 30m');
    expect(getByTestId('ve-agent-secrets')).toHaveTextContent('2 secrets');
    expect(getByTestId('ve-agent-secrets').getAttribute('title')).toContain(
      'GIT_TOKEN'
    );
    expect(getByTestId('ve-agent-schema').getAttribute('title')).toContain(
      'verdict'
    );
    // The agent step is untrusted, and shows no chip unless the YAML says so.
    expect(queryByTestId('untrusted-badge')).toBeNull();
    // The name is cut at one line, and the whole name is the tooltip.
    expect(getByTestId('ve-agent-name').getAttribute('title')).toBe(base.label);
    expect(getByTestId('ve-agent-name').className).toContain('line-clamp-1');
    expect(getByTestId('ve-agent-subtitle').getAttribute('title')).toBe(
      'agent.run · claude'
    );
  });

  it('puts the name and the subtitle as two lines beside the icon', () => {
    const { getByTestId } = view(base);
    const head = getByTestId('ve-agent-name').parentElement!.parentElement!;
    // One row: the icon, the block of the two lines, the run dot.
    const block = getByTestId('ve-agent-name').parentElement!;
    expect(block.parentElement).toBe(head);
    expect(getByTestId('ve-agent-subtitle').parentElement!.parentElement).toBe(
      block
    );
    expect(head.firstElementChild).not.toBe(block);
    expect(head.style.height).toBe(`${AGENT_CARD_PARTS.HEAD}px`);
    // No row of its own for the subtitle: the card has the header only.
    expect(AGENT_CARD_PARTS).not.toHaveProperty('META');
    expect(agentCardHeight(0, false)).toBe(
      2 * AGENT_CARD_PARTS.PAD_Y + AGENT_CARD_PARTS.HEAD
    );
  });

  it('shows the result row only with a run: retries, calls and the reason of a failure', () => {
    const idle = view(base);
    expect(idle.queryByTestId('ve-agent-run-row')).toBeNull();
    idle.unmount();
    const failed: CanvasNodeWithRoutes = {
      ...base,
      serverName: 'code',
      run: {
        status: NodeStatus.Failed,
        statusLabel: 'failed',
        retryCount: 3,
        doneCount: 0,
        error: 'exit status 75',
      },
    };
    const { getByTestId } = view(failed, false, { code: { lookup: 2 } });
    expect(getByTestId('ve-agent-result')).toHaveTextContent('exit status 75');
    expect(getByTestId('ve-agent-calls')).toHaveTextContent('2 tool calls');
    expect(getByTestId('ve-agent-retry')).toHaveTextContent('retry 3/12');
  });

  it('shows the untrusted chip of an agent step that says untrusted: true', () => {
    const marked = graphOf({ ...STEP, untrusted: true }).nodes[0];
    const { getByTestId } = view(marked as CanvasNodeWithRoutes);
    expect(getByTestId('untrusted-badge')).toBeInTheDocument();
  });

  it('tells which key sets the trust of a step', () => {
    const plain = {
      key: 'steps[1]',
      label: 'verify',
      kind: 'run',
      lane: 'main',
      issues: { errors: 0, warnings: 0 },
    } as const;
    const untrusted = view({ ...plain, untrusted: true, trust: 'marked' });
    expect(
      untrusted.getByTestId('untrusted-badge').getAttribute('title')
    ).toContain('Untrusted: the key untrusted is true.');
    untrusted.unmount();
    const trusted = view({ ...plain, trust: 'opted-out' });
    expect(trusted.getByTestId('trusted-badge').getAttribute('title')).toBe(
      'Trusted: the key untrusted is false.'
    );
  });

  it('shows a counter of the facts that a compact card cannot draw', () => {
    const { getByTestId } = view(base, true);
    expect(getByTestId('ve-node-facts')).toHaveTextContent('+4');
    expect(
      getByTestId('ve-node-facts').firstElementChild?.getAttribute('title')
    ).toContain('retry ×12');
  });
});
