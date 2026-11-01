// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ApiDAGDetails } from '../../contracts';
import { TriggerChip } from '../TriggerChip';

const mocks = vi.hoisted(() => ({ GET: vi.fn() }));

vi.mock('@/hooks/api', async () => {
  const { createQueryHook } = await import('swr-openapi');
  return { useQuery: createQueryHook({ GET: mocks.GET } as never, '/') };
});
vi.mock('../../data', () => ({
  useWorkspacePermissions: () => ({ canManageWebhooks: true }),
}));
vi.mock('../../canvas/WorkspaceEditorContext', () => ({
  useWorkspaceEditor: () => ({ handle: null, state: null }),
}));

const WEBHOOK_PATH = '/dags/{fileName}/webhook';

const scheduled = {
  name: 'etl',
  schedule: [
    { kind: 'cron', expression: '0 2 * * *' },
    { kind: 'cron', expression: '30 * * * *' },
  ],
} as ApiDAGDetails;

function answerWebhook(result: unknown) {
  mocks.GET.mockImplementation(async (path: string) =>
    path === WEBHOOK_PATH ? result : { error: { message: 'unexpected' } }
  );
}

function renderChip(dag: ApiDAGDetails = scheduled) {
  const element = <TriggerChip fileName="etl" dag={dag} workspaceName={null} />;
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <MemoryRouter initialEntries={['/dags/etl']}>
        <Routes>
          <Route path="/dags/:fileName" element={element} />
        </Routes>
      </MemoryRouter>
    </SWRConfig>
  );
}

beforeEach(() => {
  mocks.GET.mockReset();
  answerWebhook({ error: { code: 'not_found', message: 'not found' } });
});

// The bar is a size container: TriggerChip shows its text in a bar of
// 1280px or more, and only its icon below. The text stays for the screen
// reader.
describe('TriggerChip in the bar', () => {
  // 'Webhook Disabled' shows an icon only below 1280px: the title gives the
  // state.
  it('names the webhook state in the title and keeps its text on one line', async () => {
    answerWebhook({ data: { enabled: false } });
    renderChip();
    const button = await screen.findByRole('button', {
      name: 'Webhook Disabled',
    });
    expect(button).toHaveClass('whitespace-nowrap');
    expect(button.querySelector('[data-chip-text]')).toHaveClass(
      'sr-only',
      '@7xl:not-sr-only'
    );
    expect(button.title).toBe('Webhook Disabled\nOpen webhook settings');
  });

  // text-success on the page background is 4.2:1 in light mode: the state
  // takes the muted text colour and a success dot.
  it('shows an enabled webhook with a dot and readable text', async () => {
    answerWebhook({ data: { enabled: true } });
    renderChip();
    const button = await screen.findByRole('button', {
      name: 'Webhook Enabled',
    });
    const state = screen.getByText('Enabled');
    expect(state).toHaveClass('text-muted-foreground');
    expect(state).not.toHaveClass('text-success');
    expect(
      button.querySelector('.bg-\\[var\\(--status-success\\)\\]')
    ).not.toBeNull();
  });
});
