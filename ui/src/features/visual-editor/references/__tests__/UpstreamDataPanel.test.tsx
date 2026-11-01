// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  NodeStatusLabel,
  Status,
  ReferenceScopeOutputSource,
  ReferenceScopeStepOutputsKnown,
  type components,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import type { InputRun, ReferenceScope, StepOutputs } from '../../contracts';
import { scopeHttpJq, stepOutputsSample } from '../../testing/fixtures';
import { StepLinksContext } from '../StepLinks';
import {
  OUTPUTS_RETRY_MAX,
  OUTPUTS_RETRY_MS,
  UpstreamDataPanel,
} from '../UpstreamDataPanel';

type HookResult = {
  data?: StepOutputs;
  error?: unknown;
  isLoading?: boolean;
  mutate?: () => Promise<unknown>;
};
const hook = vi.hoisted(() => ({
  results: {} as Record<string, HookResult>,
  /** The run that each step asked for, at its last render. */
  asked: {} as Record<string, string | null>,
  followed: {
    details: null as unknown,
    active: true,
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
// The capture reads the definition of the run with a request: not here.
vi.mock('@/features/visual-editor/run/useCaptureStepOutputs', () => ({
  useCaptureStepOutputs: () => undefined,
  forgetCaptures: () => undefined,
}));

beforeEach(() => {
  hook.results = {};
  hook.asked = {};
  hook.followed = { details: null, active: true, error: null };
});

// Three steps in a chain: fetch -> extract -> load.
const scopeChain: ReferenceScope = {
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

function renderPanel(
  props: Partial<Parameters<typeof UpstreamDataPanel>[0]> = {}
) {
  return render(
    <UpstreamDataPanel
      dagName="etl"
      runId={null}
      scope={scopeHttpJq}
      stepName="extract"
      {...props}
    />
  );
}

describe('UpstreamDataPanel source badges', () => {
  // The source of an output is a badge in the language of the page.
  it('shows the source in English and in Japanese', () => {
    const { unmount } = renderPanel({ stepName: 'extract' });
    expect(screen.getAllByText('action output').length).toBeGreaterThan(0);
    unmount();
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));
    try {
      render(
        <UserPreferencesProvider>
          <I18nProvider>
            <UpstreamDataPanel
              dagName="etl"
              runId={null}
              scope={scopeHttpJq}
              stepName="extract"
            />
          </I18nProvider>
        </UserPreferencesProvider>
      );
      expect(screen.getAllByText('アクションの出力').length).toBeGreaterThan(0);
      expect(screen.queryByText('action output')).toBeNull();
    } finally {
      localStorage.removeItem('user_preferences');
    }
  });
});

describe('UpstreamDataPanel run button', () => {
  const runButton = () =>
    screen.queryByRole('button', { name: 'Run upstream steps' });

  it('runs the upstream steps from the row of the heading', () => {
    const onRunUpstream = vi.fn();
    renderPanel({ stepName: 'extract', onRunUpstream });
    const button = runButton()!;
    expect(button).toHaveTextContent('Run upstream steps');
    expect(button.parentElement).toContainElement(
      screen.getByRole('heading', { name: 'Upstream data' })
    );
    fireEvent.click(button);
    expect(onRunUpstream).toHaveBeenCalledTimes(1);
  });

  it('has no button without a host callback or without upstream steps', () => {
    const { unmount } = renderPanel({ stepName: 'extract' });
    expect(runButton()).toBeNull();
    unmount();
    renderPanel({ stepName: 'fetch', onRunUpstream: vi.fn() });
    expect(runButton()).toBeNull();
  });

  it('disables the button with the reason in a tooltip', async () => {
    const onRunUpstream = vi.fn();
    renderPanel({
      stepName: 'extract',
      onRunUpstream,
      runUpstreamBlocked: 'Fix the file to run the upstream steps',
    });
    const button = runButton()!;
    expect(button).toBeDisabled();
    fireEvent.click(button);
    expect(onRunUpstream).not.toHaveBeenCalled();
    // The wrapper takes the pointer event that the disabled button cannot.
    await userEvent.hover(button.parentElement as HTMLElement);
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'Fix the file to run the upstream steps'
    );
  });
});

describe('UpstreamDataPanel scope', () => {
  it('shows a hint when no step is selected', () => {
    renderPanel({ stepName: null });
    expect(
      screen.getByText('Select a step to see the data of its upstream steps')
    ).toBeInTheDocument();
  });

  it('shows a hint without scope or for an unknown step', () => {
    const { unmount } = renderPanel({ scope: undefined });
    expect(screen.getByText('No reference data yet')).toBeInTheDocument();
    unmount();
    renderPanel({ stepName: 'ghost' });
    expect(
      screen.getByText('Save or fix the step to see its upstream data')
    ).toBeInTheDocument();
  });

  it('shows a hint for a step without ancestors', () => {
    renderPanel({ stepName: 'fetch' });
    expect(
      screen.getByText('This step has no upstream steps')
    ).toBeInTheDocument();
  });

  it('lists the params and consts of the workflow for a step without upstream', () => {
    renderPanel({
      stepName: 'fetch',
      scope: {
        ...scopeHttpJq,
        consts: [{ name: 'LIMIT', reference: '${consts.LIMIT}' }],
      },
    });
    expect(
      screen.getByText('This step has no upstream steps')
    ).toBeInTheDocument();
    expect(screen.getByText('Parameters')).toBeInTheDocument();
    expect(screen.getByText('API_URL')).toBeInTheDocument();
    expect(
      screen.getByText('= https://api.example.com/items')
    ).toBeInTheDocument();
    expect(screen.getByText('Constants')).toBeInTheDocument();
    expect(screen.getByText('LIMIT')).toBeInTheDocument();
    expect(screen.queryByText('No run selected')).not.toBeInTheDocument();
  });

  it('lists declared outputs of a direct upstream step', () => {
    renderPanel();
    expect(screen.getByText('Upstream data')).toBeInTheDocument();
    expect(screen.getByText('Direct upstream')).toBeInTheDocument();
    expect(screen.getByText('fetch')).toBeInTheDocument();
    expect(screen.getByText('body')).toBeInTheDocument();
    expect(screen.getByText('integer')).toBeInTheDocument();
  });

  it('lists the direct dependency, with the one before it nested under it', () => {
    renderPanel({ scope: scopeChain, stepName: 'load' });
    const headers = screen.getAllByRole('heading', { level: 4 });
    expect(headers.map((h) => h.textContent)).toEqual(['Direct upstream']);
    // extract is a direct dependency of load; fetch is a dependency of
    // extract, so it is nested under extract and not listed beside it.
    const extract = screen.getByTestId('upstream-item-extract');
    const below = within(extract).getByTestId('upstream-below-extract');
    expect(
      within(below).getByTestId('upstream-item-fetch')
    ).toBeInTheDocument();
    expect(
      screen.getAllByTestId(/^upstream-item-/).map((el) => el.dataset.testid)
    ).toEqual(['upstream-item-extract', 'upstream-item-fetch']);
  });

  it('shows the id when it differs from the name', () => {
    renderPanel({ scope: scopeChain, stepName: 'load' });
    expect(screen.getByText('(names)')).toBeInTheDocument();
    expect(screen.queryByText('(fetch)')).not.toBeInTheDocument();
  });
});

describe('UpstreamDataPanel input run', () => {
  const RUN = 'abcdef0123456789';
  const input = (over: Partial<InputRun> = {}): InputRun => ({
    runId: RUN,
    stale: new Set(),
    missing: new Set(),
    waitingFor: null,
    status: Status.Success,
    startedAt: new Date(Date.now() - 180_000).toISOString(),
    ...over,
  });

  it('shows the status, the short id and the age of the run', () => {
    renderPanel({
      scope: scopeChain,
      stepName: 'load',
      runId: RUN,
      inputRun: input(),
    });
    const header = screen.getByTestId('upstream-input-run');
    expect(header).toHaveAttribute('data-run-id', RUN);
    expect(header).toHaveTextContent('Inputs from run abcdef01');
    expect(header).toHaveTextContent('3m ago');
    expect(within(header).getByTestId('status-dot')).toHaveAttribute(
      'data-status',
      String(Status.Success)
    );
    // The plain line is for the panel without a host.
    expect(screen.queryByText(`Run ${RUN}`)).toBeNull();
  });

  it('selects the run when the id is clicked', () => {
    const onSelectRun = vi.fn();
    renderPanel({
      scope: scopeChain,
      stepName: 'load',
      runId: RUN,
      inputRun: input({ onSelectRun }),
    });
    fireEvent.click(screen.getByRole('button', { name: 'abcdef01' }));
    expect(onSelectRun).toHaveBeenCalledWith(RUN);
  });

  it('marks a step that changed since the run and one with no output', () => {
    renderPanel({
      scope: scopeChain,
      stepName: 'load',
      runId: RUN,
      inputRun: input({
        stale: new Set(['extract']),
        missing: new Set(['fetch']),
      }),
    });
    expect(screen.getByTestId('upstream-mark-extract')).toHaveTextContent(
      'changed since run abcdef01'
    );
    expect(screen.getByTestId('upstream-mark-fetch')).toHaveTextContent(
      'no output in run abcdef01'
    );
  });

  it('has no mark for a step that did not change', () => {
    renderPanel({
      scope: scopeChain,
      stepName: 'load',
      runId: RUN,
      inputRun: input(),
    });
    expect(screen.queryByTestId('upstream-mark-extract')).toBeNull();
    expect(screen.queryByTestId('upstream-mark-fetch')).toBeNull();
  });

  it('names the active run that the step waits for when no run feeds it', () => {
    renderPanel({
      scope: scopeChain,
      stepName: 'load',
      runId: null,
      inputRun: input({ runId: null, waitingFor: 'run-8abcdef' }),
    });
    expect(screen.getByTestId('upstream-input-waiting')).toHaveTextContent(
      'Waits for run run-8abc to finish'
    );
    expect(screen.queryByTestId('upstream-input-run')).toBeNull();
  });
});

describe('UpstreamDataPanel run values', () => {
  it('shows the run id and the last-run values', () => {
    hook.results.fetch = { data: stepOutputsSample };
    renderPanel({ runId: 'run-1' });
    expect(screen.getByText('Run run-1')).toBeInTheDocument();
    expect(screen.getByText('Succeeded')).toBeInTheDocument();
    expect(screen.getByText('200')).toBeInTheDocument();
  });

  it('shows declared outputs without values when no run is selected', () => {
    hook.results.fetch = { data: stepOutputsSample };
    renderPanel({ runId: null });
    expect(screen.queryByText('No run selected')).not.toBeInTheDocument();
    expect(screen.queryByText(/^Run /)).not.toBeInTheDocument();
    expect(screen.getByText('body')).toBeInTheDocument();
    expect(screen.queryByText('200')).not.toBeInTheDocument();
    expect(screen.queryByText('Not in this run')).not.toBeInTheDocument();
  });

  it('marks a declared output that the run does not hold', () => {
    hook.results.fetch = {
      data: {
        ...stepOutputsSample,
        outputs: stepOutputsSample.outputs.slice(0, 1),
      },
    };
    renderPanel({ runId: 'run-1' });
    expect(screen.getByText('Not in this run')).toBeInTheDocument();
  });

  it('shows run values that the scope does not list', () => {
    hook.results.fetch = {
      data: {
        ...stepOutputsSample,
        outputs: [{ name: 'extra', value: 'zz', truncated: false, size: 2 }],
        outputVariable: {
          name: 'RESULT',
          value: 'rv',
          truncated: false,
          size: 2,
        },
      },
    };
    renderPanel({ runId: 'run-1' });
    expect(screen.getByText('extra')).toBeInTheDocument();
    expect(screen.getByText('zz')).toBeInTheDocument();
    expect(screen.getByText('RESULT')).toBeInTheDocument();
    expect(screen.getByText('rv')).toBeInTheDocument();
  });

  // ${RESULT} and ${steps.fetch.outputs.RESULT} can hold different values.
  it('shows an output variable apart from a named output of the same name', () => {
    const scope: ReferenceScope = {
      ...scopeHttpJq,
      steps: [
        {
          name: 'fetch',
          id: 'fetch',
          stepPath: 'steps[0]',
          upstream: [],
          outputsKnown: ReferenceScopeStepOutputsKnown.static,
          outputs: [
            {
              name: 'RESULT',
              reference: '${steps.fetch.outputs.RESULT}',
              source: ReferenceScopeOutputSource.declared,
            },
            {
              name: 'RESULT',
              reference: '${RESULT}',
              source: ReferenceScopeOutputSource.outputVariable,
            },
          ],
        },
        { ...scopeHttpJq.steps[1]!, upstream: ['fetch'] },
      ],
    };
    hook.results.fetch = {
      data: {
        ...stepOutputsSample,
        outputs: [
          { name: 'RESULT', value: 'named', truncated: false, size: 5 },
        ],
        outputVariable: {
          name: 'RESULT',
          value: 'variable',
          truncated: false,
          size: 8,
        },
      },
    };
    renderPanel({ scope, runId: 'run-1' });
    expect(screen.getByText('named')).toBeInTheDocument();
    expect(screen.getByText('variable')).toBeInTheDocument();
  });

  it('shows a note on a request error and keeps the declared outputs', () => {
    hook.results.fetch = { error: new Error('404') };
    renderPanel({ runId: 'run-1' });
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('Could not load the outputs of this step');
    expect(alert).toHaveClass('text-destructive');
    expect(screen.queryByText('No outputs in this run')).toBeNull();
    expect(screen.getByText('body')).toBeInTheDocument();
  });

  it('says that a run holds no outputs of a step that has none', () => {
    const scope: ReferenceScope = {
      ...scopeHttpJq,
      steps: [
        {
          name: 'load',
          id: 'load',
          stepPath: 'steps[0]',
          upstream: [],
          outputs: [],
          outputsKnown: ReferenceScopeStepOutputsKnown.static,
        },
        {
          name: 'after',
          id: 'after',
          stepPath: 'steps[1]',
          upstream: ['load'],
          outputs: [],
          outputsKnown: ReferenceScopeStepOutputsKnown.static,
        },
      ],
    };
    hook.results.load = {
      data: { ...stepOutputsSample, outputs: [], outputVariable: undefined },
    };
    renderPanel({ scope, stepName: 'after', runId: 'run-1' });
    expect(screen.getByText('No outputs in this run')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows a loading text without hiding the outputs', () => {
    hook.results.fetch = { isLoading: true };
    renderPanel({ runId: 'run-1' });
    expect(screen.getByText('Loading outputs...')).toBeInTheDocument();
    expect(screen.getByText('body')).toBeInTheDocument();
  });
});

describe('UpstreamDataPanel step status', () => {
  // The text comes from the enum, not from the raw API label.
  it('shows the status of a step as text, not as the API label', () => {
    hook.results.fetch = {
      data: {
        ...stepOutputsSample,
        status: NodeStatus.PartialSuccess,
        statusLabel: NodeStatusLabel.partially_succeeded,
      },
    };
    renderPanel({ runId: 'run-1' });
    expect(screen.getByText('Partially succeeded')).toBeInTheDocument();
    expect(screen.queryByText('partially_succeeded')).toBeNull();
  });
});

describe('UpstreamDataPanel truncated values', () => {
  it('marks a cut value with its original size', () => {
    hook.results.fetch = { data: stepOutputsSample };
    renderPanel({ runId: 'run-1' });
    expect(screen.getByText('truncated (128.0 KiB)')).toBeInTheDocument();
    expect(screen.getAllByText(/truncated/)).toHaveLength(1);
    expect(screen.getByText('truncated (128.0 KiB)')).toHaveClass(
      'text-warning'
    );
  });

  it('formats sizes in B, KiB and MiB', () => {
    const cut = (name: string, size: number) => ({
      name,
      value: 'v',
      truncated: true,
      size,
    });
    hook.results.fetch = {
      data: {
        ...stepOutputsSample,
        outputs: [cut('body', 900), cut('status', 1536)],
        outputVariable: cut('RESULT', 5 * 1024 * 1024),
      },
    };
    renderPanel({ runId: 'run-1' });
    // The same formatter as the step facts: one decimal.
    expect(screen.getByText('truncated (900 B)')).toBeInTheDocument();
    expect(screen.getByText('truncated (1.5 KiB)')).toBeInTheDocument();
    expect(screen.getByText('truncated (5.0 MiB)')).toBeInTheDocument();
  });
});

describe('UpstreamDataPanel JSON values', () => {
  it('shows a JSON object or array value prettified', () => {
    hook.results.fetch = { data: stepOutputsSample };
    renderPanel({ runId: 'run-1' });
    const blocks = screen.getAllByTestId('json-value');
    // The outputs list in the order of the scope: body, then status.
    expect(blocks.map((b) => b.getAttribute('data-json'))).toEqual([
      'true',
      'false',
    ]);
    expect(blocks[0]!.querySelector('pre')!.textContent).toBe(
      '[\n  {\n    "name": "alpha"\n  },\n  {\n    "name": "beta"\n  }\n]'
    );
  });

  it('keeps a plain or invalid JSON value as it is', () => {
    hook.results.fetch = {
      data: {
        ...stepOutputsSample,
        outputs: [
          { name: 'status', value: '200', truncated: false, size: 3 },
          { name: 'body', value: '{"a": 1', truncated: true, size: 70000 },
        ],
      },
    };
    renderPanel({ runId: 'run-1' });
    for (const block of screen.getAllByTestId('json-value')) {
      expect(block).toHaveAttribute('data-json', 'false');
    }
    expect(screen.getByText('{"a": 1')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Raw' })).toBeNull();
  });
});

// A runtime step: its outputs are known only from a run.
const scopeRuntime: ReferenceScope = {
  ...scopeHttpJq,
  steps: [
    {
      name: 'rt',
      id: 'rt',
      stepPath: 'steps[0]',
      upstream: [],
      outputs: [],
      outputsKnown: ReferenceScopeStepOutputsKnown.runtime,
    },
    {
      name: 'use',
      stepPath: 'steps[1]',
      upstream: ['rt'],
      outputs: [],
      outputsKnown: ReferenceScopeStepOutputsKnown.static,
    },
  ],
};

const runValue = (name: string) => ({
  name,
  value: 'v',
  truncated: false,
  size: 1,
});

// parity:upstream-insert-into-field
describe('UpstreamDataPanel long chains', () => {
  // Step s{n} depends on every step before it: the listed ancestors are the
  // closure, and the direct dependency is only the step just before.
  function chain(count: number): ReferenceScope {
    const steps = Array.from({ length: count }, (_, i) => ({
      name: `s${i}`,
      id: `s${i}`,
      stepPath: `steps[${i}]`,
      upstream:
        i === 0
          ? []
          : [`s${i - 1}`, ...Array.from({ length: i - 1 }, (_, j) => `s${j}`)],
      outputs: [],
      outputsKnown: ReferenceScopeStepOutputsKnown.static,
    }));
    return { ...scopeHttpJq, steps };
  }

  it('nests each step under the one that needs it, closed below the first', () => {
    renderPanel({ scope: chain(5), stepName: 's4' });
    // s3 is direct; s2, s1 and s0 are nested one under the other.
    expect(
      screen.getAllByTestId(/^upstream-item-/).map((el) => el.dataset.testid)
    ).toEqual([
      'upstream-item-s3',
      'upstream-item-s2',
      'upstream-item-s1',
      'upstream-item-s0',
    ]);
    expect(screen.getAllByRole('heading', { level: 4 })).toHaveLength(1);
    expect(screen.queryByText(/more levels?$/)).not.toBeInTheDocument();
    // The steps under s2 are in a closed item: only s2 shows.
    expect(screen.getByTestId('upstream-below-s3')).toBeVisible();
    expect(screen.getByTestId('upstream-below-s2')).not.toBeVisible();
  });

  it('says that an upstream step declares no outputs', () => {
    renderPanel({ scope: chain(3), stepName: 's2' });
    // s1 is direct and shows its note; s0 is closed.
    expect(screen.getAllByText('No declared outputs').length).toBe(2);
    expect(
      screen.getAllByText('No declared outputs').filter(isVisible)
    ).toHaveLength(1);
  });

  it('has no chevron on a short chain with one direct dependency', () => {
    renderPanel({ scope: scopeHttpJq, stepName: 'extract' });
    expect(screen.queryByRole('button', { name: /^Outputs of/ })).toBeNull();
    expect(screen.getByText('body')).toBeVisible();
  });
});

/** True when no ancestor of the element is hidden. */
function isVisible(el: Element): boolean {
  for (let node: Element | null = el; node; node = node.parentElement) {
    if (node.hasAttribute('hidden')) return false;
  }
  return true;
}

describe('UpstreamDataPanel actions', () => {
  // No field can take the token: it goes to the clipboard, and the name shows a Check.
  it('copies the reference and swaps the name to a Check when no field takes it', async () => {
    hook.results.fetch = { data: stepOutputsSample };
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    const onInsert = vi.fn(() => false);
    renderPanel({ runId: 'run-1', onInsert });

    fireEvent.click(screen.getByRole('button', { name: 'body' }));

    expect(writeText).toHaveBeenCalledWith('${steps.fetch.outputs.body}');
    const checked = await screen.findByRole('button', { name: 'Copied' });
    expect(checked.querySelector('svg')).not.toBeNull();
    // No floating line: the panel has no status of its own for a copy.
    expect(screen.queryByText(/^Copied/, { selector: 'p' })).toBeNull();
  });

  it('shows no Check when a click reaches a field', async () => {
    hook.results.fetch = { data: stepOutputsSample };
    renderPanel({ runId: 'run-1', onInsert: () => true });
    fireEvent.click(screen.getByRole('button', { name: 'body' }));
    await act(async () => {});
    expect(screen.queryByRole('button', { name: 'Copied' })).toBeNull();
  });

  it('gives the Check back to the name after 2 s', async () => {
    vi.useFakeTimers();
    try {
      hook.results.fetch = { data: stepOutputsSample };
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText: vi.fn(() => Promise.resolve()) },
      });
      renderPanel({ runId: 'run-1', onInsert: () => false });
      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: 'body' }));
      });
      expect(
        screen.getByRole('button', { name: 'Copied' })
      ).toBeInTheDocument();
      act(() => {
        vi.advanceTimersByTime(2100);
      });
      expect(screen.getByRole('button', { name: 'body' })).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('inserts the reference of a param or a const', () => {
    const onInsert = vi.fn();
    renderPanel({ stepName: 'fetch', onInsert });
    fireEvent.click(screen.getByRole('button', { name: 'API_URL' }));
    expect(onInsert).toHaveBeenCalledWith('${params.API_URL}');
    expect(onInsert).toHaveBeenCalledTimes(1);
    expect(
      screen.queryByRole('button', { name: 'Insert' })
    ).not.toBeInTheDocument();
  });

  // A text field takes the plain text of a drop at the point of the drop.
  it('drags the reference of an output as plain text', () => {
    renderPanel();
    const chip = screen.getByRole('button', { name: 'status' });
    expect(chip).toHaveAttribute('draggable', 'true');

    const data: Record<string, string> = {};
    const dataTransfer = {
      setData: (type: string, value: string) => (data[type] = value),
      effectAllowed: '',
    };
    fireEvent.dragStart(chip, { dataTransfer });

    expect(data['text/plain']).toBe('${steps.fetch.outputs.status}');
    expect(dataTransfer.effectAllowed).toBe('copy');
  });

  it('inserts the reference when the name of an output is clicked', () => {
    const onInsert = vi.fn();
    renderPanel({ onInsert });
    fireEvent.click(screen.getByRole('button', { name: 'status' }));
    expect(onInsert).toHaveBeenCalledWith('${steps.fetch.outputs.status}');
  });

  it('copies the reference when the name is clicked without onInsert', () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
    renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'body' }));
    expect(writeText).toHaveBeenCalledWith('${steps.fetch.outputs.body}');
  });

  // The rows have no Copy and no Insert button: the name does both.
  it('has no Copy and no Insert button on a row', () => {
    hook.results.fetch = { data: stepOutputsSample };
    for (const onInsert of [undefined, vi.fn()]) {
      const { unmount } = renderPanel({ runId: 'run-1', onInsert });
      expect(
        screen.queryByRole('button', { name: 'Insert' })
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: 'Copy reference' })
      ).not.toBeInTheDocument();
      unmount();
    }
  });

  it('does not fail when the clipboard is missing', () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: undefined,
      configurable: true,
    });
    renderPanel();
    expect(() =>
      fireEvent.click(screen.getByRole('button', { name: 'body' }))
    ).not.toThrow();
  });

  it('builds the reference of a run value of a runtime step', () => {
    hook.results.rt = {
      data: { ...stepOutputsSample, outputs: [runValue('out_1')] },
    };
    const onInsert = vi.fn();
    renderPanel({
      scope: scopeRuntime,
      stepName: 'use',
      runId: 'run-1',
      onInsert,
    });
    fireEvent.click(screen.getByRole('button', { name: 'out_1' }));
    expect(onInsert).toHaveBeenCalledWith('${steps.rt.outputs.out_1}');
  });

  it('gives no action for a run value with an invalid name', () => {
    hook.results.rt = {
      data: { ...stepOutputsSample, outputs: [runValue('bad-name')] },
    };
    renderPanel({
      scope: scopeRuntime,
      stepName: 'use',
      runId: 'run-1',
      onInsert: vi.fn(),
    });
    expect(screen.getByText('bad-name')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Insert' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Copy reference' })
    ).not.toBeInTheDocument();
  });

  it('gives no action for the output variable value', () => {
    hook.results.rt = {
      data: {
        ...stepOutputsSample,
        outputs: [],
        outputVariable: runValue('RESULT'),
      },
    };
    renderPanel({
      scope: scopeRuntime,
      stepName: 'use',
      runId: 'run-1',
      onInsert: vi.fn(),
    });
    expect(screen.getByText('RESULT')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Insert' })
    ).not.toBeInTheDocument();
  });
});

describe('UpstreamDataPanel steps without an id', () => {
  const scopeNoId: ReferenceScope = {
    ...scopeHttpJq,
    steps: [
      { ...scopeHttpJq.steps[0]!, id: undefined },
      { ...scopeHttpJq.steps[1]! },
    ],
  };

  it('hints to add an id and gives no reference action', () => {
    hook.results.fetch = { data: stepOutputsSample };
    renderPanel({ scope: scopeNoId, runId: 'run-1', onInsert: vi.fn() });
    expect(
      screen.getByText('Add an id to this step to reference its outputs')
    ).toBeInTheDocument();
    expect(screen.getByText('200')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Insert' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Copy reference' })
    ).not.toBeInTheDocument();
  });

  it('shows no hint for a step with an id', () => {
    renderPanel();
    expect(
      screen.queryByText('Add an id to this step to reference its outputs')
    ).not.toBeInTheDocument();
  });
});

// parity:upstream-data-panel
describe('UpstreamDataPanel layout', () => {
  function root(): HTMLElement {
    return screen.getByText('Upstream data').closest('[data-layout]')!;
  }

  it('keeps the bottom layout by default', () => {
    renderPanel();
    expect(root()).toHaveAttribute('data-layout', 'bottom');
    expect(root()).not.toHaveClass('h-full');
    expect(root()).not.toHaveClass('overflow-y-auto');
  });

  it('fills a column of its own with the column layout', () => {
    renderPanel({ layout: 'column' });
    expect(root()).toHaveAttribute('data-layout', 'column');
    expect(root()).toHaveClass('h-full', 'flex-1', 'overflow-y-auto');
    expect(screen.getByText('fetch')).toBeInTheDocument();
  });
});

describe('UpstreamDataPanel level headings', () => {
  // Readable: 12 px, in normal case.
  it('shows the level heading at 12px in normal case', () => {
    renderPanel({ scope: scopeChain, stepName: 'load' });
    const heading = screen.getByText('Direct upstream');
    expect(heading).toHaveClass('text-xs');
    expect(heading).not.toHaveClass('uppercase');
    expect(heading.className).not.toContain('text-[11px]');
  });
});

type RunNode = components['schemas']['Node'];

function runNode(name: string, status: NodeStatus, error?: string): RunNode {
  return {
    step: { name },
    status,
    statusLabel: NodeStatusLabel.succeeded,
    error,
    startedAt: '2026-10-01T00:00:00Z',
    finishedAt: '2026-10-01T00:00:05Z',
    retryCount: 0,
    doneCount: 0,
  } as RunNode;
}

function followRun(nodes: RunNode[] | null, active: boolean) {
  hook.followed = {
    details: nodes ? { dagRunId: 'r9', nodes } : null,
    active,
    error: null,
  };
}

const FOLLOWED = {
  kind: 'upstream',
  step: 'load',
  runId: 'r9',
  steps: ['fetch', 'extract'],
  startedAt: Date.now(),
} as const;

const stateOf = (name: string) =>
  screen.getByTestId(`upstream-state-${name}`).getAttribute('data-state');

// The panel lists the nearest step first. The run goes from the farthest
// step to the nearest one: the rows light up from the bottom to the top.
describe('UpstreamDataPanel followed run', () => {
  const renderFollowed = (
    props: Partial<Parameters<typeof UpstreamDataPanel>[0]> = {}
  ) =>
    renderPanel({
      scope: scopeChain,
      stepName: 'load',
      followedRun: FOLLOWED,
      ...props,
    });

  it('lists the steps as waiting before the run has started a step', () => {
    followRun(null, true);
    renderFollowed();
    expect(stateOf('extract')).toBe('waiting');
    expect(stateOf('fetch')).toBe('waiting');
    expect(screen.getByTestId('upstream-state-fetch')).toHaveTextContent(
      'Waiting'
    );
  });

  it('shows a loader on the step that runs and keeps the others waiting', () => {
    followRun(
      [
        runNode('fetch', NodeStatus.Running),
        runNode('extract', NodeStatus.NotStarted),
      ],
      true
    );
    renderFollowed();
    expect(stateOf('fetch')).toBe('running');
    expect(
      screen.getByTestId('upstream-state-fetch').querySelector('.animate-spin')
    ).not.toBeNull();
    expect(stateOf('extract')).toBe('waiting');
    expect(
      screen
        .getByTestId('upstream-state-extract')
        .querySelector('.animate-spin')
    ).toBeNull();
  });

  it('runs from the bottom of the list upward', () => {
    const first = renderFollowed();
    // The nearest step is on top, the farthest at the bottom.
    const rows = screen
      .getAllByTestId(/^upstream-state-/)
      .map((el) => el.getAttribute('data-testid'));
    expect(rows).toEqual(['upstream-state-extract', 'upstream-state-fetch']);
    first.unmount();

    // The farthest step finishes first, then the next one starts.
    followRun(
      [
        runNode('fetch', NodeStatus.Success),
        runNode('extract', NodeStatus.Running),
      ],
      true
    );
    renderFollowed();
    expect(stateOf('fetch')).toBe('success');
    expect(stateOf('extract')).toBe('running');
  });

  it('shows the real outputs of a step that succeeded, from the followed run', () => {
    hook.results.fetch = { data: stepOutputsSample };
    followRun(
      [
        runNode('fetch', NodeStatus.Success),
        runNode('extract', NodeStatus.Running),
      ],
      true
    );
    renderFollowed({ onInsert: vi.fn() });
    expect(stateOf('fetch')).toBe('success');
    expect(hook.asked.fetch).toBe('r9');
    expect(screen.getByText('200')).toBeInTheDocument();
    expect(screen.getAllByText('"alpha"').length).toBeGreaterThan(0);
    expect(
      screen.queryByRole('button', { name: 'Copy reference' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Insert' })
    ).not.toBeInTheDocument();
    // The running step has no output yet: it asks for none.
    expect(hook.asked.extract).toBeNull();
  });

  // The live state of the run shows a step done before the stored state of
  // the run does. The first step to finish gets a read with no outputs, and
  // the answer must not stay: the step that finishes last shows its data.
  describe('a read older than the run', () => {
    afterEach(() => vi.useRealTimers());

    const bothDone = () =>
      followRun(
        [
          runNode('fetch', NodeStatus.Success),
          runNode('extract', NodeStatus.Success),
        ],
        true
      );
    const staleFetch: StepOutputs = {
      stepName: 'fetch',
      status: NodeStatus.Running,
      statusLabel: NodeStatusLabel.running,
      outputs: [],
    };

    it('reads the outputs again until the step holds them', async () => {
      vi.useFakeTimers();
      const mutate = vi.fn(async () => {
        hook.results.fetch = { data: stepOutputsSample, mutate };
      });
      hook.results.fetch = { data: staleFetch, mutate };
      bothDone();
      renderFollowed();
      expect(screen.queryByText('200')).toBeNull();
      expect(mutate).not.toHaveBeenCalled();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(OUTPUTS_RETRY_MS);
      });
      expect(mutate).toHaveBeenCalledTimes(1);
      expect(screen.getByText('200')).toBeInTheDocument();

      // The step holds its outputs: no more reads.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(OUTPUTS_RETRY_MS * 5);
      });
      expect(mutate).toHaveBeenCalledTimes(1);
    });

    it('reads again when a finished step shows no declared output', async () => {
      vi.useFakeTimers();
      const mutate = vi.fn(async () => undefined);
      hook.results.fetch = {
        data: { ...staleFetch, status: NodeStatus.Success, outputs: [] },
        mutate,
      };
      bothDone();
      renderFollowed();
      for (let i = 0; i < 3; i++) {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(OUTPUTS_RETRY_MS);
        });
      }
      expect(mutate).toHaveBeenCalledTimes(3);
    });

    it('stops after a bounded number of reads', async () => {
      vi.useFakeTimers();
      const mutate = vi.fn(async () => undefined);
      hook.results.fetch = { data: staleFetch, mutate };
      bothDone();
      renderFollowed();
      for (let i = 0; i < OUTPUTS_RETRY_MAX + 5; i++) {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(OUTPUTS_RETRY_MS);
        });
      }
      expect(mutate).toHaveBeenCalledTimes(OUTPUTS_RETRY_MAX);
    });

    it('reads once more when the run ends', () => {
      const mutate = vi.fn(async () => undefined);
      hook.results.fetch = { data: stepOutputsSample, mutate };
      bothDone();
      const view = renderFollowed();
      expect(mutate).not.toHaveBeenCalled();
      followRun(
        [
          runNode('fetch', NodeStatus.Success),
          runNode('extract', NodeStatus.Success),
        ],
        false
      );
      view.rerender(
        <UpstreamDataPanel
          dagName="etl"
          runId={null}
          scope={scopeChain}
          stepName="load"
          followedRun={FOLLOWED}
        />
      );
      expect(mutate).toHaveBeenCalledTimes(1);
    });

    it('does not read a step again that holds its outputs', async () => {
      vi.useFakeTimers();
      const mutate = vi.fn(async () => undefined);
      hook.results.fetch = { data: stepOutputsSample, mutate };
      bothDone();
      renderFollowed();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(OUTPUTS_RETRY_MS * 5);
      });
      expect(mutate).not.toHaveBeenCalled();
    });
  });

  it('shows the error of a step that failed', () => {
    followRun(
      [
        runNode('fetch', NodeStatus.Success),
        runNode('extract', NodeStatus.Failed, 'exit status 2'),
      ],
      false
    );
    renderFollowed();
    expect(stateOf('extract')).toBe('failed');
    expect(screen.getByTestId('upstream-error-extract')).toHaveTextContent(
      'exit status 2'
    );
  });

  it('says that a step did not run when the run ended before it', () => {
    followRun(
      [
        runNode('fetch', NodeStatus.Failed, 'boom'),
        runNode('extract', NodeStatus.NotStarted),
      ],
      false
    );
    renderFollowed();
    expect(stateOf('fetch')).toBe('failed');
    expect(stateOf('extract')).toBe('notRun');
  });

  it('keeps the data after the run ends, with its age and a Run again button', () => {
    hook.results.fetch = { data: stepOutputsSample };
    followRun(
      [
        runNode('fetch', NodeStatus.Success),
        runNode('extract', NodeStatus.Success),
      ],
      false
    );
    const onRunUpstream = vi.fn();
    // The run started three minutes ago.
    renderFollowed({
      onRunUpstream,
      followedRun: { ...FOLLOWED, startedAt: Date.now() - 180_000 },
    });
    expect(screen.getByTestId('upstream-last-run')).toHaveTextContent(
      'Last run 3m ago'
    );
    expect(screen.getByText('200')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Run again' }));
    expect(onRunUpstream).toHaveBeenCalledTimes(1);
  });

  it('disables Run again with the reason when the run is not possible', () => {
    renderFollowed({ onRunUpstream: vi.fn(), runUpstreamBlocked: 'Fix it' });
    expect(screen.getByRole('button', { name: 'Run again' })).toBeDisabled();
  });

  // Both run buttons are the same control: one class list, in every state.
  it('gives Run upstream steps and Run again the same class list', () => {
    followRun([runNode('fetch', NodeStatus.Success)], false);
    const { unmount } = renderFollowed({ onRunUpstream: vi.fn() });
    const first = screen.getByRole('button', { name: 'Run upstream steps' });
    const again = screen.getByRole('button', { name: 'Run again' });
    expect(first.className).toBe(again.className);
    const idle = first.className;
    unmount();
    followRun([runNode('fetch', NodeStatus.Running)], true);
    renderFollowed({ onRunUpstream: vi.fn() });
    for (const name of ['Run upstream steps', 'Run again']) {
      const busy = screen.getByRole('button', { name });
      expect(busy.className).toBe(idle);
      expect(busy).toHaveAttribute('aria-busy', 'true');
      expect(busy.querySelector('.animate-spin')).not.toBeNull();
    }
  });

  it('ignores a click on a busy run button', () => {
    followRun([runNode('fetch', NodeStatus.Running)], true);
    const onRunUpstream = vi.fn();
    renderFollowed({ onRunUpstream });
    fireEvent.click(screen.getByRole('button', { name: 'Run again' }));
    expect(onRunUpstream).not.toHaveBeenCalled();
  });

  it('shows a load error when the followed run cannot be read', () => {
    hook.followed = { details: null, active: true, error: new Error('500') };
    renderFollowed();
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('Could not load the run r9');
    expect(alert).toHaveClass('text-destructive');
  });

  it('ignores the run of another step', () => {
    followRun([runNode('fetch', NodeStatus.Running)], true);
    renderFollowed({
      followedRun: { ...FOLLOWED, step: 'other' },
    });
    expect(screen.queryByTestId('upstream-state-fetch')).toBeNull();
    expect(screen.queryByTestId('upstream-last-run')).toBeNull();
  });

  // The run of the step alone has no upstream run of its own: the column
  // shows the states and the inputs of that run, without a Run again button.
  it('follows the run of Execute step without the upstream run controls', () => {
    followRun([runNode('fetch', NodeStatus.Running)], true);
    renderFollowed({
      onRunUpstream: vi.fn(),
      followedRun: { ...FOLLOWED, kind: 'execute' },
    });
    expect(stateOf('fetch')).toBe('running');
    expect(screen.queryByTestId('upstream-last-run')).toBeNull();
    expect(
      screen.getByRole('button', { name: 'Run upstream steps' })
    ).not.toHaveAttribute('aria-busy');
  });

  it('opens every step while a run is followed', () => {
    const steps = ['s0', 's1', 's2', 's3', 's4'].map((name, i) => ({
      name,
      id: name,
      stepPath: `steps[${i}]`,
      upstream: i === 0 ? [] : [`s${i - 1}`],
      outputs: [],
      outputsKnown: ReferenceScopeStepOutputsKnown.static,
    }));
    renderPanel({
      scope: { ...scopeHttpJq, steps },
      stepName: 's4',
      followedRun: { ...FOLLOWED, step: 's4' },
    });
    expect(screen.getByTestId('upstream-below-s2')).toBeVisible();
    expect(screen.getByTestId('upstream-state-s0')).toBeVisible();
  });
});

describe('UpstreamDataPanel long values', () => {
  const long = Array.from({ length: 12 }, (_, i) => `line ${i}`).join('\n');

  it('folds a long value behind Show more and unfolds it', () => {
    hook.results.fetch = {
      data: {
        ...stepOutputsSample,
        outputs: [{ name: 'status', value: long, truncated: false, size: 90 }],
      },
    };
    renderPanel({ runId: 'run-1' });
    const more = screen.getByRole('button', { name: 'Show more' });
    expect(more).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(more);
    expect(screen.getByRole('button', { name: 'Show less' })).toHaveAttribute(
      'aria-expanded',
      'true'
    );
  });

  it('has no Show more for a short value, and copies the value', () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
    hook.results.fetch = {
      data: {
        ...stepOutputsSample,
        outputs: [{ name: 'status', value: '200', truncated: false, size: 3 }],
      },
    };
    renderPanel({ runId: 'run-1' });
    expect(screen.queryByRole('button', { name: 'Show more' })).toBeNull();
    const row = screen.getByText('200').closest('li')!;
    fireEvent.click(within(row).getByRole('button', { name: 'Copy value' }));
    expect(writeText).toHaveBeenCalledWith('200');
  });
});

// A scope of steps given by name, upstream names and output names.
function scopeOf(
  steps: Array<[name: string, upstream: string[], outputs: string[]]>
): ReferenceScope {
  return {
    ...scopeHttpJq,
    steps: steps.map(([name, upstream, outputs], i) => ({
      name,
      id: name,
      stepPath: `steps[${i}]`,
      upstream,
      outputs: outputs.map((output) => ({
        name: output,
        reference: `\${steps.${name}.outputs.${output}}`,
        source: ReferenceScopeOutputSource.declared,
      })),
      outputsKnown: ReferenceScopeStepOutputsKnown.static,
    })),
  };
}

const itemOf = (name: string) => screen.getByTestId(`upstream-item-${name}`);

// The panel shows the direct dependencies of the step with their outputs.
// The steps under them are items that a chevron opens, closed at first.
describe('UpstreamDataPanel dependency tree', () => {
  // step1 > step2 > step3, and the panel is the one of step3.
  const chain = scopeOf([
    ['step1', [], ['one']],
    ['step2', ['step1'], ['two']],
    ['step3', ['step1', 'step2'], []],
  ]);

  it('shows the outputs of the direct dependency and closes the one under it', () => {
    renderPanel({ scope: chain, stepName: 'step3' });
    // step2 is direct: its output shows, and it has no chevron.
    expect(screen.getByText('two')).toBeVisible();
    expect(
      screen.queryByRole('button', { name: 'Outputs of step2' })
    ).toBeNull();
    // step1 is listed under step2, with its output behind a closed chevron.
    const below = within(itemOf('step2')).getByTestId('upstream-below-step2');
    expect(below).toContainElement(itemOf('step1'));
    expect(below).toBeVisible();
    const chevron = screen.getByRole('button', { name: 'Outputs of step1' });
    expect(chevron).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText('one')).not.toBeVisible();
    // The title of step1 shows, as the dependency of step2.
    expect(within(itemOf('step1')).getByText('step1')).toBeVisible();
  });

  it('opens and closes the outputs of a nested step with its chevron', () => {
    renderPanel({ scope: chain, stepName: 'step3' });
    const chevron = screen.getByRole('button', { name: 'Outputs of step1' });
    fireEvent.click(chevron);
    expect(chevron).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('one')).toBeVisible();
    fireEvent.click(chevron);
    expect(chevron).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText('one')).not.toBeVisible();
  });

  it('keeps the steps below a closed step closed, and opens them one by one', () => {
    const four = scopeOf([
      ['a', [], ['oa']],
      ['b', ['a'], ['ob']],
      ['c', ['a', 'b'], ['oc']],
      ['d', ['a', 'b', 'c'], []],
    ]);
    renderPanel({ scope: four, stepName: 'd' });
    // c is direct, b is under it and closed, a is under b.
    expect(screen.getByText('oc')).toBeVisible();
    expect(screen.getByTestId('upstream-below-c')).toBeVisible();
    expect(screen.getByTestId('upstream-below-b')).not.toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Outputs of b' }));
    expect(screen.getByText('ob')).toBeVisible();
    expect(screen.getByTestId('upstream-below-b')).toBeVisible();
    expect(screen.getByText('oa')).not.toBeVisible();
  });

  // a feeds b and c, and both feed d.
  it('lists the two direct dependencies of a diamond, with the shared step once in full', () => {
    const diamond = scopeOf([
      ['a', [], ['oa']],
      ['b', ['a'], ['ob']],
      ['c', ['a'], ['oc']],
      ['d', ['a', 'b', 'c'], []],
    ]);
    renderPanel({ scope: diamond, stepName: 'd' });
    expect(screen.getByText('ob')).toBeVisible();
    expect(screen.getByText('oc')).toBeVisible();
    // a is a dependency of b and of c: in full under b, as a short entry under c.
    expect(
      within(itemOf('b')).getByTestId('upstream-below-b')
    ).toContainElement(itemOf('a'));
    const repeated = within(itemOf('c')).getByTestId('upstream-repeated-a');
    expect(repeated).toHaveTextContent('shown above');
    expect(screen.getAllByTestId('upstream-item-a')).toHaveLength(1);
    expect(screen.getAllByText('oa')).toHaveLength(1);
  });

  it('ends on a cycle', () => {
    const cycle = scopeOf([
      ['a', ['b'], ['oa']],
      ['b', ['a'], ['ob']],
      ['c', ['a', 'b'], []],
    ]);
    renderPanel({ scope: cycle, stepName: 'c' });
    expect(screen.getAllByTestId(/^upstream-item-/).length).toBeLessThan(4);
  });
});

describe('UpstreamDataPanel step links', () => {
  const chain = scopeOf([
    ['step1', [], ['one']],
    ['step2', ['step1'], ['two']],
    ['step3', ['step1', 'step2'], []],
  ]);
  const renderLinked = (props: Parameters<typeof renderPanel>[0] = {}) => {
    const open = vi.fn();
    render(
      <StepLinksContext.Provider value={{ open }}>
        <UpstreamDataPanel
          dagName="etl"
          runId={null}
          scope={chain}
          stepName="step3"
          {...props}
        />
      </StepLinksContext.Provider>
    );
    return open;
  };

  it('opens the step of a title with a click, for a direct and a nested step', () => {
    const open = renderLinked();
    fireEvent.click(screen.getByRole('button', { name: 'Open step step2' }));
    expect(open).toHaveBeenLastCalledWith('step2');
    fireEvent.click(screen.getByRole('button', { name: 'Open step step1' }));
    expect(open).toHaveBeenLastCalledWith('step1');
    expect(open).toHaveBeenCalledTimes(2);
  });

  it('inserts no reference and does not open or close the outputs', () => {
    const onInsert = vi.fn();
    const open = renderLinked({ onInsert });
    fireEvent.click(screen.getByRole('button', { name: 'Open step step1' }));
    expect(open).toHaveBeenCalledWith('step1');
    expect(onInsert).not.toHaveBeenCalled();
    expect(
      screen.getByRole('button', { name: 'Outputs of step1' })
    ).toHaveAttribute('aria-expanded', 'false');
    // The name of an output still inserts its reference.
    fireEvent.click(screen.getByRole('button', { name: 'two' }));
    expect(onInsert).toHaveBeenCalledWith('${steps.step2.outputs.two}');
    expect(open).toHaveBeenCalledTimes(1);
  });

  it('opens with Enter and with Space, and underlines on hover', async () => {
    const user = userEvent.setup();
    const open = renderLinked();
    const title = screen.getByRole('button', { name: 'Open step step2' });
    expect(title).toHaveClass('hover:underline');
    title.focus();
    await user.keyboard('{Enter}');
    await user.keyboard(' ');
    expect(open).toHaveBeenCalledTimes(2);
    expect(open).toHaveBeenNthCalledWith(1, 'step2');
    expect(open).toHaveBeenNthCalledWith(2, 'step2');
  });

  it('shows plain names without a host that has links', () => {
    renderPanel({ scope: chain, stepName: 'step3' });
    expect(screen.queryByRole('button', { name: /^Open step/ })).toBeNull();
    expect(screen.getByText('step2')).toBeVisible();
  });
});
