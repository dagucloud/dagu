// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { createRef, useState, type ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { LauncherHost, VisualEditorHandle } from '../contracts';
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
vi.mock('@/contexts/AuthContext', () => ({ useCanExecute: () => true }));
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
      onData,
    }: {
      scheduleCount: number;
      onData(data: LauncherData): void;
    }) => {
      useEffect(() => {
        onData({
          states: launcherStates({
            scheduleCount,
            webhookCount: probe.webhookCount,
            webhooksAvailable: probe.webhooks,
          }),
          webhooksAvailable: probe.webhooks,
          reloadWebhook: () => undefined,
        });
      }, [scheduleCount, onData]);
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
  overrides: Partial<VisualEditorProps> = {}
) {
  const handleRef = createRef<VisualEditorHandle>();
  const node: ReactNode = (
    <UserPreferencesProvider>
      <Harness initial={initial} handleRef={handleRef} {...overrides} />
    </UserPreferencesProvider>
  );
  state.text = initial;
  render(node);
  return { handle: () => handleRef.current! };
}

function openAddStep(handle: () => VisualEditorHandle) {
  act(() => handle().addStep());
}

beforeEach(() => {
  probe.webhooks = true;
  probe.webhookCount = 0;
  vi.mocked(host.guard).mockClear();
});

describe('VisualEditor launchers', () => {
  it('draws the three launchers as roots of the graph', () => {
    renderEditor();
    expect(screen.getByTestId('ve-launcher-manual')).toBeInTheDocument();
    expect(screen.getByTestId('ve-launcher-cron')).toHaveAttribute(
      'data-status',
      'dashed'
    );
    expect(screen.getByTestId('ve-launcher-webhook')).toHaveAttribute(
      'data-status',
      'dashed'
    );
  });

  it('draws no Webhook when the instance has none', () => {
    probe.webhooks = false;
    renderEditor();
    expect(screen.queryByTestId('ve-launcher-webhook')).toBeNull();
    expect(screen.getByTestId('ve-launcher-cron')).toBeInTheDocument();
  });

  it('opens no Webhook panel when the instance has no webhooks', () => {
    probe.webhooks = false;
    const { handle } = renderEditor();
    act(() => handle().openLauncher('webhook'));
    expect(screen.queryByTestId('ve-launcher-panel-webhook')).toBeNull();
    act(() => handle().openLauncher('cron'));
    expect(screen.getByTestId('ve-launcher-panel-cron')).toBeInTheDocument();
  });

  it('draws Cron solid when the YAML has a schedule', () => {
    renderEditor(`schedule: "0 * * * *"\n${yamlArraySteps}`);
    expect(screen.getByTestId('ve-launcher-cron')).toHaveAttribute(
      'data-status',
      'active'
    );
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
    fireEvent.click(within(panel).getByRole('button', { name: 'Add' }));
    expect(state.text).toContain('- 0 * * * *');
    expect(state.text).toContain('- 30 6 * * *');
    expect(state.text).toContain(yamlArraySteps);

    fireEvent.click(
      within(panel).getByRole('button', { name: 'Remove schedule' })
    );
    expect(state.text).toBe(yamlArraySteps);
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
      within(screen.getByTestId('ve-launcher-cron')).getByRole('button')
    );

    expect(onYamlVisibleChange).toHaveBeenLastCalledWith(false);
    expect(onSelectedStepNameChange).toHaveBeenLastCalledWith(null);
    expect(screen.getByTestId('ve-launcher-panel-cron')).toBeInTheDocument();
    expect(ring()).not.toContain('ring-primary');
  });

  // A launcher panel and a step take turns: a click on a step closes the
  // panel, and the step is the one that is focused.
  it('closes the launcher panel when a step is clicked', () => {
    const onSelectedStepNameChange = vi.fn();
    renderEditor(yamlArraySteps, { onSelectedStepNameChange });
    fireEvent.click(
      within(screen.getByTestId('ve-launcher-cron')).getByRole('button')
    );
    expect(screen.getByTestId('ve-launcher-panel-cron')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('ve-canvas-node-steps[1]'));

    expect(screen.queryByTestId('ve-launcher-panel-cron')).toBeNull();
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
      within(screen.getByTestId('ve-launcher-cron')).getByRole('button')
    );
    expect(onSelectedStepNameChange).toHaveBeenCalledWith(null);
  });

  it('lists no launcher in the Add step list', () => {
    const { handle } = renderEditor();
    openAddStep(handle);
    const list = within(screen.getByTestId('ve-add-step'));
    expect(list.getAllByRole('option').length).toBeGreaterThan(0);
    for (const name of ['Manual', 'Cron', 'Webhook']) {
      expect(
        list.queryByRole('option', { name: new RegExp(`^${name}\\b`, 'i') })
      ).toBeNull();
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
