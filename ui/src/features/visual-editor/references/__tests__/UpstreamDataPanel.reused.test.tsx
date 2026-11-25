// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  NodeStatusLabel,
  ReferenceScopeStepOutputsKnown,
  ReusedInputVia,
  Status,
  type components,
} from '@/api/v1/schema';
import type {
  FollowedStepRun,
  InputRun,
  ReferenceScope,
  StepOutputs,
} from '../../contracts';
import { outputCache, type CacheEntry } from '../../run/outputCache';
import type { InputProvenance } from '../../run/inputProvenance';
import { scopeHttpJq } from '../../testing/fixtures';
import { UpstreamDataPanel, usedValues } from '../UpstreamDataPanel';

type RunNode = components['schemas']['Node'];
type HookResult = {
  data?: StepOutputs;
  error?: unknown;
  isLoading?: boolean;
};

const hook = vi.hoisted(() => ({
  results: {} as Record<string, HookResult>,
  asked: {} as Record<string, string | null>,
  followed: {
    details: null as unknown,
    active: false,
    error: null as Error | null,
  },
}));

vi.mock('@/features/visual-editor/api/useStepOutputs', () => ({
  useStepOutputs: (_dag: string, run: string | null, step: string | null) => {
    hook.asked[step ?? ''] = run;
    return (
      hook.results[step ?? ''] ?? {
        data: undefined,
        error: undefined,
        isLoading: false,
      }
    );
  },
}));
vi.mock('@/features/visual-editor/run/useFollowedRun', () => ({
  useFollowedRun: () => hook.followed,
}));
vi.mock('@/features/visual-editor/run/useCaptureStepOutputs', () => ({
  useCaptureStepOutputs: () => undefined,
  forgetCaptures: () => undefined,
}));

beforeEach(() => {
  outputCache.clear();
  hook.results = {};
  hook.asked = {};
  hook.followed = { details: null, active: false, error: null };
});

// fetch -> extract -> load; the step in the panel is load.
const scope: ReferenceScope = {
  ...scopeHttpJq,
  steps: [
    ...scopeHttpJq.steps,
    {
      name: 'load',
      id: 'load',
      stepPath: 'steps[2]',
      upstream: ['fetch', 'extract'],
      outputs: [],
      outputsKnown: ReferenceScopeStepOutputsKnown.static,
    },
  ],
};

const T0 = Date.parse('2026-10-01T12:00:00Z');
const reused = (
  step: string,
  extra: Partial<InputProvenance> = {}
): InputProvenance => ({
  step,
  source: 'run',
  runId: 'r0abcdef12',
  stale: false,
  capturedAt: T0,
  ...extra,
});

function skipped(name: string): RunNode {
  return {
    step: { name },
    status: NodeStatus.Skipped,
    statusLabel: NodeStatusLabel.skipped,
    startedAt: '-',
    finishedAt: '-',
    retryCount: 0,
    doneCount: 0,
  } as RunNode;
}

const followedRun = (
  extra: Partial<FollowedStepRun> = {}
): FollowedStepRun => ({
  kind: 'execute',
  step: 'load',
  runId: 'r9',
  steps: ['load'],
  startedAt: Date.now(),
  ...extra,
});

function renderRun(p: {
  followed?: Partial<FollowedStepRun>;
  inputRun?: Partial<InputRun>;
  labels?: string[];
  /** More details of the followed run, such as its `reusedInputs`. */
  details?: Record<string, unknown>;
  panel?: Partial<Parameters<typeof UpstreamDataPanel>[0]>;
}) {
  hook.followed = {
    details: {
      dagRunId: 'r9',
      status: Status.Success,
      labels: p.labels ?? [],
      nodes: [skipped('fetch'), skipped('extract')],
      ...p.details,
    },
    active: false,
    error: null,
  };
  return render(
    <UpstreamDataPanel
      dagName="etl"
      runId="r9"
      scope={scope}
      stepName="load"
      followedRun={followedRun(p.followed)}
      {...p.panel}
      inputRun={{
        runId: 'r9',
        stale: new Set(),
        missing: new Set(),
        waitingFor: null,
        ...p.inputRun,
      }}
    />
  );
}

const outputs = (name: string, value: string): StepOutputs => ({
  stepName: name,
  status: NodeStatus.Skipped,
  statusLabel: NodeStatusLabel.skipped,
  outputs: [{ name: 'status', value, truncated: false, size: value.length }],
});

describe('UpstreamDataPanel reused inputs of a followed run', () => {
  it('reads the outputs of the followed run for a step that it reused', () => {
    renderRun({
      followed: { reused: [reused('fetch'), reused('extract')] },
    });
    expect(hook.asked['fetch']).toBe('r9');
    expect(hook.asked['extract']).toBe('r9');
  });

  it('does not read a step that the run neither executed nor reused', () => {
    renderRun({ followed: { reused: [reused('fetch')] } });
    expect(hook.asked['fetch']).toBe('r9');
    expect(hook.asked['extract']).toBeNull();
    expect(screen.getByTestId('upstream-state-extract')).toHaveAttribute(
      'data-state',
      'skipped'
    );
  });

  it('shows the values of the run and a mark for each source, never an empty row', () => {
    hook.results['fetch'] = { data: outputs('fetch', '200') };
    hook.results['extract'] = { data: outputs('extract', '404') };
    renderRun({
      followed: {
        reused: [
          reused('fetch'),
          reused('extract', { source: 'cache', runId: 'r1abcdef34' }),
        ],
      },
    });
    const fetch = screen.getByTestId('upstream-reused-fetch');
    expect(fetch).toHaveAttribute('data-source', 'run');
    expect(fetch).toHaveTextContent('from run r0abcdef');
    expect(fetch).toHaveAttribute(
      'title',
      expect.stringMatching(/^Outputs reused from run r0abcdef, 2026-10-0/)
    );
    expect(screen.getByText('200')).toBeInTheDocument();
    // A cached value names the run that it was read from.
    const cached = screen.getByTestId('upstream-reused-extract');
    expect(cached).toHaveAttribute('data-source', 'cache');
    expect(cached).toHaveTextContent('cached from run r1abcdef');
    expect(screen.getByText('404')).toBeInTheDocument();
    // The mark takes the place of the status text.
    expect(screen.queryByTestId('upstream-state-fetch')).toBeNull();
    expect(screen.queryByTestId('upstream-state-extract')).toBeNull();
  });

  // The run is gone (a test run is dropped) or has no outputs: the values
  // that the browser read from that run are shown, read-only, with no error.
  it('shows the values that the browser read from the run when the run cannot give them', () => {
    hook.results['fetch'] = { error: new Error('404') };
    outputCache.put({ remoteNode: 'local', fileName: 'etl.yaml' }, 'fetch', {
      values: { status: '201' },
      runId: 'r0abcdef12',
      capturedAt: T0,
      stepHash: 'h',
    });
    renderRun({
      followed: { reused: [reused('fetch', { source: 'cache' })] },
      panel: { fileName: 'etl.yaml' },
    });
    expect(screen.getByText('201')).toBeInTheDocument();
    expect(screen.getByTestId('upstream-reused-fetch')).toHaveTextContent(
      /^cached from run r0abcdef/
    );
    expect(screen.queryByText(/Could not load/)).toBeNull();
  });

  it('shows the cached mark with its run and its capture time as the tooltip', () => {
    renderRun({
      followed: { reused: [reused('fetch', { source: 'cache' })] },
    });
    expect(screen.getByTestId('upstream-reused-fetch')).toHaveAttribute(
      'title',
      expect.stringMatching(/^Cached values from run r0abcdef, 2026-10-0/)
    );
  });

  it('marks a step that changed since its source run with the warning tone', () => {
    renderRun({ followed: { reused: [reused('fetch', { stale: true })] } });
    const mark = screen.getByTestId('upstream-reused-fetch');
    expect(mark).toHaveAttribute('data-stale');
    expect(mark).toHaveTextContent('changed since run r0abcdef');
    expect(mark.className).toContain('text-warning');
  });

  it('keeps the provenance after a reload, from the record of the run', () => {
    renderRun({
      details: {
        reusedInputs: [
          { step: 'fetch', runId: 'RunA', via: ReusedInputVia.run },
          { step: 'extract', runId: 'RunB', via: ReusedInputVia.cache },
        ],
      },
    });
    expect(screen.getByTestId('upstream-reused-fetch')).toHaveAttribute(
      'data-source',
      'run'
    );
    expect(screen.getByTestId('upstream-reused-extract')).toHaveAttribute(
      'data-source',
      'cache'
    );
    expect(hook.asked['fetch']).toBe('r9');
  });

  it('reads the labels of a run that was made before the record', () => {
    renderRun({
      labels: ['dagu.input=run/5/fetch/r1', 'dagu.input=cache/6/extract'],
    });
    expect(screen.getByTestId('upstream-reused-fetch')).toHaveAttribute(
      'data-source',
      'run'
    );
    expect(screen.getByTestId('upstream-reused-extract')).toHaveAttribute(
      'data-source',
      'cache'
    );
  });

  it('makes the source run a button when the history holds it', () => {
    const onSelectRun = vi.fn();
    renderRun({
      followed: { reused: [reused('fetch')] },
      inputRun: { onSelectRun, resolveRun: (id) => id.toUpperCase() },
    });
    const mark = screen.getByTestId('upstream-reused-fetch');
    fireEvent.click(within(mark).getByRole('button'));
    expect(onSelectRun).toHaveBeenCalledWith('R0ABCDEF12');
  });

  it('shows no button for a source run that the history does not hold', () => {
    renderRun({
      followed: { reused: [reused('fetch')] },
      inputRun: { onSelectRun: vi.fn(), resolveRun: () => null },
    });
    expect(
      within(screen.getByTestId('upstream-reused-fetch')).queryByRole('button')
    ).toBeNull();
  });

  it('names the cache in the header only when the run used it', () => {
    const { unmount } = renderRun({
      followed: { reused: [reused('fetch')] },
      inputRun: {
        cache: {
          cached: new Set(['fetch']),
          capturedAt: T0,
          times: {},
          runs: {},
        },
      },
    });
    const header = screen.getByTestId('upstream-input-run');
    expect(header).toHaveTextContent('Inputs from run r9');
    expect(header).not.toHaveTextContent('your cache');
    unmount();
    renderRun({
      followed: { reused: [reused('fetch', { source: 'cache' })] },
    });
    expect(screen.getByTestId('upstream-input-run')).toHaveTextContent(
      'Inputs from run r9 and your cache'
    );
  });
});

describe('UpstreamDataPanel with no value of the user', () => {
  // The values come from a run: there is no editor and no pin.
  it('has no button that sets a value, with or without a file', () => {
    hook.results['fetch'] = { data: outputs('fetch', '200') };
    renderRun({
      followed: { reused: [reused('fetch', { source: 'cache' })] },
      panel: { fileName: 'etl.yaml' },
    });
    expect(screen.queryByTestId('pin-edit-status')).toBeNull();
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(screen.queryByText(/pinned/i)).toBeNull();
  });

  it('shows the source run of a followed test run as a link', () => {
    const onSelectRun = vi.fn();
    renderRun({
      followed: { reused: [reused('fetch')] },
      details: { ephemeral: true },
      inputRun: { onSelectRun },
    });
    fireEvent.click(
      within(screen.getByTestId('upstream-input-run')).getByRole('button')
    );
    expect(onSelectRun).toHaveBeenCalledWith('r9');
  });
});

describe('UpstreamDataPanel reused inputs of the shown run', () => {
  it('marks the rows that the input run itself reused', () => {
    hook.results['fetch'] = { data: outputs('fetch', '200') };
    render(
      <UpstreamDataPanel
        dagName="etl"
        runId="r1"
        scope={scope}
        stepName="load"
        inputRun={{
          runId: 'r1',
          stale: new Set(),
          missing: new Set(),
          waitingFor: null,
          provenance: new Map([
            ['fetch', reused('fetch', { source: 'cache' })],
          ]),
        }}
      />
    );
    expect(screen.getByTestId('upstream-reused-fetch')).toHaveAttribute(
      'data-source',
      'cache'
    );
    expect(screen.getByText('200')).toBeInTheDocument();
    expect(screen.queryByTestId('upstream-reused-extract')).toBeNull();
  });
});

describe('usedValues', () => {
  const entry = (extra: Partial<CacheEntry> = {}): CacheEntry => ({
    values: { out: 'cached' },
    runId: 'r0abcdef12',
    capturedAt: T0 + 400,
    stepHash: 'h',
    ...extra,
  });

  it('gives the entry that read the values from the run that the record names', () => {
    expect(usedValues(reused('a'), entry())).toEqual({ out: 'cached' });
    expect(usedValues(reused('a', { source: 'cache' }), entry())).toEqual({
      out: 'cached',
    });
    // Another run: not the entry that the record names.
    expect(usedValues(reused('a'), entry({ runId: 'other' }))).toBeUndefined();
  });

  it('gives nothing for no record, no entry or a record with no run', () => {
    expect(usedValues(undefined, entry())).toBeUndefined();
    expect(usedValues(reused('a'), undefined)).toBeUndefined();
    expect(
      usedValues(reused('a', { runId: undefined }), entry())
    ).toBeUndefined();
  });
});
