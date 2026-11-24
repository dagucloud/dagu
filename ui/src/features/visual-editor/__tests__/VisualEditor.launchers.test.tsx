// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react';
import { createRef, useState, type ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { RightPanelProvider } from '@/contexts/RightPanelContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { LauncherHost, VisualEditorHandle } from '../contracts';
import type { RestartPolicy } from '../doc';
import { realSchema } from '../inspector/__tests__/realSchema';
import type { LauncherData } from '../launchers/LauncherData';
import { launcherStates } from '../launchers/model';
import { yamlArraySteps } from '../testing/fixtures';
import { VisualEditor, type VisualEditorProps } from '../VisualEditor';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('../inspector/__tests__/monacoMock')).monacoModule
);
vi.mock('../inspector/monacoSetup', () => ({ monacoTheme: () => 'light' }));
vi.mock('@/hooks/api', () => ({
  useQuery: () => ({ data: undefined, isLoading: false, mutate: () => {} }),
  useClient: () => ({ POST: () => undefined }),
}));
vi.mock('@/hooks/useDAGRunSSE', () => ({
  useDAGRunSSE: () => ({
    data: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
  }),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanExecute: () => true,
  useCanExecuteForWorkspace: () => true,
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tz: 'UTC' }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/contexts/SchemaContext', () => ({
  useSchema: () => ({ schema: realSchema }),
}));
vi.mock('../run/StepRunPanel', () => ({
  StepRunPanel: () => <p data-testid="step-run-panel" />,
}));
// The probe reads the server: the test gives the states it would give.
const probe = vi.hoisted(() => ({
  webhooks: true,
  webhookCount: 0 as number | null,
}));
vi.mock('../launchers/LauncherData', async () => {
  const { useEffect } = await import('react');
  return {
    LauncherDataProbe: ({
      scheduleCount,
      schedulePresent,
      restartPolicy,
      restartWaitSec,
      restartRetentionRuns,
      restartRetentionDays,
      onData,
    }: {
      scheduleCount: number;
      schedulePresent: boolean;
      restartPolicy: RestartPolicy;
      restartWaitSec: number | null;
      restartRetentionRuns: number | null;
      restartRetentionDays: number | null;
      onData(data: LauncherData): void;
    }) => {
      useEffect(() => {
        onData({
          states: launcherStates({
            scheduleCount,
            schedulePresent,
            restartPolicy,
            restartWaitSec,
            restartRetentionRuns,
            restartRetentionDays,
            webhookCount: probe.webhookCount,
            webhooksAvailable: probe.webhooks,
          }),
          webhooksAvailable: probe.webhooks,
          reloadWebhook: () => undefined,
        });
      }, [
        scheduleCount,
        schedulePresent,
        restartPolicy,
        restartWaitSec,
        restartRetentionRuns,
        restartRetentionDays,
        onData,
      ]);
      return null;
    },
  };
});
vi.mock('@/features/dags/components/dag-details/WebhookTab', () => ({
  default: () => <p data-testid="webhook-tab" />,
}));

const host: LauncherHost = {
  guard: vi.fn((next: () => void) => next()),
  setSecretVisible: vi.fn(),
};

const state = { text: '' };

function Harness({
  initial,
  ...overrides
}: Partial<VisualEditorProps> & { initial: string }) {
  const [text, setText] = useState(initial);
  state.text = text;
  return (
    <VisualEditor
      fileName="example.yaml"
      dagName="example"
      text={text}
      onTextChange={setText}
      validation={null}
      editable
      isDirty={text !== initial}
      customActions={[]}
      launcherHost={host}
      {...overrides}
    />
  );
}

function renderEditor(
  initial = yamlArraySteps,
  overrides: Partial<VisualEditorProps> = {},
  { panels = false }: { panels?: boolean } = {}
) {
  const handleRef = createRef<VisualEditorHandle>();
  const editor = (
    <UserPreferencesProvider>
      <Harness initial={initial} handleRef={handleRef} {...overrides} />
    </UserPreferencesProvider>
  );
  // With `panels`, the right panels take turns as on the page.
  const node: ReactNode = panels ? (
    <RightPanelProvider>{editor}</RightPanelProvider>
  ) : (
    editor
  );
  state.text = initial;
  render(node);
  return { handle: () => handleRef.current! };
}

function openAddStep(handle: () => VisualEditorHandle) {
  act(() => handle().addStep());
}

const launcherButton = (id: string) =>
  within(screen.getByTestId(`ve-launcher-${id}`)).getByRole('button');

beforeEach(() => {
  probe.webhooks = true;
  probe.webhookCount = 0;
  vi.mocked(host.guard).mockClear();
});

describe('VisualEditor launchers', () => {
  it('draws the launchers as roots of the graph', () => {
    renderEditor();
    expect(screen.getByTestId('ve-launcher-manual')).toBeInTheDocument();
    // Neither Cron nor Always on is set: one dashed placeholder for both.
    expect(screen.getByTestId('ve-launcher-schedule')).toHaveAttribute(
      'data-status',
      'dashed'
    );
    expect(screen.queryByTestId('ve-launcher-cron')).toBeNull();
    expect(screen.queryByTestId('ve-launcher-always')).toBeNull();
    expect(screen.getByTestId('ve-launcher-webhook')).toHaveAttribute(
      'data-status',
      'dashed'
    );
  });

  it('draws no Webhook when the instance has none', () => {
    probe.webhooks = false;
    renderEditor();
    expect(screen.queryByTestId('ve-launcher-webhook')).toBeNull();
    expect(screen.getByTestId('ve-launcher-schedule')).toBeInTheDocument();
  });

  it('opens no Webhook panel when the instance has no webhooks', () => {
    probe.webhooks = false;
    const { handle } = renderEditor();
    act(() => handle().openLauncher('webhook'));
    expect(screen.queryByTestId('ve-launcher-panel-webhook')).toBeNull();
    // The panel of the slot opens on the node that is drawn.
    act(() => handle().openLauncher('cron'));
    expect(
      screen.getByTestId('ve-launcher-panel-schedule')
    ).toBeInTheDocument();
  });

  it('draws Cron solid and alone when the YAML has a schedule', () => {
    renderEditor(`schedule: "0 * * * *"\n${yamlArraySteps}`);
    expect(screen.getByTestId('ve-launcher-cron')).toHaveAttribute(
      'data-status',
      'active'
    );
    expect(screen.queryByTestId('ve-launcher-always')).toBeNull();
  });

  it('opens the Cron panel from its node and edits the YAML text only', () => {
    renderEditor(`schedule: "0 * * * *"\n${yamlArraySteps}`);
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-cron')).getByRole('button')
    );
    const panel = screen.getByTestId('ve-launcher-panel-cron');
    fireEvent.change(within(panel).getByLabelText('New cron expression'), {
      target: { value: '30 6 * * *' },
    });
    fireEvent.click(
      within(panel).getByRole('button', { name: 'Add schedule' })
    );
    expect(state.text).toContain('- 0 * * * *');
    expect(state.text).toContain('- 30 6 * * *');
    expect(state.text).toContain(yamlArraySteps);

    within(panel)
      .getAllByRole('button', { name: 'Remove schedule' })
      .forEach(() =>
        fireEvent.click(
          within(panel).getAllByRole('button', { name: 'Remove schedule' })[0]!
        )
      );
    expect(state.text).toBe(yamlArraySteps);
  });

  // Cron and Always on are one slot of the graph (spec 080).
  it('draws Always on in place of Cron with a restart policy', () => {
    renderEditor(
      `restart_policy: always\nrestart_wait_sec: 5\nhist_retention_runs: 20\n${yamlArraySteps}`
    );
    expect(screen.getByTestId('ve-launcher-always')).toHaveAttribute(
      'data-status',
      'active'
    );
    expect(launcherButton('always')).toHaveAttribute(
      'title',
      'Always on: Restarts after each run, wait 5 s, keeps 20 runs'
    );
    expect(screen.queryByTestId('ve-launcher-cron')).toBeNull();
  });
  // A file with `hist_retention_days` keeps runs by days, not the 100 runs
  // of the default.
  it('tells the days that an Always on workflow keeps its runs for', () => {
    renderEditor(
      `restart_policy: always\nhist_retention_days: 7\n${yamlArraySteps}`
    );
    expect(launcherButton('always')).toHaveAttribute(
      'title',
      'Always on: Restarts after each run, keeps runs for 7 days'
    );
  });

  // Always on tells the state of the latest run of the workflow, which the
  // host keeps live, and is idle for a suspended workflow.
  describe('Always on state', () => {
    const file = `restart_policy: always\nrestart_wait_sec: 5\n${yamlArraySteps}`;
    const alive = () =>
      screen.getByTestId('ve-launcher-always').getAttribute('data-alive');

    it('is running while the latest run of the host runs', () => {
      renderEditor(file, {
        launcherHost: {
          ...host,
          latestRun: { status: Status.Running, finishedAt: '-' },
        },
      });
      expect(alive()).toBe('running');
    });

    it('is idle when the latest run ended long ago, or the workflow is suspended', () => {
      renderEditor(file, {
        launcherHost: {
          ...host,
          latestRun: {
            status: Status.Success,
            finishedAt: '2020-01-01T00:00:00Z',
          },
        },
      });
      expect(alive()).toBeNull();
      cleanup();
      renderEditor(file, {
        launcherHost: {
          ...host,
          suspended: true,
          latestRun: { status: Status.Running, finishedAt: '-' },
        },
      });
      expect(alive()).toBeNull();
    });
  });

  it('swaps the slot as the author chooses, sets, switches and removes', () => {
    renderEditor();
    const open = (id: string) =>
      fireEvent.click(
        within(screen.getByTestId(`ve-launcher-${id}`)).getByRole('button')
      );
    const slot = () =>
      ['schedule', 'cron', 'always'].filter((id) =>
        screen.queryByTestId(`ve-launcher-${id}`)
      );
    expect(slot()).toEqual(['schedule']);

    // The chooser writes nothing.
    open('schedule');
    const panel = () => screen.getByTestId(/^ve-launcher-panel-/);
    fireEvent.click(within(panel()).getByRole('button', { name: /^Cron/ }));
    expect(state.text).toBe(yamlArraySteps);
    expect(slot()).toEqual(['schedule']);

    // The first expression turns the node into Cron: the panel follows it.
    fireEvent.change(within(panel()).getByLabelText('New cron expression'), {
      target: { value: '0 * * * *' },
    });
    fireEvent.click(
      within(panel()).getByRole('button', { name: 'Add schedule' })
    );
    expect(slot()).toEqual(['cron']);
    expect(screen.getByTestId('ve-launcher-panel-cron')).toBeInTheDocument();
    expect(screen.getByTestId('ve-launcher-cron')).toHaveAttribute(
      'data-status',
      'active'
    );

    // Switch: the schedule goes, the chooser is back with Always on chosen.
    fireEvent.click(
      within(panel()).getByRole('button', { name: 'Switch to Always on' })
    );
    expect(state.text).toBe(yamlArraySteps);
    expect(slot()).toEqual(['schedule']);
    expect(
      within(panel()).getByRole('button', { name: /^Always on/ })
    ).toHaveAttribute('aria-pressed', 'true');

    // The first policy turns the node into Always on, with a wait of 5 s.
    fireEvent.click(within(panel()).getByRole('button', { name: 'Always' }));
    expect(slot()).toEqual(['always']);
    expect(screen.getByTestId('ve-launcher-panel-always')).toBeInTheDocument();
    expect(state.text).toContain(
      'restart_policy: always\nrestart_wait_sec: 5\n'
    );
    expect(launcherButton('always')).toHaveAttribute(
      'title',
      'Always on: Restarts after each run, wait 5 s, keeps 100 runs, or as many as the base configuration sets'
    );
    const wait = within(panel()).getByLabelText('Wait before a new run (s)');
    fireEvent.change(wait, { target: { value: '30' } });
    fireEvent.blur(wait);
    fireEvent.click(
      within(panel()).getByRole('button', { name: 'Unless stopped' })
    );
    const runs = within(panel()).getByLabelText('Runs kept in the history');
    fireEvent.change(runs, { target: { value: '20' } });
    fireEvent.blur(runs);
    expect(state.text).toContain('restart_policy: unless-stopped\n');
    expect(state.text).toContain('restart_wait_sec: 30\n');
    expect(state.text).toContain('hist_retention_runs: 20\n');
    expect(launcherButton('always')).toHaveAttribute(
      'title',
      'Always on: Restarts unless stopped, wait 30 s, keeps 20 runs'
    );

    // Switch back to Cron.
    fireEvent.click(
      within(panel()).getByRole('button', { name: 'Switch to Cron' })
    );
    // The wait goes with the policy; the runs kept also apply to a schedule.
    expect(state.text).not.toContain('restart_policy');
    expect(state.text).not.toContain('restart_wait_sec');
    expect(state.text).toContain('hist_retention_runs: 20\n');
    expect(slot()).toEqual(['schedule']);
    expect(
      within(panel()).getByRole('button', { name: /^Cron/ })
    ).toHaveAttribute('aria-pressed', 'true');
  });

  // The switch removes the start expressions only. With a `stop` entry the
  // schedule would stay, and a restart policy cannot go with it.
  it('offers no switch to Always on for a schedule that would stay', () => {
    const text = `schedule:\n  start: "0 8 * * *"\n  stop: "0 18 * * *"\n${yamlArraySteps}`;
    renderEditor(text);
    fireEvent.click(launcherButton('cron'));
    const panel = screen.getByTestId('ve-launcher-panel-cron');
    expect(
      within(panel).queryByRole('button', { name: 'Switch to Always on' })
    ).toBeNull();
    expect(
      within(panel).getByText(
        'Edit the schedule in the YAML to switch to Always on.'
      )
    ).toBeInTheDocument();
    expect(state.text).toBe(text);
  });

  it('switches to Always on from a schedule with start entries only', () => {
    renderEditor(`schedule:\n  start: "0 8 * * *"\n${yamlArraySteps}`);
    fireEvent.click(launcherButton('cron'));
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-panel-cron')).getByRole('button', {
        name: 'Switch to Always on',
      })
    );
    expect(state.text).not.toContain('schedule');
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-panel-schedule')).getByRole(
        'button',
        { name: 'Always' }
      )
    );
    expect(state.text).toContain('restart_policy: always\n');
  });

  // A file with both keys is invalid: the panel names the conflict, and the
  // switch of each form removes its own setting.
  it('names the conflict when the YAML has a policy and a schedule', () => {
    renderEditor(
      `restart_policy: always\nrestart_wait_sec: 5\nschedule: "0 * * * *"\n${yamlArraySteps}`
    );
    // Both nodes are solid, so that the conflict shows on the graph.
    for (const id of ['cron', 'always']) {
      expect(screen.getByTestId(`ve-launcher-${id}`)).toHaveAttribute(
        'data-status',
        'active'
      );
    }
    fireEvent.click(launcherButton('always'));
    const panel = screen.getByTestId('ve-launcher-panel-always');
    expect(within(panel).getByRole('note')).toHaveTextContent(
      'A service DAG cannot have a schedule'
    );
    fireEvent.click(
      within(panel).getByRole('button', { name: 'Switch to Cron' })
    );
    expect(state.text).not.toContain('restart_policy');
    expect(state.text).toContain('schedule');
    expect(screen.queryByTestId('ve-launcher-always')).toBeNull();
  });

  // Spec 080: an empty schedule defines none. It holds no slot, and a policy
  // can go with it.
  it('counts an empty schedule as none', () => {
    renderEditor(`schedule: {}\n${yamlArraySteps}`);
    expect(screen.getByTestId('ve-launcher-schedule')).toHaveAttribute(
      'data-status',
      'dashed'
    );
    cleanup();
    renderEditor(
      `restart_policy: always\nrestart_wait_sec: 5\nschedule: {}\n${yamlArraySteps}`
    );
    expect(screen.queryByTestId('ve-launcher-cron')).toBeNull();
    fireEvent.click(launcherButton('always'));
    expect(
      within(screen.getByTestId('ve-launcher-panel-always')).queryByRole('note')
    ).toBeNull();
  });

  // The YAML panel shows the file with a step in focus. A click on a launcher
  // ends that focus, and the panel of the launcher takes the place of the file.
  it('ends the focus of the YAML on a step when a launcher is clicked', () => {
    const onYamlVisibleChange = vi.fn();
    const onSelectedStepNameChange = vi.fn();
    renderEditor(yamlArraySteps, {
      yamlVisible: true,
      initialYamlMode: 'file',
      selectedStepName: 'transform',
      onYamlVisibleChange,
      onSelectedStepNameChange,
    });
    // The spotlight of a host is a variant that only data-spotlit turns on.
    const ring = () =>
      screen
        .getByTestId('ve-canvas-node-steps[1]')
        .className.split(/\s+/)
        .filter((t) => !t.startsWith('group-data-[spotlit]/node:'))
        .join(' ');
    expect(ring()).toContain('ring-primary');

    fireEvent.click(
      within(screen.getByTestId('ve-launcher-schedule')).getByRole('button')
    );

    expect(onYamlVisibleChange).toHaveBeenLastCalledWith(false);
    expect(onSelectedStepNameChange).toHaveBeenLastCalledWith(null);
    expect(
      screen.getByTestId('ve-launcher-panel-schedule')
    ).toBeInTheDocument();
    expect(ring()).not.toContain('ring-primary');
  });

  // A trigger node opens its panel over the page; with no panel under it,
  // nothing opens when it closes (RightPanelContext restores a covered one).
  it('opens nothing on close when no panel was open before', () => {
    const onYamlVisibleChange = vi.fn();
    renderEditor(yamlArraySteps, { onYamlVisibleChange }, { panels: true });
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-schedule')).getByRole('button')
    );
    const panel = screen.getByTestId('ve-launcher-panel-schedule');
    fireEvent.click(within(panel).getByRole('button', { name: 'Close' }));
    expect(onYamlVisibleChange).not.toHaveBeenCalledWith(true, 'panel');
  });

  // A click on the empty canvas ends the focus on a trigger, as on a step.
  it('closes the launcher panel on a click on the empty canvas', () => {
    renderEditor();
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-schedule')).getByRole('button')
    );
    expect(
      screen.getByTestId('ve-launcher-panel-schedule')
    ).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('flow'));
    expect(screen.queryByTestId('ve-launcher-panel-schedule')).toBeNull();
  });

  // The YAML of the file stays: a trigger shows its part of the file, as a
  // step shows its block.
  it('shows the YAML of the trigger while the YAML of the file shows', () => {
    const onYamlVisibleChange = vi.fn();
    const onRevealTrigger = vi.fn(() => true);
    renderEditor(`schedule: "0 * * * *"\n${yamlArraySteps}`, {
      yamlVisible: true,
      initialYamlMode: 'file',
      onYamlVisibleChange,
      onRevealTrigger,
    });
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-cron')).getByRole('button')
    );
    expect(onRevealTrigger).toHaveBeenCalledWith('cron');
    expect(screen.queryByTestId('ve-launcher-panel-cron')).toBeNull();
    expect(onYamlVisibleChange).not.toHaveBeenCalledWith(false);
  });

  // A trigger with no part in the file, such as a webhook, opens its panel.
  it('opens the panel of a trigger that has no part in the YAML', () => {
    renderEditor(yamlArraySteps, {
      yamlVisible: true,
      initialYamlMode: 'file',
      onYamlVisibleChange: vi.fn(),
      onRevealTrigger: () => false,
    });
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-schedule')).getByRole('button')
    );
    expect(
      screen.getByTestId('ve-launcher-panel-schedule')
    ).toBeInTheDocument();
  });

  // A launcher panel and a step take turns: a click on a step closes the
  // panel, and the step is the one that is focused.
  it('closes the launcher panel when a step is clicked', () => {
    const onSelectedStepNameChange = vi.fn();
    renderEditor(yamlArraySteps, { onSelectedStepNameChange });
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-schedule')).getByRole('button')
    );
    expect(
      screen.getByTestId('ve-launcher-panel-schedule')
    ).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));

    expect(screen.queryByTestId('ve-launcher-panel-schedule')).toBeNull();
    expect(onSelectedStepNameChange).toHaveBeenLastCalledWith('transform');
  });

  // A click on a launcher ends the focus on a step, whatever the panel shows.
  it('clears the selected step when a launcher opens', () => {
    const onSelectedStepNameChange = vi.fn();
    renderEditor(yamlArraySteps, {
      selectedStepName: 'transform',
      onSelectedStepNameChange,
    });
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-schedule')).getByRole('button')
    );
    expect(onSelectedStepNameChange).toHaveBeenCalledWith(null);
  });

  // The list holds the whole catalog: a role query by name computes the
  // accessible name of every row, so the rows are read once by their text.
  // A row reads as its title run into its blurb, as "CronRun on a ...".
  it('lists no launcher in the Add step list', () => {
    const { handle } = renderEditor();
    openAddStep(handle);
    const options = within(screen.getByTestId('ve-add-step')).getAllByRole(
      'option'
    );
    expect(options.length).toBeGreaterThan(0);
    const texts = options.map((option) => option.textContent ?? '');
    for (const name of ['Manual', 'Cron', 'Webhook']) {
      const launcher = new RegExp(`^${name}(?![a-z])`);
      expect(texts.filter((text) => launcher.test(text))).toEqual([]);
    }
    expect(state.text).toBe(yamlArraySteps);
  });

  it('closes a launcher panel through the guard of the host', () => {
    renderEditor();
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-webhook')).getByRole('button')
    );
    const panel = screen.getByTestId('ve-launcher-panel-webhook');
    fireEvent.click(within(panel).getByRole('button', { name: 'Close' }));
    expect(host.guard).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId('ve-launcher-panel-webhook')).toBeNull();
  });

  it('has no launcher without a host', () => {
    const handleRef = createRef<VisualEditorHandle>();
    render(
      <UserPreferencesProvider>
        <Harness
          initial={yamlArraySteps}
          handleRef={handleRef}
          launcherHost={undefined}
        />
      </UserPreferencesProvider>
    );
    expect(screen.queryByTestId('ve-launcher-manual')).toBeNull();
  });
});
