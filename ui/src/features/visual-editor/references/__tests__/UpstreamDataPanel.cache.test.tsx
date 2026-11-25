// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  NodeStatusLabel,
  ReferenceScopeOutputSource,
  ReferenceScopeStepOutputsKnown,
} from '@/api/v1/schema';
import type { InputRun, ReferenceScope, StepOutputs } from '../../contracts';
import {
  outputCache,
  type CacheEntry,
  type CacheScope,
} from '../../run/outputCache';
import { UpstreamDataPanel } from '../UpstreamDataPanel';

const hook = vi.hoisted(() => ({
  results: {} as Record<string, { data?: StepOutputs }>,
  captures: [] as Record<string, unknown>[],
  followedDetails: null as unknown,
}));

// A result keyed `<run>/<step>` answers for that run only; one keyed by the
// step answers for any run.
vi.mock('@/features/visual-editor/api/useStepOutputs', () => ({
  useStepOutputs: (_dag: string, run: string | null, step: string | null) =>
    (run !== null &&
      (hook.results[`${run}/${step}`] ?? hook.results[step ?? ''])) || {
      data: undefined,
      error: undefined,
      isLoading: false,
    },
}));
vi.mock('@/features/visual-editor/run/useCaptureStepOutputs', () => ({
  useCaptureStepOutputs: (p: Record<string, unknown>) => {
    hook.captures.push(p);
  },
  forgetCaptures: () => undefined,
}));
vi.mock('@/features/visual-editor/run/useFollowedRun', () => ({
  useFollowedRun: () => ({
    details: hook.followedDetails,
    active: true,
    error: null,
  }),
}));

const SCOPE: CacheScope = { remoteNode: 'local', fileName: 'etl.yaml' };

const scope: ReferenceScope = {
  params: [],
  consts: [],
  env: [],
  secrets: [],
  steps: [
    {
      name: 'fetch',
      id: 'fetch',
      stepPath: 'steps[0]',
      upstream: [],
      outputsKnown: ReferenceScopeStepOutputsKnown.static,
      outputs: [
        {
          name: 'data',
          reference: '${steps.fetch.outputs.data}',
          source: ReferenceScopeOutputSource.declared,
          type: 'json',
        },
        {
          name: 'note',
          reference: '${steps.fetch.outputs.note}',
          source: ReferenceScopeOutputSource.declared,
          type: 'string',
        },
      ],
    },
    {
      name: 'use',
      id: 'use',
      stepPath: 'steps[1]',
      upstream: ['fetch'],
      outputsKnown: ReferenceScopeStepOutputsKnown.static,
      outputs: [],
    },
  ],
};

const sample: StepOutputs = {
  stepName: 'fetch',
  stepId: 'fetch',
  status: NodeStatus.Success,
  statusLabel: NodeStatusLabel.succeeded,
  outputs: [
    { name: 'data', value: '{"n":1}', truncated: false, size: 7 },
    { name: 'note', value: 'hello', truncated: false, size: 5 },
  ],
};

function input(p: Partial<InputRun> = {}): InputRun {
  return {
    runId: 'run-1',
    stale: new Set(),
    missing: new Set(),
    waitingFor: null,
    ...p,
  };
}

function renderPanel(
  props: Partial<Parameters<typeof UpstreamDataPanel>[0]> = {}
) {
  return render(
    <UpstreamDataPanel
      dagName="etl"
      fileName="etl.yaml"
      runId="run-1"
      scope={scope}
      stepName="use"
      inputRun={input()}
      {...props}
    />
  );
}

function entry(p: Partial<CacheEntry> = {}): CacheEntry {
  return {
    values: { data: '{"n":2}', note: 'cached note' },
    runId: 'run-0abcdef12',
    capturedAt: Date.parse('2026-10-01T00:00:00Z'),
    stepHash: 'h',
    ...p,
  };
}

beforeEach(() => {
  outputCache.clear();
  hook.results = { fetch: { data: sample } };
  hook.captures = [];
  hook.followedDetails = null;
});

const CAPTURED = Date.parse('2026-10-01T00:00:00Z');

describe('UpstreamDataPanel cache', () => {
  it('gives the data that it loaded to the capture, with the file name', () => {
    renderPanel();
    expect(hook.captures).toContainEqual(
      expect.objectContaining({
        fileName: 'etl.yaml',
        dagName: 'etl',
        runId: 'run-1',
        step: 'fetch',
        data: sample,
      })
    );
  });

  it('shows the cached values when no run is loaded, with the run that they were read from', () => {
    hook.results = {};
    outputCache.put(SCOPE, 'fetch', entry());
    renderPanel({
      runId: null,
      inputRun: input({
        runId: null,
        cache: {
          cached: new Set(['fetch']),
          capturedAt: CAPTURED,
          times: { fetch: CAPTURED },
          runs: { fetch: 'run-0abcdef12' },
        },
      }),
    });
    expect(screen.getByText('cached note')).toBeInTheDocument();
    expect(screen.getByTestId('upstream-cached-fetch')).toHaveTextContent(
      /^cached from run run-0abc/
    );
  });

  // The source run holds an older fetch: the next step run reads fetch from
  // the cached run, and the row shows the values of that run.
  it('shows the values of the cached run, not of the source run', () => {
    hook.results['run-0abcdef12/fetch'] = {
      data: {
        ...sample,
        outputs: [
          {
            name: 'note',
            value: 'from the cached run',
            truncated: false,
            size: 19,
          },
        ],
      },
    };
    renderPanel({
      inputRun: input({
        cache: {
          cached: new Set(['fetch']),
          capturedAt: CAPTURED,
          times: { fetch: CAPTURED },
          runs: { fetch: 'run-0abcdef12' },
        },
      }),
    });
    expect(screen.getByTestId('upstream-cached-fetch')).toHaveTextContent(
      /^cached from run run-0abc/
    );
    expect(screen.getByText('from the cached run')).toBeInTheDocument();
    expect(screen.queryByText('hello')).toBeNull();
  });

  it('makes the run of a cached value a link', () => {
    const onSelectRun = vi.fn();
    hook.results = {};
    renderPanel({
      runId: null,
      inputRun: input({
        runId: null,
        onSelectRun,
        resolveRun: (id) => id,
        cache: {
          cached: new Set(['fetch']),
          capturedAt: CAPTURED,
          times: { fetch: CAPTURED },
          runs: { fetch: 'run-0abcdef12' },
        },
      }),
    });
    fireEvent.click(
      within(screen.getByTestId('upstream-cached-fetch')).getByRole('button')
    );
    expect(onSelectRun).toHaveBeenCalledWith('run-0abcdef12');
  });

  it('has no editor and no pin: a value cannot be typed', () => {
    renderPanel();
    expect(screen.queryByTestId('pin-edit-note')).toBeNull();
    expect(screen.queryByRole('textbox')).toBeNull();
  });

  it('says where the inputs come from when there is no picker', () => {
    const cache = {
      cached: new Set(['fetch']),
      capturedAt: CAPTURED,
      times: {},
      runs: {},
    };
    const { rerender } = renderPanel({ inputRun: input() });
    expect(screen.getByTestId('upstream-input-run')).toHaveTextContent(
      /^Inputs from run run-1$/
    );
    rerender(
      <UpstreamDataPanel
        dagName="etl"
        fileName="etl.yaml"
        runId={null}
        scope={scope}
        stepName="use"
        inputRun={input({ runId: null, cache })}
      />
    );
    expect(screen.getByTestId('upstream-input-cache')).toHaveTextContent(
      /^Inputs from your cache \(/
    );
  });
});

describe('UpstreamDataPanel run picker', () => {
  const picker = (onSelect = vi.fn()) => ({
    options: [
      {
        id: 'run:run-1',
        kind: 'run' as const,
        runId: 'run-1',
        auto: true,
        startedAt: '2026-10-01T00:00:00Z',
      },
      {
        id: 'run:run-2',
        kind: 'run' as const,
        runId: 'run-2',
        startedAt: '2026-09-30T00:00:00Z',
      },
    ],
    selectedId: 'run:run-1',
    onSelect,
  });

  it('shows the picker in the header in place of the plain line', () => {
    renderPanel({ inputRun: input({ picker: picker() }) });
    const header = screen.getByTestId('upstream-input-run');
    expect(header).toHaveTextContent('Inputs from');
    expect(within(header).getByTestId('run-source-picker')).toHaveTextContent(
      'run-1'
    );
  });

  it('gives the choice to the host', async () => {
    const onSelect = vi.fn();
    renderPanel({ inputRun: input({ picker: picker(onSelect) }) });
    await userEvent.click(screen.getByTestId('run-source-picker'));
    await userEvent.click(
      await screen.findByTestId('run-source-option-run:run-2')
    );
    expect(onSelect).toHaveBeenCalledWith(
      expect.objectContaining({ runId: 'run-2' })
    );
  });

  // The picker is for the next run: a followed run keeps its own header.
  it('shows no picker for a followed run', () => {
    hook.followedDetails = { dagRunId: 'run-9', status: 'success', nodes: [] };
    renderPanel({
      followedRun: {
        kind: 'execute',
        step: 'use',
        runId: 'run-9',
        steps: ['use'],
        startedAt: Date.now(),
      },
      inputRun: input({ picker: picker() }),
    });
    expect(screen.queryByTestId('run-source-picker')).toBeNull();
  });
});
