// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status, StatusLabel } from '@/api/v1/schema';
import { RunStatusChip } from '../RunStatusChip';

const h = vi.hoisted(() => ({
  nodes: [] as unknown[],
  steps: [] as unknown[],
  service: true,
  probe: 'data' as 'data' | 'loading' | 'error',
  query: vi.fn(),
  options: undefined as unknown,
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown, options: unknown) => {
    h.query(path, init);
    h.options = options;
    if (init === null) return { data: undefined };
    if (path === '/dags/{fileName}') {
      if (h.probe === 'loading') return { data: undefined };
      if (h.probe === 'error')
        return { data: undefined, error: new Error('x') };
      return {
        data: {
          dag: {
            steps: h.steps,
            restartPolicy: h.service ? 'always' : undefined,
          },
        },
      };
    }
    return {
      data: { dagRunDetails: { dagRunId: 'r1', status: 1, nodes: h.nodes } },
    };
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const forever = { repeatPolicy: { repeat: 'while', interval: 10 } };
const node = (status: NodeStatus, step: object = {}) => ({ status, step });
const detailsCalls = () =>
  h.query.mock.calls.filter(
    ([path, init]) => path === '/dag-runs/{name}/{dagRunId}' && init !== null
  );

beforeEach(() => {
  // A running status reads the reduced-motion preference.
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  });
  h.nodes = [];
  h.steps = [];
  h.service = true;
  h.probe = 'data';
  h.query.mockClear();
});

// The summary of a run in a list has no steps: a running run reads its details.
describe('RunStatusChip', () => {
  it('reads Healthy for a running run of a service DAG with a continuous step', () => {
    h.steps = [forever];
    h.nodes = [node(NodeStatus.Running, forever)];
    render(
      <RunStatusChip name="always" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    expect(screen.getByText('Healthy')).toBeVisible();
    expect(h.query).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}',
      expect.objectContaining({
        params: {
          path: { name: 'always', dagRunId: 'r1' },
          query: { remoteNode: 'local' },
        },
      })
    );
  });

  it('keeps Running for a continuous step of a workflow that is not a service DAG', () => {
    h.service = false;
    h.steps = [forever];
    h.nodes = [node(NodeStatus.Running, forever)];
    render(
      <RunStatusChip name="always" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    expect(screen.getByText('Running')).toBeVisible();
    expect(screen.queryByText('Healthy')).toBeNull();
    expect(detailsCalls()).toHaveLength(0);
  });

  it('keeps Running when a step of the run failed', () => {
    h.steps = [forever];
    h.nodes = [node(NodeStatus.Running, forever), node(NodeStatus.Failed)];
    render(
      <RunStatusChip name="always" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    expect(screen.getByText('Running')).toBeVisible();
  });

  it('keeps Running for an ordinary run that is not a service', () => {
    h.service = false;
    h.nodes = [node(NodeStatus.Running)];
    render(
      <RunStatusChip name="etl" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    expect(screen.getByText('Running')).toBeVisible();
  });

  it('reads Healthy for a running run of a service with no continuous step', () => {
    h.nodes = [node(NodeStatus.Running)];
    render(
      <RunStatusChip name="etl" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    expect(screen.getByText('Healthy')).toBeVisible();
  });

  // A page of running runs must not read every run again and again: only a
  // run that can turn Healthy reads its details again, once a minute.
  it('reads the details again only for a run with a continuous step', () => {
    render(
      <RunStatusChip name="etl" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    const { refreshInterval } = h.options as {
      refreshInterval: (latest?: unknown) => number;
    };
    const details = (nodes: unknown[]) => ({ dagRunDetails: { nodes } });

    expect(refreshInterval(undefined)).toBe(0);
    expect(refreshInterval(details([node(NodeStatus.Running)]))).toBe(0);
    expect(refreshInterval(details([node(NodeStatus.Success, forever)]))).toBe(
      60_000
    );
  });

  it('reads the details only for a running run', () => {
    h.nodes = [node(NodeStatus.Running, forever)];
    render(
      <RunStatusChip name="always" dagRunId="r1" status={Status.Failed}>
        {StatusLabel.failed}
      </RunStatusChip>
    );
    expect(screen.getByText('Failed')).toBeVisible();
    expect(h.query).toHaveBeenCalledWith('/dag-runs/{name}/{dagRunId}', null);
  });

  // A page of running runs must not read the details of each run.
  it('reads no run details when the workflow is not a service', () => {
    h.service = false;
    h.steps = [forever];
    h.nodes = [node(NodeStatus.Running)];
    render(
      <>
        {['r1', 'r2', 'r3'].map((id) => (
          <RunStatusChip
            key={id}
            name="etl"
            dagRunId={id}
            status={Status.Running}
          >
            {StatusLabel.running}
          </RunStatusChip>
        ))}
      </>
    );
    expect(screen.getAllByText('Running')).toHaveLength(3);
    expect(detailsCalls()).toHaveLength(0);
  });

  it('waits for the definition before it reads any run details', () => {
    h.probe = 'loading';
    render(
      <RunStatusChip name="etl" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    expect(screen.getByText('Running')).toBeVisible();
    expect(detailsCalls()).toHaveLength(0);
  });

  // Without the definition the service fact is unknown: the status stands.
  it('keeps Running when the definition cannot be read', () => {
    h.probe = 'error';
    h.nodes = [node(NodeStatus.Running, forever)];
    render(
      <RunStatusChip name="renamed" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    expect(screen.getByText('Running')).toBeVisible();
    expect(detailsCalls()).toHaveLength(0);
  });

  it('reads no definition for a run that is not running', () => {
    render(
      <RunStatusChip name="etl" dagRunId="r1" status={Status.Success}>
        {StatusLabel.succeeded}
      </RunStatusChip>
    );
    expect(h.query.mock.calls.every(([, init]) => init === null)).toBe(true);
  });
});
