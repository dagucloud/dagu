// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import {
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { DAGContext } from '../../../contexts/DAGContext';
import NodeStatusTableRow from '../NodeStatusTableRow';

const configMock = vi.hoisted(() => ({
  runDags: true,
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({
    PATCH: vi.fn(),
    POST: vi.fn(),
  }),
  useQuery: vi.fn(),
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({
    permissions: {
      runDags: configMock.runDags,
    },
  }),
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({
    showError: vi.fn(),
  }),
}));

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const dagRun = {
  name: 'example',
  dagRunId: 'run-1',
  status: Status.Success,
  statusLabel: StatusLabel.succeeded,
  startedAt: '',
  finishedAt: '',
  autoRetryCount: 0,
} as components['schemas']['DAGRunDetails'];

const mockedUseQuery = vi.mocked(
  useQuery as unknown as (path: unknown, init: unknown) => unknown
);

const stepLogPath = '/dag-runs/{name}/{dagRunId}/steps/{stepName}/log';

describe('NodeStatusTableRow', () => {
  beforeEach(() => {
    configMock.runDags = true;
    mockedUseQuery.mockImplementation((path, init) => ({
      data:
        path === stepLogPath && init
          ? { content: 'Deploying production\n' }
          : undefined,
      isLoading: false,
      error: undefined,
      isValidating: false,
      mutate: vi.fn(),
    }));
  });

  it('shows log step messages in the status table without opening step logs', () => {
    const node = {
      step: {
        name: 'announce',
        executorConfig: {
          type: 'log',
          config: {
            message: 'Deploying ${ENVIRONMENT}',
          },
        },
      },
      status: NodeStatus.Success,
      statusLabel: NodeStatusLabel.succeeded,
      stdout: '/tmp/announce.out',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 1,
    } as components['schemas']['Node'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="example.yaml"
                  dagRun={dagRun}
                  view="desktop"
                />
              </tbody>
            </table>
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    const message = screen.getByLabelText('Log message: Deploying production');
    expect(message).toBeInTheDocument();
    expect(message.querySelector('svg')).not.toBeInTheDocument();
    expect(
      screen.queryByText('Deploying ${ENVIRONMENT}')
    ).not.toBeInTheDocument();
    expect(screen.getByText('stdout')).toBeInTheDocument();
  });

  it('falls back to the configured log message when stdout cannot be loaded', () => {
    mockedUseQuery.mockImplementation((path, init) => ({
      data: undefined,
      isLoading: false,
      error:
        path === stepLogPath && init ? new Error('log not found') : undefined,
      isValidating: false,
      mutate: vi.fn(),
    }));

    const node = {
      step: {
        name: 'announce',
        executorConfig: {
          type: 'log',
          config: {
            message: 'Deploying ${ENVIRONMENT}',
          },
        },
      },
      status: NodeStatus.Success,
      statusLabel: NodeStatusLabel.succeeded,
      stdout: '/tmp/announce.out',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 1,
    } as components['schemas']['Node'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="example.yaml"
                  dagRun={dagRun}
                  view="desktop"
                />
              </tbody>
            </table>
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(
      screen.getByLabelText('Log message: Deploying ${ENVIRONMENT}')
    ).toBeInTheDocument();
    expect(screen.queryByText('Loading log output...')).not.toBeInTheDocument();
  });

  it('hides step retry controls when DAG runs are disabled', () => {
    configMock.runDags = false;

    const node = {
      step: {
        name: 'build',
      },
      status: NodeStatus.Success,
      statusLabel: NodeStatusLabel.succeeded,
      stdout: '',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 1,
    } as components['schemas']['Node'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="example.yaml"
                  dagRun={dagRun}
                  view="desktop"
                />
              </tbody>
            </table>
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(screen.queryByTitle('Retry from this step')).not.toBeInTheDocument();
  });
});
