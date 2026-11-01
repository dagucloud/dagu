// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel, Stream } from '@/api/v1/schema';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import ExecutionLog from '@/features/dags/components/dag-execution/ExecutionLog';
import StepLog from '@/features/dags/components/dag-execution/StepLog';
import type { ApiDAGRunDetails, DAGPageData } from '../../contracts';
import { LogPopoutPage, type LogPopoutPageProps } from '../LogPopoutPage';

const h = vi.hoisted(() => ({ runDags: true }));

vi.mock('@/features/dags/components/dag-execution/StepLog', () => ({
  default: vi.fn(() => <div data-testid="step-log" />),
}));

vi.mock('@/features/dags/components/dag-execution/ExecutionLog', () => ({
  default: vi.fn(() => <div data-testid="execution-log" />),
}));

vi.mock('@/features/dag-runs/components/common/RunMetaLine', () => ({
  RunMetaLine: ({ runHref }: { runHref?: string }) => (
    <span data-testid="run-meta" data-href={runHref} />
  ),
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { runDags: h.runDags }, basePath: '' }),
}));

vi.mock('@/hooks/api', () => ({ useClient: () => ({}) }));

vi.mock('@/components/ui/error-modal', async (importOriginal) => ({
  ...(await importOriginal<object>()),
  useErrorModal: () => ({ showError: vi.fn() }),
}));

vi.mock('@/components/ui/simple-toast', async (importOriginal) => ({
  ...(await importOriginal<object>()),
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));

const stepLog = vi.mocked(StepLog);
const executionLog = vi.mocked(ExecutionLog);

function node(name: string) {
  return { step: { name }, status: 4 } as never;
}

const latestRun = {
  name: 'release',
  dagRunId: 'run-latest',
  rootDAGRunName: 'release',
  rootDAGRunId: 'run-latest',
  status: Status.Success,
  statusLabel: StatusLabel.succeeded,
  nodes: [node('build')],
} as unknown as ApiDAGRunDetails;

const pinnedRun = {
  ...latestRun,
  dagRunId: 'run-7',
  rootDAGRunId: 'run-7',
  nodes: [node('build'), node('deploy')],
} as unknown as ApiDAGRunDetails;

const subRun = {
  name: 'child',
  dagRunId: 'sub-3',
  rootDAGRunName: 'release',
  rootDAGRunId: 'run-7',
  parentDAGRunName: 'middle',
  parentDAGRunId: 'sub-2',
  status: Status.Success,
  statusLabel: StatusLabel.succeeded,
  nodes: [node('compile')],
} as unknown as ApiDAGRunDetails;

const dagData = {
  dag: { name: 'release' },
  latestDAGRun: latestRun,
} as unknown as DAGPageData;

function renderPopout(
  url: string,
  props: Partial<LogPopoutPageProps> = {}
): { refresh: ReturnType<typeof vi.fn> } {
  const refresh = vi.fn(() => Promise.resolve());
  const tab = new URL(url, 'http://x').pathname.split('/')[3];
  render(
    <MemoryRouter initialEntries={[url]}>
      <RemoteNodeProvider remoteNode="local">
        <Routes>
          <Route
            path="/dags/:fileName/:tab"
            element={
              <LogPopoutPage
                fileName="release.yaml"
                dagData={dagData}
                tab={tab}
                refresh={refresh}
                {...props}
              />
            }
          />
        </Routes>
      </RemoteNodeProvider>
    </MemoryRouter>
  );
  return { refresh };
}

function lastStepLogProps() {
  return stepLog.mock.lastCall?.[0];
}

beforeEach(() => {
  vi.clearAllMocks();
  h.runDags = true;
});

// parity:log-fullpage
// parity:shell-log-tabs
describe('LogPopoutPage', () => {
  // parity:shell-stream-param
  it('shows the step log on stderr with the node of the run', () => {
    renderPopout(
      '/dags/release.yaml/log?dagRunId=run-7&step=deploy&stream=stderr',
      {
        dagRun: pinnedRun,
      }
    );

    expect(screen.getByTestId('step-log')).toBeInTheDocument();
    expect(lastStepLogProps()).toMatchObject({
      dagName: 'release',
      dagRunId: 'run-7',
      stepName: 'deploy',
      stream: Stream.stderr,
      dagRun: pinnedRun,
      node: { step: { name: 'deploy' } },
    });
  });

  it.each([
    ['another value', '&stream=both'],
    ['stdout', '&stream=stdout'],
    ['no value', ''],
  ])('shows stdout for %s', (_, stream) => {
    renderPopout(`/dags/release.yaml/log?dagRunId=run-7&step=build${stream}`, {
      dagRun: pinnedRun,
    });

    expect(lastStepLogProps()?.stream).toBe(Stream.stdout);
  });

  it('explains a link without a step and links to the workflow', () => {
    renderPopout('/dags/release.yaml/log?dagRunId=run-7&workspace=ops', {
      dagRun: pinnedRun,
    });

    expect(stepLog).not.toHaveBeenCalled();
    expect(
      screen.getByText(
        'This link names no step. Open a step log from the workflow.'
      )
    ).toBeVisible();
    expect(
      screen.getByRole('link', { name: 'Open the workflow' })
    ).toHaveAttribute(
      'href',
      '/dags/release.yaml?remoteNode=local&run=run-7&workspace=ops'
    );
  });

  it('shows the scheduler log of the run on dagRun-log', () => {
    renderPopout('/dags/release.yaml/dagRun-log?dagRunId=run-7', {
      dagRun: pinnedRun,
    });

    expect(screen.getByTestId('execution-log')).toBeInTheDocument();
    expect(stepLog).not.toHaveBeenCalled();
    expect(executionLog.mock.lastCall?.[0]).toMatchObject({
      name: 'release',
      dagRunId: 'run-7',
      dagRun: pinnedRun,
    });
    expect(screen.getByText('Run log')).toBeVisible();
  });

  it('uses the latest run without a dagRunId', () => {
    renderPopout('/dags/release.yaml/log?step=build');

    expect(lastStepLogProps()).toMatchObject({
      dagRunId: 'run-latest',
      dagRun: latestRun,
      node: { step: { name: 'build' } },
    });
  });

  it('shows a loading state while the named run loads', () => {
    renderPopout('/dags/release.yaml/log?dagRunId=run-7&step=build', {
      runLoading: true,
    });

    expect(screen.getByRole('status', { name: 'Loading...' })).toBeVisible();
    expect(stepLog).not.toHaveBeenCalled();
  });

  it('says so when the workflow has never run', () => {
    renderPopout('/dags/release.yaml/dagRun-log', {
      dagData: { ...dagData, latestDAGRun: undefined } as never,
    });

    expect(screen.getByText('No runs yet')).toBeVisible();
    expect(executionLog).not.toHaveBeenCalled();
  });

  it('treats a latest run without an ID as no run', () => {
    renderPopout('/dags/release.yaml/log?step=build', {
      dagData: { ...dagData, latestDAGRun: {} } as never,
    });

    expect(screen.getByText('No runs yet')).toBeVisible();
    expect(stepLog).not.toHaveBeenCalled();
    expect(
      screen.queryByRole('link', { name: 'Open in workflow' })
    ).not.toBeInTheDocument();
  });

  // parity:header-actions-visibility
  describe('root run bar', () => {
    it('shows the run actions, Refresh and Open in workflow', async () => {
      const { refresh } = renderPopout(
        '/dags/release.yaml/log?dagRunId=run-7&step=deploy',
        { dagRun: pinnedRun }
      );

      expect(screen.getByRole('heading', { name: 'release' })).toBeVisible();
      // DAGRunActions offers Retry for a finished root run.
      expect(screen.getByRole('button', { name: /retry/i })).toBeVisible();
      expect(
        screen.getByRole('link', { name: 'Open in workflow' })
      ).toHaveAttribute(
        'href',
        '/dags/release.yaml?remoteNode=local&run=run-7&step=deploy&dock=log'
      );
      expect(screen.getByTestId('run-meta')).toHaveAttribute(
        'data-href',
        '/dags/release.yaml?remoteNode=local&run=run-7'
      );

      fireEvent.click(screen.getByRole('button', { name: /refresh/i }));
      await waitFor(() => expect(refresh).toHaveBeenCalled());
    });

    // The run ID opens the file of the workflow, whose name can differ from
    // the DAG name.
    it('links the run ID to the workflow file', () => {
      renderPopout('/dags/nightly/log?dagRunId=run-7&step=deploy', {
        fileName: 'nightly',
        dagRun: pinnedRun,
      });
      expect(screen.getByTestId('run-meta')).toHaveAttribute(
        'data-href',
        '/dags/nightly?remoteNode=local&run=run-7'
      );
    });

    it('opens the run log of the run in the workflow without a step', () => {
      renderPopout('/dags/release.yaml/dagRun-log?dagRunId=run-7', {
        dagRun: pinnedRun,
      });

      expect(
        screen.getByRole('link', { name: 'Open in workflow' })
      ).toHaveAttribute(
        'href',
        '/dags/release.yaml?remoteNode=local&run=run-7&dock=log'
      );
    });

    it('hides the run actions without the runDags permission', () => {
      h.runDags = false;

      renderPopout('/dags/release.yaml/log?dagRunId=run-7&step=deploy', {
        dagRun: pinnedRun,
      });

      expect(
        screen.queryByRole('button', { name: /retry/i })
      ).not.toBeInTheDocument();
      expect(
        screen.getByRole('link', { name: 'Open in workflow' })
      ).toBeVisible();
    });
  });

  // parity:shell-subdag-run-load
  // parity:header-subrun-breadcrumb
  // parity:run-header-breadcrumb
  // parity:run-page-subrun-deeplink
  // parity:header-actions-visibility
  describe('sub-run bar', () => {
    const url =
      '/dags/child.yaml/log?dagRunId=run-7&dagRunName=release&subDAGRunId=sub-3&step=compile';

    it('shows the sub-run with its root and parent links', () => {
      renderPopout(url, { dagRun: subRun });

      expect(screen.getByRole('heading', { name: 'child' })).toBeVisible();
      expect(screen.getByText('Root:')).toBeVisible();
      expect(screen.getByRole('link', { name: 'release' })).toHaveAttribute(
        'href',
        buildDAGRunPageURL({
          rootDAGRunName: 'release',
          rootDAGRunId: 'run-7',
          remoteNode: 'local',
        })
      );
      expect(screen.getByRole('link', { name: 'middle' })).toHaveAttribute(
        'href',
        buildDAGRunPageURL({
          rootDAGRunName: 'release',
          rootDAGRunId: 'run-7',
          remoteNode: 'local',
          subDAGRunId: 'sub-2',
        })
      );
      // The run ID opens the sub-run over its root run.
      expect(screen.getByTestId('run-meta')).toHaveAttribute(
        'data-href',
        '/dags/release?remoteNode=local&run=run-7&subRun=sub-3'
      );
      expect(lastStepLogProps()).toMatchObject({
        dagName: 'child',
        dagRunId: 'sub-3',
        stepName: 'compile',
        dagRun: subRun,
        node: { step: { name: 'compile' } },
      });
    });

    it('hides Start, Rename, Delete, Pin and the run actions', () => {
      renderPopout(url, { dagRun: subRun });

      for (const name of [
        /start/i,
        /rename/i,
        /delete/i,
        /pin/i,
        /retry/i,
        /stop/i,
      ]) {
        expect(screen.queryByRole('button', { name })).not.toBeInTheDocument();
      }
      expect(
        screen.queryByRole('link', { name: 'Open in workflow' })
      ).not.toBeInTheDocument();
    });

    it('leaves out a parent that is the root', () => {
      renderPopout(url, {
        dagRun: {
          ...subRun,
          parentDAGRunName: 'release',
          parentDAGRunId: 'run-7',
        } as ApiDAGRunDetails,
      });

      expect(screen.getAllByRole('link', { name: 'release' })).toHaveLength(1);
      expect(
        screen.queryByRole('link', { name: 'middle' })
      ).not.toBeInTheDocument();
    });
  });
});
