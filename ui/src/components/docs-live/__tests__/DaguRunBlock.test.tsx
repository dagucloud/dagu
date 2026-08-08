// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const postMock = vi.fn();
const state = {
  live: null as unknown,
  runDags: true,
  canExecute: true,
};

vi.mock('../context', async () => {
  const actual =
    await vi.importActual<typeof import('../context')>('../context');
  return {
    ...actual,
    useDocLive: () => state.live,
  };
});

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: postMock }),
  useQuery: () => ({ data: undefined }),
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { runDags: state.runDags } }),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanExecuteForWorkspace: () => state.canExecute,
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));

import { DaguRunBlock, serializeRunParams } from '../DaguRunBlock';

const foundLive = {
  workspace: 'ops',
  remoteNode: 'local',
  registerRef: () => () => {},
  lookup: () => ({
    state: 'found',
    fileName: 'etl.yaml',
    dagName: 'daily-etl',
    latestDAGRun: { status: 4, statusLabel: 'finished' },
    suspended: false,
  }),
};

const BLOCK = `dag: daily-etl
label: Retry today's ETL
params:
  DATE: "2026-08-08"
confirm: Re-runs the ETL.
`;

function renderBlock(source = BLOCK) {
  return render(
    <MemoryRouter>
      <DaguRunBlock source={source} />
    </MemoryRouter>
  );
}

beforeEach(() => {
  postMock.mockReset().mockResolvedValue({ data: { dagRunId: 'run-1' } });
  state.live = foundLive;
  state.runDags = true;
  state.canExecute = true;
});

describe('DaguRunBlock', () => {
  it('rejects unknown keys at render time', () => {
    renderBlock('dag: daily-etl\nbogus: true\n');
    expect(screen.getByText('Unknown dagu-run key: bogus')).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('rejects an invalid mode', () => {
    renderBlock('dag: daily-etl\nmode: later\n');
    expect(screen.getByText('mode must be start or enqueue')).toBeInTheDocument();
  });

  it('renders an inert summary without a provider', () => {
    state.live = null;
    renderBlock();
    expect(screen.getByText("Retry today's ETL")).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('hides the run button when running is disabled server-wide', () => {
    state.runDags = false;
    renderBlock();
    expect(
      screen.queryByRole('button', { name: /run/i })
    ).not.toBeInTheDocument();
  });

  it('disables the run button below operator access', () => {
    state.canExecute = false;
    renderBlock();
    expect(screen.getByRole('button', { name: /run/i })).toBeDisabled();
  });

  it('confirms then posts the exact start request', async () => {
    const user = userEvent.setup();
    renderBlock();

    await user.click(screen.getByRole('button', { name: /run/i }));
    await user.click(screen.getByRole('button', { name: 'Run' }));

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
    expect(postMock).toHaveBeenCalledWith('/dags/{fileName}/start', {
      params: {
        path: { fileName: 'etl.yaml' },
        query: { remoteNode: 'local' },
      },
      body: { params: '[{"DATE":"2026-08-08"}]' },
    });
    expect(await screen.findByText('View run →')).toBeInTheDocument();
  });

  it('uses the enqueue endpoint and singleton flag when configured', async () => {
    const user = userEvent.setup();
    renderBlock('dag: daily-etl\nmode: enqueue\nsingleton: true\n');

    await user.click(screen.getByRole('button', { name: /enqueue/i }));
    await user.click(screen.getByRole('button', { name: 'Enqueue' }));

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
    expect(postMock).toHaveBeenCalledWith('/dags/{fileName}/enqueue', {
      params: {
        path: { fileName: 'etl.yaml' },
        query: { remoteNode: 'local' },
      },
      body: { params: '', singleton: true },
    });
  });
});

describe('serializeRunParams', () => {
  it('produces the run dialog JSON-array convention preserving order', () => {
    expect(serializeRunParams({ B: '2', A: '1' })).toBe('[{"B":"2"},{"A":"1"}]');
    expect(serializeRunParams({})).toBe('');
  });
});
