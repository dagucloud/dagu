// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status, StatusLabel } from '@/api/v1/schema';
import { RunStatusChip } from '../RunStatusChip';

const h = vi.hoisted(() => ({
  nodes: [] as unknown[],
  query: vi.fn(),
  options: undefined as unknown,
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown, options: unknown) => {
    h.query(path, init);
    h.options = options;
    if (init === null) return { data: undefined };
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
  h.query.mockClear();
});

// The summary of a run in a list has no steps: a running run reads its details.
describe('RunStatusChip', () => {
  it('reads Healthy for a running run with a continuous step', () => {
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

  it('keeps Running when a step of the run failed', () => {
    h.nodes = [node(NodeStatus.Running, forever), node(NodeStatus.Failed)];
    render(
      <RunStatusChip name="always" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    expect(screen.getByText('Running')).toBeVisible();
  });

  it('keeps Running for an ordinary run', () => {
    h.nodes = [node(NodeStatus.Running)];
    render(
      <RunStatusChip name="etl" dagRunId="r1" status={Status.Running}>
        {StatusLabel.running}
      </RunStatusChip>
    );
    expect(screen.getByText('Running')).toBeVisible();
  });

  // A page of running runs must not read every run again and again: only a
  // run that can turn Healthy reads its details again.
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
    expect(
      refreshInterval(details([node(NodeStatus.Success, forever)]))
    ).toBeGreaterThan(0);
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
});
