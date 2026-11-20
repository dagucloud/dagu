// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ScheduleKind, type components } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { ScheduleCell, WebhookCell } from '../ActiveCell';

type DAGFile = components['schemas']['DAGFile'];

const mocks = vi.hoisted(() => ({
  canManageWebhooks: true,
  runDags: true,
  webhooks: [] as Array<{ dagName: string; enabled: boolean }>,
  post: vi.fn(),
  mutate: vi.fn(),
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
  useErrorModal: () => ({ showError: vi.fn() }),
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
});

describe('ScheduleCell and WebhookCell', () => {
  it('shows the schedule switch and no webhook switch for a scheduled workflow', () => {
    renderCell(dagFile(cron));
    expect(
      screen.getByRole('switch', { name: 'Schedule for etl' })
    ).toBeVisible();
    expect(screen.queryByTestId('webhook-switch')).toBeNull();
  });

  it('shows a dash in each column for a workflow that has neither', () => {
    renderCell(dagFile([]));
    expect(screen.getByTestId('schedule-cell').textContent).toBe('-');
    expect(screen.getByTestId('webhook-cell').textContent).toBe('-');
  });

  it('shows the restart policy and the switch of a service DAG', () => {
    const dag = dagFile([]);
    dag.dag = {
      ...dag.dag,
      restartPolicy: 'always',
      restartWaitSec: 1,
    } as never;
    renderCell(dag);
    expect(screen.getByTestId('schedule-cell').textContent).toContain(
      'Restart: always · 1s'
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

  it('gives a reader the marker of the webhook without a switch', () => {
    mocks.canManageWebhooks = false;
    mocks.webhooks = [{ dagName: 'etl', enabled: true }];
    renderCell(dagFile(cron));
    expect(screen.queryByTestId('webhook-switch')).toBeNull();
  });
});
