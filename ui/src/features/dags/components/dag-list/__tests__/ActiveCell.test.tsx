// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ScheduleKind,
  ServiceDAGState,
  type components,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { GroupScheduleCell, ScheduleCell, WebhookCell } from '../ActiveCell';

type DAGFile = components['schemas']['DAGFile'];
type ServiceDAG = components['schemas']['ServiceDAG'];

const mocks = vi.hoisted(() => ({
  canManageWebhooks: true,
  runDags: true,
  webhooks: [] as Array<{ dagName: string; enabled: boolean }>,
  post: vi.fn(),
  mutate: vi.fn(),
  showError: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => ({
    data:
      path === '/webhooks' && init ? { webhooks: mocks.webhooks } : undefined,
    mutate: mocks.mutate,
  }),
  useClient: () => ({ POST: mocks.post }),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanManageWebhooks: () => mocks.canManageWebhooks,
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tz: 'UTC', permissions: { runDags: mocks.runDags } }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: mocks.showError }),
}));

function dagFile(schedule: components['schemas']['Schedule'][]): DAGFile {
  return {
    fileName: 'etl',
    dag: { name: 'etl', schedule },
    latestDAGRun: {},
    suspended: false,
    pinned: false,
    errors: [],
  } as unknown as DAGFile;
}

function renderCell(dag: DAGFile) {
  return render(
    <UserPreferencesProvider>
      <I18nProvider>
        <ScheduleCell dag={dag} />
        <WebhookCell dag={dag} />
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

const cron = [{ kind: ScheduleKind.cron, expression: '0 * * * *' }];

beforeEach(() => {
  mocks.canManageWebhooks = true;
  mocks.runDags = true;
  mocks.webhooks = [];
  mocks.post.mockReset().mockResolvedValue({ error: undefined });
  mocks.mutate.mockReset();
  mocks.showError.mockReset();
});

describe('ScheduleCell and WebhookCell', () => {
  it('shows the schedule switch and no webhook switch for a scheduled workflow', () => {
    renderCell(dagFile(cron));
    expect(
      screen.getByRole('switch', { name: 'Schedule for etl' })
    ).toBeVisible();
    expect(screen.queryByTestId('webhook-switch')).toBeNull();
  });

  // The switch of a group suspends its workflows: the row follows the list.
  it('follows a schedule that was suspended from elsewhere', () => {
    const view = renderCell(dagFile(cron));
    expect(
      screen.getByRole('switch', { name: 'Schedule for etl' })
    ).toBeChecked();
    const suspended = { ...dagFile(cron), suspended: true } as DAGFile;
    view.rerender(
      <UserPreferencesProvider>
        <I18nProvider>
          <ScheduleCell dag={suspended} />
          <WebhookCell dag={suspended} />
        </I18nProvider>
      </UserPreferencesProvider>
    );
    expect(
      screen.getByRole('switch', { name: 'Schedule for etl' })
    ).not.toBeChecked();
  });

  it('shows a dash in each column for a workflow that has neither', () => {
    renderCell(dagFile([]));
    expect(screen.getByTestId('schedule-cell').textContent).toBe('-');
    expect(screen.getByTestId('webhook-cell').textContent).toBe('-');
  });

  it('shows a service DAG with its switch and its next restart', () => {
    const dag = dagFile([]);
    dag.dag = {
      ...dag.dag,
      restartPolicy: 'always',
      restartWaitSec: 1,
    } as never;
    const service = {
      fileName: 'etl',
      state: ServiceDAGState.restarting,
      nextRestartAt: new Date(Date.now() + 4 * 60_000).toISOString(),
    } as ServiceDAG;
    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <ScheduleCell dag={dag} service={service} />
        </I18nProvider>
      </UserPreferencesProvider>
    );
    expect(screen.getByTestId('schedule-cell').textContent).toBe(
      'ServiceRestart in 4 minutes'
    );
    expect(
      screen.getByRole('switch', { name: 'Restarts for etl' })
    ).toBeVisible();
  });

  it('switches a webhook off after a confirmation', async () => {
    mocks.webhooks = [{ dagName: 'etl', enabled: true }];
    renderCell(dagFile([]));

    const toggle = screen.getByRole('switch', { name: 'Webhook of etl' });
    expect(toggle).toBeChecked();
    await userEvent.click(toggle);
    await userEvent.click(screen.getByRole('button', { name: 'Disable' }));

    await waitFor(() =>
      expect(mocks.post).toHaveBeenCalledWith(
        '/dags/{fileName}/webhook/toggle',
        {
          params: { path: { fileName: 'etl' }, query: { remoteNode: 'local' } },
          body: { enabled: false },
        }
      )
    );
    await waitFor(() => expect(mocks.mutate).toHaveBeenCalled());
  });

  it('shows both switches for a workflow with a schedule and a webhook', () => {
    mocks.webhooks = [{ dagName: 'etl', enabled: false }];
    renderCell(dagFile(cron));
    expect(
      screen.getByRole('switch', { name: 'Schedule for etl' })
    ).toBeVisible();
    expect(
      screen.getByRole('switch', { name: 'Webhook of etl' })
    ).not.toBeChecked();
  });

  it('shows a dash, with no switch, to a reader of a workflow with a webhook', () => {
    mocks.canManageWebhooks = false;
    mocks.webhooks = [{ dagName: 'etl', enabled: true }];
    renderCell(dagFile(cron));
    expect(screen.queryByTestId('webhook-switch')).toBeNull();
    expect(screen.getByTestId('webhook-cell').textContent).toBe('-');
  });
});

function Providers({ children }: { children: React.ReactNode }) {
  return (
    <UserPreferencesProvider>
      <I18nProvider>{children}</I18nProvider>
    </UserPreferencesProvider>
  );
}

describe('GroupScheduleCell', () => {
  // A request that throws, such as on a network error, has no error response.
  it('reports a request that fails without a response', async () => {
    const refresh = vi.fn();
    mocks.post.mockRejectedValueOnce(new Error('Failed to fetch'));
    const members = ['a', 'b'].map(
      (fileName) => ({ ...dagFile(cron), fileName }) as DAGFile
    );
    render(
      <GroupScheduleCell name="nightly" members={members} refresh={refresh} />,
      { wrapper: Providers }
    );

    await userEvent.click(
      screen.getByRole('switch', { name: 'Schedules of group nightly' })
    );
    await userEvent.click(screen.getByRole('button', { name: 'Disable' }));

    await waitFor(() =>
      expect(mocks.showError).toHaveBeenCalledWith(
        'Failed to fetch',
        expect.any(String)
      )
    );
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(refresh).toHaveBeenCalledOnce();
  });
});
