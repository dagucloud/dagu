// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ScheduleKind, type components } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { ScheduleLine } from '../ScheduleSummary';

type DAGFile = components['schemas']['DAGFile'];

const mocks = vi.hoisted(() => ({
  canManageWebhooks: true,
  webhooks: [] as Array<{ dagName: string; enabled: boolean }>,
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => ({
    data:
      path === '/webhooks' && init ? { webhooks: mocks.webhooks } : undefined,
  }),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanManageWebhooks: () => mocks.canManageWebhooks,
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tz: 'UTC' }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const NOW = new Date('2026-10-26T10:07:30Z');

function dagFile(overrides: {
  schedule?: components['schemas']['Schedule'][];
  suspended?: boolean;
  nextRun?: string;
  fileName?: string;
}): DAGFile {
  return {
    fileName: overrides.fileName ?? 'etl',
    dag: { name: 'etl', schedule: overrides.schedule ?? [] },
    latestDAGRun: {},
    suspended: overrides.suspended ?? false,
    pinned: false,
    errors: [],
    nextRun: overrides.nextRun,
  } as unknown as DAGFile;
}

function renderLine(dag: DAGFile) {
  return render(
    <UserPreferencesProvider>
      <I18nProvider>
        <ScheduleLine dag={dag} />
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(NOW);
  mocks.canManageWebhooks = true;
  mocks.webhooks = [];
});

afterEach(() => {
  vi.useRealTimers();
});

describe('ScheduleLine', () => {
  it('shows the next run and the schedule that fires on one line', () => {
    renderLine(
      dagFile({
        schedule: [{ kind: ScheduleKind.cron, expression: '*/15 * * * *' }],
        nextRun: '2026-10-26T10:15:00Z',
      })
    );
    const line = screen.getByTestId('schedule-line');
    expect(line.textContent).toBe('in 8 minutes · every 15 min');
    expect(line.querySelector('[title="*/15 * * * *"]')).not.toBeNull();
    expect(screen.queryByText(/^\+\d/)).toBeNull();
    expect(line.className).toContain('h-6');
    expect(line.querySelector('.truncate')).not.toBeNull();
  });

  it('shows a plus badge and lists every schedule in its tooltip', async () => {
    const user = userEvent.setup();
    renderLine(
      dagFile({
        schedule: [
          { kind: ScheduleKind.cron, expression: '0 9 * * 1-5' },
          { kind: ScheduleKind.cron, expression: '*/30 * * * *' },
          { kind: ScheduleKind.cron, expression: '0 2 1 * *' },
        ],
        nextRun: '2026-10-26T10:30:00Z',
      })
    );
    const badge = screen.getByRole('button', { name: 'Show all 3 schedules' });
    expect(badge.textContent).toBe('+2');

    await user.hover(badge);
    const [list] = await screen.findAllByTestId('schedule-list');
    if (!list) throw new Error('missing schedule list');
    const rows = Array.from(list.querySelectorAll('li'));
    expect(rows).toHaveLength(3);
    expect(list.textContent).toContain('*/30 * * * *');
    expect(list.textContent).toContain('0 9 * * 1-5');
    expect(list.textContent).toContain('0 2 1 * *');
    // The soonest one is first and marked.
    const text = rows.map((row) => row.textContent ?? '');
    expect(text[0]).toContain('Next');
    expect(text[0]).toContain('every 30 min');
    expect(text[1]).not.toContain('Next');
    expect(text[2]).not.toContain('Next');
  });

  // The row that holds the line opens the workflow on a click.
  it('keeps a click in the schedule list from the row', async () => {
    const user = userEvent.setup();
    const openRow = vi.fn();
    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <div onClick={openRow}>
            <ScheduleLine
              dag={dagFile({
                schedule: [
                  { kind: ScheduleKind.cron, expression: '0 9 * * 1-5' },
                  { kind: ScheduleKind.cron, expression: '*/30 * * * *' },
                ],
              })}
            />
          </div>
        </I18nProvider>
      </UserPreferencesProvider>
    );

    await user.hover(
      screen.getByRole('button', { name: 'Show all 2 schedules' })
    );
    const [list] = await screen.findAllByTestId('schedule-list');
    await user.click(list!);

    expect(openRow).not.toHaveBeenCalled();
  });

  it('shows the schedule muted with Suspended instead of a time', () => {
    renderLine(
      dagFile({
        schedule: [{ kind: ScheduleKind.cron, expression: '0 * * * *' }],
        suspended: true,
        nextRun: '2026-10-26T11:00:00Z',
      })
    );
    const line = screen.getByTestId('schedule-line');
    expect(line.textContent).toBe('hourly · Suspended');
    expect(line.querySelector('.text-muted-foreground')).not.toBeNull();
  });

  // The server leaves out the next run when the scheduler plans none, as
  // while it is paused: the cron expression must not stand in for it.
  it('shows no upcoming run when the scheduler plans none', () => {
    renderLine(
      dagFile({
        schedule: [{ kind: ScheduleKind.cron, expression: '*/15 * * * *' }],
        nextRun: undefined,
      })
    );
    expect(screen.getByTestId('schedule-line').textContent).toBe(
      'No upcoming run · every 15 min'
    );
  });

  it('shows No schedule without a schedule', () => {
    renderLine(dagFile({}));
    expect(screen.getByTestId('schedule-line').textContent).toBe('No schedule');
  });
});

describe('WebhookIndicator in the schedule line', () => {
  it('shows the icon only for a workflow that has a webhook', () => {
    mocks.webhooks = [{ dagName: 'etl', enabled: true }];
    const { unmount } = renderLine(
      dagFile({
        schedule: [{ kind: ScheduleKind.cron, expression: '0 * * * *' }],
      })
    );
    expect(screen.getByRole('img', { name: 'Webhook enabled' })).toBeTruthy();
    unmount();

    renderLine(
      dagFile({
        fileName: 'other',
        schedule: [{ kind: ScheduleKind.cron, expression: '0 * * * *' }],
      })
    );
    expect(screen.queryByTestId('webhook-indicator')).toBeNull();
  });

  it('names a disabled webhook', () => {
    mocks.webhooks = [{ dagName: 'etl', enabled: false }];
    renderLine(dagFile({}));
    expect(screen.getByRole('img', { name: 'Webhook disabled' })).toBeTruthy();
  });

  it('shows nothing when the instance does not give the webhook list', () => {
    mocks.canManageWebhooks = false;
    mocks.webhooks = [{ dagName: 'etl', enabled: true }];
    renderLine(dagFile({}));
    expect(screen.queryByTestId('webhook-indicator')).toBeNull();
  });
});
