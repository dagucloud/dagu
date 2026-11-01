// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from 'react-router-dom';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ApiDAGDetails } from '../../contracts';
import { TriggerChip } from '../TriggerChip';

const mocks = vi.hoisted(() => ({
  GET: vi.fn(),
  canManageWebhooks: true,
}));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return { useQuery: createQueryHook({ GET: mocks.GET } as never, '/') };
});
vi.mock('../../data', () => ({
  useWorkspacePermissions: () => ({
    canManageWebhooks: mocks.canManageWebhooks,
  }),
}));

const WEBHOOK_PATH = '/dags/{fileName}/webhook';
const NEXT_RUN = '2026-10-25T02:00:00Z';

const scheduled = {
  name: 'etl',
  schedule: [
    { kind: 'cron', expression: '0 2 * * *' },
    { kind: 'cron', expression: '30 * * * *' },
  ],
  nextRun: NEXT_RUN,
} as ApiDAGDetails;
const unscheduled = { name: 'etl' } as ApiDAGDetails;

function Probe() {
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output data-testid="location">
        {location.pathname + location.search}
      </output>
      <button type="button" onClick={() => navigate('/dags/etl')}>
        Close settings
      </button>
    </>
  );
}

function renderChip(dag: ApiDAGDetails, url = '/dags/etl') {
  const element = (
    <>
      <Probe />
      <TriggerChip fileName="etl" dag={dag} workspaceName={null} />
    </>
  );
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <MemoryRouter initialEntries={[url]}>
        <Routes>
          <Route path="/dags/:fileName" element={element} />
          <Route path="/dags/:fileName/:tab" element={element} />
        </Routes>
      </MemoryRouter>
    </SWRConfig>
  );
}

function location() {
  return screen.getByTestId('location').textContent;
}

function webhookCalls() {
  return mocks.GET.mock.calls.filter((call) => call[0] === WEBHOOK_PATH);
}

function answerWebhook(result: unknown) {
  mocks.GET.mockImplementation(async (path: string) =>
    path === WEBHOOK_PATH ? result : { error: { message: 'unexpected' } }
  );
}

const NOT_FOUND = { error: { code: 'not_found', message: 'not found' } };

beforeEach(() => {
  mocks.GET.mockReset();
  answerWebhook(NOT_FOUND);
  mocks.canManageWebhooks = true;
});

// The schedule shows on the Cron launcher and in the Workflow panel only.
describe('TriggerChip schedule', () => {
  it('does not show the schedule of the workflow', async () => {
    answerWebhook({ data: { id: 'w1', enabled: true } });
    renderChip(scheduled);
    await screen.findByRole('button', { name: 'Webhook Enabled' });
    expect(screen.queryByText(/0 2 \* \* \*/)).toBeNull();
    expect(screen.queryByText(/Next run/)).toBeNull();
  });

  it('renders nothing for a schedule without a webhook', async () => {
    renderChip(scheduled);
    await waitFor(() => expect(webhookCalls()).toHaveLength(1));
    await act(() => new Promise((resolve) => setTimeout(resolve, 20)));
    expect(screen.queryByTestId('ww-trigger-chip')).toBeNull();
  });
});

// parity:workflow-attributes
describe('TriggerChip webhook', () => {
  it('shows the webhook state and opens the Webhook launcher panel', async () => {
    answerWebhook({ data: { id: 'w1', enabled: true } });
    const user = userEvent.setup();
    renderChip(unscheduled);

    const button = await screen.findByRole('button', {
      name: 'Webhook Enabled',
    });
    expect(webhookCalls()).toEqual([
      [
        WEBHOOK_PATH,
        {
          params: { path: { fileName: 'etl' }, query: { remoteNode: 'local' } },
        },
      ],
    ]);
    await user.click(button);
    expect(location()).toBe('/dags/etl?panel=webhook');
  });

  it('shows a disabled webhook', async () => {
    answerWebhook({ data: { id: 'w1', enabled: false } });
    renderChip(unscheduled);
    expect(
      await screen.findByRole('button', { name: 'Webhook Disabled' })
    ).toBeVisible();
  });
});

describe('TriggerChip without triggers', () => {
  it('renders nothing when there is no schedule and no webhook', async () => {
    renderChip(unscheduled);
    await waitFor(() => expect(webhookCalls()).toHaveLength(1));
    // Let the 404 reach the component.
    await act(() => new Promise((resolve) => setTimeout(resolve, 20)));
    expect(screen.queryByTestId('ww-trigger-chip')).toBeNull();
  });

  it('renders nothing and reads nothing without webhook permission', () => {
    mocks.canManageWebhooks = false;
    renderChip(unscheduled);
    expect(screen.queryByTestId('ww-trigger-chip')).toBeNull();
    expect(webhookCalls()).toEqual([]);
  });
});
