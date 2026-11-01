// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Stream, type components } from '@/api/v1/schema';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import LogViewer from '../LogViewer';

vi.mock('@/hooks/api', () => ({ useClient: vi.fn(), useQuery: vi.fn() }));
vi.mock('../StepLog', () => ({
  default: ({
    onStreamShown,
  }: {
    onStreamShown?: (stream: string) => void;
  }) => (
    <button type="button" onClick={() => onStreamShown?.('stderr')}>
      Show stderr
    </button>
  ),
}));
vi.mock('../ExecutionLog', () => ({ default: () => null }));
vi.mock('../ForeachBodyStepLog', () => ({ default: () => null }));

type DAGRunDetails = components['schemas']['DAGRunDetails'];

const rootRun = {
  name: 'etl',
  dagRunId: 'run-1',
  sourceFileName: 'etl-pipeline',
} as DAGRunDetails;

function renderViewer(props: Partial<React.ComponentProps<typeof LogViewer>>) {
  return render(
    <RemoteNodeProvider remoteNode="edge">
      <LogViewer
        isOpen
        onClose={vi.fn()}
        logType="step"
        dagName="etl"
        dagRunId="run-1"
        stepName="build"
        dagRun={rootRun}
        {...props}
      />
    </RemoteNodeProvider>
  );
}

function openedURL(): URL {
  fireEvent.click(screen.getByTitle('Open in new tab'));
  const target = vi.mocked(window.open).mock.calls[0]?.[0];
  return new URL(String(target), 'http://localhost');
}

describe('LogViewer', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('opens a step log in a new tab on its file, stream, and node', () => {
    vi.spyOn(window, 'open').mockReturnValue(null);
    renderViewer({ stream: Stream.stderr });

    const url = openedURL();
    expect(url.pathname).toBe('/dags/etl-pipeline/log');
    expect(Object.fromEntries(url.searchParams)).toEqual({
      remoteNode: 'edge',
      dagRunId: 'run-1',
      step: 'build',
      stream: 'stderr',
    });
  });

  it('opens the stream the step log switched to', () => {
    vi.spyOn(window, 'open').mockReturnValue(null);
    renderViewer({});

    fireEvent.click(screen.getByRole('button', { name: 'Show stderr' }));

    expect(openedURL().searchParams.get('stream')).toBe('stderr');
  });

  it('addresses a sub-run log through its root run', () => {
    vi.spyOn(window, 'open').mockReturnValue(null);
    renderViewer({
      dagRunId: 'child-1',
      dagRun: {
        ...rootRun,
        name: 'child',
        dagRunId: 'child-1',
        rootDAGRunName: 'etl',
        rootDAGRunId: 'run-1',
      } as DAGRunDetails,
    });

    const url = openedURL();
    expect(url.pathname).toBe('/dags/etl-pipeline/log');
    expect(Object.fromEntries(url.searchParams)).toMatchObject({
      dagRunId: 'run-1',
      dagRunName: 'etl',
      subDAGRunId: 'child-1',
      step: 'build',
    });
  });

  it('opens the run log in a new tab', () => {
    vi.spyOn(window, 'open').mockReturnValue(null);
    renderViewer({ logType: 'execution', stepName: undefined });

    expect(openedURL().pathname).toBe('/dags/etl-pipeline/dagRun-log');
  });

  // The full-page log has no route for a foreach body step.
  it('offers no new tab for a foreach body step log', () => {
    renderViewer({ foreach: { stepName: 'fan-out', item: '0' } });

    expect(screen.queryByTitle('Open in new tab')).not.toBeInTheDocument();
  });
});
