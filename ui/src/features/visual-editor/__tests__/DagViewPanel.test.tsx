// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { ToolRunContext, type ToolRunInfo } from '../canvas/toolRun';
import type { ToolSelection } from '../contracts';
import { DagViewPanel } from '../DagViewPanel';
import { parseDagDoc } from '../doc';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
// The canvas of the DAG page is tested on its own: a list of buttons stands in
// for it, with the step that is selected and the state of the run.
vi.mock('../DetailsCanvas', () => ({
  default: (props: {
    dag: { name: string; steps?: { name: string }[] };
    selectedStepName?: string;
    overlay?: Record<string, { status: number }>;
    onSelectStep(name: string | null): void;
  }) => (
    <div
      data-testid="details-canvas"
      data-dag={props.dag.name}
      data-selected={props.selectedStepName}
      data-overlay={JSON.stringify(props.overlay ?? null)}
    >
      {(props.dag.steps ?? []).map((item) => (
        <button
          key={item.name}
          type="button"
          data-testid={`pick-${item.name}`}
          onClick={() => props.onSelectStep(item.name)}
        >
          {item.name}
        </button>
      ))}
    </div>
  ),
}));

const step = (name: string, over = {}) => ({ name, depends: [], ...over });

const DAGS: Record<string, unknown> = {
  triage: {
    name: 'triage',
    description: 'Read a ticket and route it.',
    paramDefs: [
      { name: 'ticket_id', type: 'string', required: true },
      { name: 'lang', type: 'string', required: false },
    ],
    steps: [
      step('fetch', { commands: [{ command: 'curl', args: ['x'] }] }),
      step('rate', {
        depends: ['fetch'],
        preconditions: [{ condition: '${X}', expected: 'high' }],
      }),
      step('hand-over', { depends: ['rate'], call: 'loop-b' }),
    ],
  },
  'loop-b': {
    name: 'loop-b',
    description: 'B calls A.',
    steps: [
      step('call-a', { call: 'triage' }),
      step('call-self', { call: 'loop-b' }),
    ],
  },
  empty: { name: 'empty', description: '', steps: [] },
};

let sub: unknown;

function serve() {
  (useQuery as unknown as ReturnType<typeof vi.fn>).mockImplementation(
    (
      path: string,
      init: { params?: { path?: { fileName?: string } } } | null
    ) => {
      if (init === null) return { data: undefined };
      if (path === '/dags/{fileName}') {
        const dag = DAGS[init.params?.path?.fileName ?? ''];
        return dag
          ? { data: { dag } }
          : { data: undefined, error: new Error('404') };
      }
      if (path.includes('/sub-dag-runs/')) {
        return { data: sub ? { dagRunDetails: sub } : undefined };
      }
      return { data: undefined };
    }
  );
}

afterEach(() => {
  vi.clearAllMocks();
  sub = undefined;
});

const TEXT = (dag: string) => `steps:
  - id: support
    action: agent.run
    with:
      provider: claude
      prompt: help
      dags:
        - name: ${dag}
          params:
            lang: fr
`;

const TOOL: ToolSelection = { step: 'steps[0]', category: 'dags', index: 0 };

function setup(
  dag = 'triage',
  opts: {
    host?: string;
    run?: ToolRunInfo | null;
    tool?: ToolSelection;
  } = {}
) {
  serve();
  const model = parseDagDoc(TEXT(dag));
  const onShowTool = vi.fn();
  const onBack = vi.fn();
  const onClose = vi.fn();
  const view = render(
    <ToolRunContext.Provider value={opts.run ?? null}>
      <DagViewPanel
        step={model.steps[0]!}
        stepLabel="support"
        stepName="support"
        tool={opts.tool ?? TOOL}
        host={opts.host ?? 'main'}
        onShowTool={onShowTool}
        onBack={onBack}
        onClose={onClose}
      />
    </ToolRunContext.Provider>
  );
  return { ...view, onShowTool, onBack, onClose };
}

const crumbs = () =>
  within(screen.getByTestId('ve-dag-crumbs'))
    .getAllByRole('listitem')
    .map((item) => item.textContent);

const trail = () => screen.getByTestId('ve-dag-crumbs').textContent;

describe('DagViewPanel', () => {
  it('shows the DAG of the tool: its title, description, page link, inputs and canvas', () => {
    setup();
    expect(screen.getByTestId('ve-dag-title')).toHaveTextContent('triage');
    expect(screen.getByTestId('ve-dag-panel')).toHaveAttribute(
      'data-depth',
      '1'
    );
    expect(screen.getByTestId('ve-dag-panel-description')).toHaveTextContent(
      'Read a ticket and route it.'
    );
    expect(screen.getByTestId('ve-open-text-link')).toHaveAttribute(
      'href',
      '/dags/triage'
    );
    // The inputs of the DAG, with the state that the entry gives each one.
    const chips = screen.getAllByTestId('ve-dag-input');
    expect(chips.map((chip) => [chip.textContent, chip.dataset.state])).toEqual(
      [
        ['ticket_id*', 'open'],
        ['lang', 'fixed'],
      ]
    );
    expect(screen.getByTestId('details-canvas')).toHaveAttribute(
      'data-dag',
      'triage'
    );
    expect(
      within(screen.getByTestId('ve-dag-graph')).getByTestId('details-canvas')
    ).toBeInTheDocument();
    expect(screen.getByTestId('ve-dag-step-empty')).toHaveTextContent(
      'Select a step to see its details.'
    );
  });

  it('has a breadcrumb that starts at the calling step and ends at the DAG', () => {
    const { onBack, onShowTool, onClose } = setup();
    expect(trail()).toBe('supporttriage');
    fireEvent.click(screen.getByTestId('ve-dag-crumb-step'));
    expect(onBack).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId('ve-dag-edit-inputs'));
    expect(onShowTool).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('shows the facts of the step that the canvas selects', () => {
    setup();
    fireEvent.click(screen.getByTestId('pick-rate'));
    const facts = screen.getByTestId('ve-dag-step');
    expect(facts).toHaveAttribute('data-step', 'rate');
    expect(facts).toHaveTextContent('Depends on: fetch');
    expect(facts).toHaveTextContent('Only if ${X} = high');
    expect(screen.getByTestId('details-canvas')).toHaveAttribute(
      'data-selected',
      'rate'
    );
    fireEvent.click(screen.getByTestId('pick-fetch'));
    expect(screen.getByTestId('ve-dag-step')).toHaveTextContent('curl x');
  });

  it('opens the DAG that a call step names one level deeper, and goes back from the breadcrumb', () => {
    setup();
    fireEvent.click(screen.getByTestId('pick-hand-over'));
    expect(screen.getByTestId('ve-dag-step-call')).toHaveTextContent(
      'Calls loop-b'
    );
    fireEvent.click(screen.getByTestId('ve-dag-step-open'));
    expect(screen.getByTestId('ve-dag-title')).toHaveTextContent('loop-b');
    expect(screen.getByTestId('ve-dag-panel')).toHaveAttribute(
      'data-depth',
      '2'
    );
    expect(trail()).toBe('supporttriageloop-b');
    expect(screen.getByTestId('details-canvas')).toHaveAttribute(
      'data-dag',
      'loop-b'
    );
    // The new DAG starts with no step selected.
    expect(screen.getByTestId('ve-dag-step-empty')).toBeInTheDocument();
    // The tool has its inputs at the first level only.
    expect(screen.queryByTestId('ve-dag-edit-inputs')).toBeNull();
    fireEvent.click(screen.getByTestId('ve-dag-crumb'));
    expect(screen.getByTestId('ve-dag-title')).toHaveTextContent('triage');
    expect(screen.getByTestId('ve-dag-panel')).toHaveAttribute(
      'data-depth',
      '1'
    );
  });

  // A -> B -> A: the call that goes back to an open DAG is shown once and is
  // never opened, so the view cannot loop.
  it('shows a call to an open DAG as a loop, with no button to open it', () => {
    setup();
    fireEvent.click(screen.getByTestId('pick-hand-over'));
    fireEvent.click(screen.getByTestId('ve-dag-step-open'));
    fireEvent.click(screen.getByTestId('pick-call-a'));
    expect(screen.getByTestId('ve-dag-step-call')).toHaveTextContent(
      'Calls triage'
    );
    expect(screen.getByTestId('ve-dag-loop')).toHaveTextContent(
      'Open already: a call that loops is shown once.'
    );
    expect(screen.queryByTestId('ve-dag-step-open')).toBeNull();
    // A DAG that calls itself is the same.
    fireEvent.click(screen.getByTestId('pick-call-self'));
    expect(screen.getByTestId('ve-dag-loop')).toBeInTheDocument();
    expect(screen.queryByTestId('ve-dag-step-open')).toBeNull();
    expect(screen.getByTestId('ve-dag-panel')).toHaveAttribute(
      'data-depth',
      '2'
    );
  });

  it('shows a call to the DAG that holds the step as a loop too', () => {
    setup('triage', { host: 'loop-b' });
    fireEvent.click(screen.getByTestId('pick-hand-over'));
    expect(screen.getByTestId('ve-dag-loop')).toBeInTheDocument();
    expect(screen.queryByTestId('ve-dag-step-open')).toBeNull();
  });

  it('says that the server does not know the DAG, and draws no canvas', () => {
    setup('no-such-dag');
    expect(screen.getByTestId('ve-dag-missing')).toHaveTextContent(
      'The server does not know the DAG "no-such-dag".'
    );
    expect(screen.queryByTestId('details-canvas')).toBeNull();
  });

  it('says that a DAG with no step has none', () => {
    setup('empty');
    expect(screen.getByText('This DAG has no steps.')).toBeInTheDocument();
    expect(screen.queryByTestId('details-canvas')).toBeNull();
  });

  it('says that the tool is gone when the entry no longer exists', () => {
    setup('triage', {
      tool: { step: 'steps[0]', category: 'dags', index: 5 },
    });
    expect(screen.getByText('This tool no longer exists.')).toBeInTheDocument();
  });

  it('gives the canvas the state of the steps of the latest sub-run of a run view', () => {
    sub = {
      status: Status.Success,
      nodes: [
        { step: { name: 'fetch' }, status: 4, statusLabel: 'succeeded' },
        { step: { name: 'rate' }, status: 2, statusLabel: 'failed' },
      ],
    };
    setup('triage', {
      run: {
        dagName: 'main',
        runId: 'run-1',
        calls: { support: { triage: 1 } },
        subRuns: { support: { triage: [{ dagRunId: 'sub-1' }] } },
        root: { name: 'main', id: 'run-1' },
      },
    });
    const overlay = JSON.parse(
      screen.getByTestId('details-canvas').dataset.overlay ?? 'null'
    );
    expect(Object.keys(overlay).sort()).toEqual(['fetch', 'rate']);
    // The title bar has the status of the sub-run and a link to its page.
    expect(screen.getByTestId('status-dot')).toHaveAttribute(
      'data-status',
      String(Status.Success)
    );
    expect(
      screen
        .getAllByTestId('ve-open-link')
        .some((link) => link.getAttribute('href')?.includes('subRun=sub-1'))
    ).toBe(true);
  });

  it('has no run state in a canvas with no run', () => {
    setup();
    expect(screen.getByTestId('details-canvas')).toHaveAttribute(
      'data-overlay',
      'null'
    );
    expect(crumbs()).toEqual(['support', 'triage']);
  });
});
