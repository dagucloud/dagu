// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel, TriggerType } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import ArtifactsTab from '@/features/dags/components/artifacts/ArtifactsTab';
import { ArtifactListModal } from '../ArtifactListModal';
import { useClient } from '@/hooks/api';

vi.mock('@/hooks/api', () => ({ useClient: vi.fn() }));

vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: vi.fn(),
}));

vi.mock('@/features/dags/components/artifacts/ArtifactsTab', () => ({
  default: vi.fn(),
}));

const appBarValue = {
  title: 'Cockpit',
  setTitle: vi.fn(),
  remoteNodes: ['local', 'edge'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'edge',
  selectRemoteNode: vi.fn(),
};

const run = {
  dagRunId: 'run-1',
  name: 'artifact-dag',
  status: Status.Success,
  statusLabel: StatusLabel.succeeded,
  artifactsAvailable: true,
  autoRetryCount: 0,
  triggerType: TriggerType.manual,
  queuedAt: '',
  scheduleTime: '',
  startedAt: '2026-03-16T00:00:00Z',
  finishedAt: '2026-03-16T00:01:00Z',
};

const details = {
  ...run,
  rootDAGRunName: 'artifact-dag',
  rootDAGRunId: 'run-1',
  nodes: [],
};

afterEach(() => {
  vi.clearAllMocks();
});

beforeEach(() => {
  vi.mocked(ArtifactsTab).mockImplementation(({ dagRun }) => (
    <div data-testid="artifact-preview-tab">{dagRun.name}</div>
  ));
});

describe('ArtifactListModal', () => {
  it('tabs into preview links before wrapping to the close button', async () => {
    vi.mocked(ArtifactsTab).mockImplementation(() => (
      <div role="region" aria-label="Preview" tabIndex={-1}>
        <a href="#artifact-link">Artifact link</a>
      </div>
    ));
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: details,
      isLoading: false,
      isValidating: false,
    } as never);
    const user = userEvent.setup();
    render(
      <AppBarContext.Provider value={appBarValue}>
        <ArtifactListModal run={run} isOpen onClose={() => {}} />
      </AppBarContext.Provider>
    );
    const close = screen.getByTitle('Close artifact preview');
    await waitFor(() => expect(close).toHaveFocus());
    screen.getByRole('region', { name: 'Preview' }).focus();
    await user.tab();
    expect(screen.getByRole('link', { name: 'Artifact link' })).toHaveFocus();
    await user.tab();
    expect(close).toHaveFocus();
  });

  it('loads DAG-run details and renders the shared artifact preview tab', () => {
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: details,
      error: null,
      isLoading: false,
      isValidating: false,
      refresh: vi.fn(),
    } as never);

    render(
      <AppBarContext.Provider value={appBarValue}>
        <ArtifactListModal run={run} isOpen={true} onClose={() => {}} />
      </AppBarContext.Provider>
    );

    expect(screen.getByTestId('artifact-preview-tab')).toHaveTextContent(
      'artifact-dag'
    );
    expect(useBoundedDAGRunDetails).toHaveBeenCalledWith({
      target: {
        remoteNode: 'edge',
        name: 'artifact-dag',
        dagRunId: 'run-1',
      },
      enabled: true,
      pollIntervalMs: 2000,
    });
    expect(ArtifactsTab).toHaveBeenCalledWith(
      expect.objectContaining({
        dagRun: details,
        artifactEnabled: true,
        className: 'h-full',
        fillHeight: true,
      }),
      undefined
    );
  });

  it('returns from preview before closing and traps only tab stops', async () => {
    const { default: RealArtifactsTab } = await vi.importActual<
      typeof import('@/features/dags/components/artifacts/ArtifactsTab')
    >('@/features/dags/components/artifacts/ArtifactsTab');
    vi.mocked(ArtifactsTab).mockImplementation(RealArtifactsTab);
    vi.mocked(useClient).mockReturnValue({
      GET: vi.fn(
        async (
          endpoint: string,
          init?: { params?: { query?: { path?: string } } }
        ) => {
          if (endpoint.endsWith('/artifacts')) {
            return {
              data: {
                items: ['a.txt', 'b.txt'].map((path) => ({
                  name: path,
                  path,
                  type: 'file',
                  size: 1,
                })),
              },
            };
          }
          const path = init?.params?.query?.path;
          return {
            data: { path, name: path, kind: 'text', content: path, size: 1 },
          };
        }
      ),
    } as never);
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: details,
      isLoading: false,
      isValidating: false,
      refresh: vi.fn(),
    } as never);
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(
      <AppBarContext.Provider value={appBarValue}>
        <ArtifactListModal run={run} isOpen onClose={onClose} />
      </AppBarContext.Provider>
    );
    const close = screen.getByTitle('Close artifact preview');
    await waitFor(() => expect(close).toHaveFocus());
    await screen.findByRole('treeitem', { name: 'a.txt' });
    await user.tab();
    expect(screen.getByTitle('Reload artifacts')).toHaveFocus();
    await user.tab();
    expect(screen.getByRole('treeitem', { name: 'a.txt' })).toHaveFocus();
    await user.keyboard('j{Enter}');
    expect(screen.getByRole('region', { name: 'b.txt' })).toHaveFocus();
    await user.tab();
    expect(close).toHaveFocus();
    await user.click(screen.getByRole('treeitem', { name: 'b.txt' }));
    await user.keyboard('{Enter}');
    await user.tab({ shift: true });
    expect(screen.getByRole('button', { name: 'Download' })).toHaveFocus();
    await user.click(screen.getByRole('treeitem', { name: 'b.txt' }));
    await user.keyboard('{Enter}');
    await user.keyboard('{Escape}');
    expect(screen.getByRole('treeitem', { name: 'b.txt' })).toHaveFocus();
    expect(onClose).not.toHaveBeenCalled();

    await user.tab({ shift: true });
    await user.tab({ shift: true });
    expect(close).toHaveFocus();
    await user.tab({ shift: true });
    expect(screen.getByRole('button', { name: 'Download' })).toHaveFocus();
    await user.tab();
    expect(close).toHaveFocus();
    await user.click(screen.getByRole('treeitem', { name: 'b.txt' }));
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledOnce();
  });
});
