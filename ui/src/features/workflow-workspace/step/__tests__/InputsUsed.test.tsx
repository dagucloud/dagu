// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  NodeStatusLabel,
  ReusedInputVia,
  Status,
} from '@/api/v1/schema';
import type { StepOutputs } from '@/features/visual-editor/contracts';
import {
  resetRunAvailability,
  RunLinksContext,
} from '@/features/visual-editor/references/RunLinks';
import { outputCache } from '@/features/visual-editor/run/outputCache';
import { node, run } from '../../data/__tests__/fixtures';
import type { ApiDAGRunDetails } from '../../contracts';
import { InputsUsed } from '../InputsUsed';

const h = vi.hoisted(() => ({
  results: {} as Record<string, { data?: StepOutputs; error?: unknown }>,
  asked: [] as [string | null, string | null][],
  select: vi.fn(),
}));

vi.mock('@/features/visual-editor/api/useStepOutputs', () => ({
  useStepOutputs: (_dag: string, runId: string | null, step: string | null) => {
    h.asked.push([runId, step]);
    return h.results[step ?? ''] ?? { data: undefined };
  },
}));
vi.mock('../../data/context', () => ({
  useWorkspaceData: () => ({ fileName: 'etl.yaml' }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const skipped = (name: string) =>
  node(name, NodeStatus.Skipped, {
    statusLabel: NodeStatusLabel.skipped,
    startedAt: '-',
    finishedAt: '-',
    doneCount: 0,
  });

function details(
  labels: string[],
  extra: Partial<ApiDAGRunDetails> = {}
): ApiDAGRunDetails {
  return run(
    'r9',
    [skipped('fetch'), skipped('extract'), node('load')],
    Status.Success,
    { labels, ...extra }
  );
}

// The source runs that the history holds, for the links of the marks.
function withLinks(held: string[], ui: ReactElement) {
  const links = {
    select: h.select,
    resolve: (id: string) =>
      held.find((r) => r.toLowerCase() === id.toLowerCase()) ?? null,
    exists: async () => false,
  };
  return render(
    <RunLinksContext.Provider value={links}>{ui}</RunLinksContext.Provider>
  );
}

const outputs = (name: string, ...pairs: [string, string][]): StepOutputs => ({
  stepName: name,
  status: NodeStatus.Skipped,
  statusLabel: NodeStatusLabel.skipped,
  outputs: pairs.map(([n, value]) => ({
    name: n,
    value,
    truncated: false,
    size: value.length,
  })),
});

beforeEach(() => {
  h.results = {};
  h.asked = [];
  h.select.mockClear();
  resetRunAvailability();
  outputCache.clear();
});

describe('InputsUsed', () => {
  it('shows nothing for a run that reused no step', () => {
    const { container } = render(
      <InputsUsed details={details(['dagu.source=editor'])} />
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('lists each reused step with its source, in the order of the run', () => {
    h.results['fetch'] = { data: outputs('fetch', ['status', '200']) };
    render(
      <InputsUsed
        details={details([], {
          reusedInputs: [
            { step: 'extract', runId: 'RunB', via: ReusedInputVia.cache },
            { step: 'fetch', runId: 'RunA', via: ReusedInputVia.run },
          ],
        })}
      />
    );
    expect(screen.getByRole('heading', { name: 'Inputs used' })).toBeVisible();
    const rows = screen.getAllByTestId(/^inputs-used-(fetch|extract)$/);
    expect(rows.map((r) => r.getAttribute('data-testid'))).toEqual([
      'inputs-used-fetch',
      'inputs-used-extract',
    ]);
    // The line above the list names the source of the first step, once.
    expect(screen.getByTestId('inputs-used-source-mark')).toHaveAttribute(
      'data-source',
      'run'
    );
    expect(screen.getByTestId('inputs-used-source')).toHaveTextContent(
      'Values from run RunA'
    );
    expect(screen.queryByTestId('inputs-used-mark-fetch')).toBeNull();
    // A row with another source keeps its own mark. A cached value names the
    // run that it was read from.
    const cached = screen.getByTestId('inputs-used-mark-extract');
    expect(cached).toHaveAttribute('data-source', 'cache');
    expect(cached).toHaveTextContent('cached from run RunB');
  });

  // The steps of one run show one mark, not one for each step.
  it('shows the mark once for steps that come from one run', () => {
    render(
      <InputsUsed
        details={details([], {
          reusedInputs: [
            { step: 'fetch', runId: 'RunA', via: ReusedInputVia.run },
            { step: 'extract', runId: 'RunA', via: ReusedInputVia.run },
          ],
        })}
      />
    );
    expect(screen.getAllByTestId('inputs-used-source')).toHaveLength(1);
    expect(screen.queryByTestId('inputs-used-mark-fetch')).toBeNull();
    expect(screen.queryByTestId('inputs-used-mark-extract')).toBeNull();
    expect(
      screen.getByTestId('inputs-used-fetch').querySelector('[data-source]')
    ).toBeNull();
  });

  it('reads the labels of a run that has no record of the server', () => {
    render(<InputsUsed details={details(['dagu.input=cache/5/extract'])} />);
    expect(screen.getByTestId('inputs-used-source-mark')).toHaveAttribute(
      'data-source',
      'cache'
    );
  });

  it('reads the outputs of the run itself, for the step that it reused', () => {
    render(<InputsUsed details={details(['dagu.input=cache/6/fetch'])} />);
    expect(h.asked).toContainEqual(['r9', 'fetch']);
  });

  it('collapses the values to the first one and opens the list with Show values', () => {
    h.results['fetch'] = {
      data: outputs('fetch', ['status', '200'], ['body', '{"items":[1,2]}']),
    };
    render(<InputsUsed details={details(['dagu.input=run/5/fetch/r0'])} />);
    const first = screen.getByTestId('inputs-used-first-fetch');
    expect(first).toHaveTextContent('status: 200');
    expect(screen.queryByText('body')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Show values' }));
    expect(screen.queryByTestId('inputs-used-first-fetch')).toBeNull();
    expect(screen.getByText('status')).toBeVisible();
    expect(screen.getByText('body')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Hide values' }));
    expect(screen.getByTestId('inputs-used-first-fetch')).toBeVisible();
  });

  it('truncates a long first value', () => {
    h.results['fetch'] = {
      data: outputs('fetch', ['text', 'x'.repeat(200)]),
    };
    render(<InputsUsed details={details(['dagu.input=cache/5/fetch'])} />);
    const first = screen.getByTestId('inputs-used-first-fetch');
    expect(first.textContent?.length).toBeLessThan(80);
    expect(first.textContent).toContain('…');
    expect(first).toHaveAttribute('title', 'x'.repeat(200));
  });

  // The run is gone or never kept the values: the entry that read them from
  // the run that the record names gives them, for display only.
  it('falls back to the cache entry of the run that the record names', () => {
    outputCache.put({ remoteNode: 'local', fileName: 'etl.yaml' }, 'fetch', {
      values: { status: '201' },
      runId: 'r0',
      capturedAt: 5_300,
      stepHash: 'h',
    });
    h.results['fetch'] = { error: new Error('404') };
    render(
      <InputsUsed
        details={details([], {
          reusedInputs: [
            { step: 'fetch', runId: 'r0', via: ReusedInputVia.run },
          ],
        })}
      />
    );
    expect(screen.getByTestId('inputs-used-first-fetch')).toHaveTextContent(
      'status: 201'
    );
  });

  it('does not take the entry of another run for the values that the record names', () => {
    outputCache.put({ remoteNode: 'local', fileName: 'etl.yaml' }, 'fetch', {
      values: { status: '201' },
      runId: 'other',
      capturedAt: 5_300,
      stepHash: 'h',
    });
    h.results['fetch'] = { error: new Error('404') };
    render(
      <InputsUsed
        details={details([], {
          reusedInputs: [
            {
              step: 'fetch',
              runId: 'r0',
              capturedAt: '1970-01-01T00:00:05Z',
              via: ReusedInputVia.run,
            },
          ],
        })}
      />
    );
    expect(screen.queryByTestId('inputs-used-first-fetch')).toBeNull();
  });

  it('shows no values toggle when nothing holds the values', () => {
    render(<InputsUsed details={details(['dagu.input=cache/5/fetch'])} />);
    expect(screen.getByTestId('inputs-used-fetch')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Show values' })).toBeNull();
  });

  it('selects a source run that the history holds', () => {
    withLinks(
      ['R0AbC'],
      <InputsUsed details={details(['dagu.input=run/5/fetch/.52.30.41b.43'])} />
    );
    fireEvent.click(screen.getByTestId('inputs-used-source-show'));
    expect(h.select).toHaveBeenCalledWith('R0AbC');
  });

  it('shows the source run that the history does not hold as plain text', () => {
    withLinks(
      [],
      <InputsUsed details={details(['dagu.input=run/5/fetch/r0'])} />
    );
    expect(screen.getByTestId('inputs-used-source')).toHaveTextContent(
      'Values from run'
    );
    expect(screen.queryByTestId('inputs-used-source-show')).toBeNull();
  });
});
