// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status, StatusLabel } from '@/api/v1/schema';
import { RunStatusChip } from '../RunStatusChip';

const h = vi.hoisted(() => ({
  nodes: [] as unknown[],
  // True when the service list names every workflow of the tests.
  service: true,
  probe: 'data' as 'data' | 'loading' | 'error',
  query: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    h.query(path, init);
    if (init === null) return { data: undefined };
    if (path === '/service-dags') {
      if (h.probe === 'loading') return { data: undefined };
      if (h.probe === 'error')
        return { data: undefined, error: new Error('x') };
      return {
        data: {
          dags: h.service ? [{ name: 'always' }, { name: 'etl' }] : [],
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
  h.service = true;
  h.probe = 'data';
  h.query.mockClear();
});

// The summary of a run in a list has no steps: a running run reads its details.
describe('RunStatusChip', () => {
  it('reads Healthy for a running run of a service DAG with a continuous step', () => {
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

  it('waits for the service list before it reads any run details', () => {
    h.probe = 'loading';
    render(
      <RunStatusChip name="etl" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    expect(screen.getByText('Running')).toBeVisible();
    expect(detailsCalls()).toHaveLength(0);
  });

  // Without the service list the service fact is unknown: the status stands.
  it('keeps Running when the service list cannot be read', () => {
    h.probe = 'error';
    h.nodes = [node(NodeStatus.Running, forever)];
    render(
      <RunStatusChip name="always" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    expect(screen.getByText('Running')).toBeVisible();
    expect(detailsCalls()).toHaveLength(0);
  });

  it('reads nothing for a run that is not running', () => {
    render(
      <RunStatusChip name="etl" dagRunId="r1" status={Status.Success}>
        {StatusLabel.succeeded}
      </RunStatusChip>
    );
    expect(h.query.mock.calls.every(([, init]) => init === null)).toBe(true);
  });
});
