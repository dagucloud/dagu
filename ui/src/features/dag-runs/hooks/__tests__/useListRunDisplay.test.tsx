// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, RestartPolicy, Status } from '@/api/v1/schema';
import type { RunDisplayStatus } from '@/lib/runDisplayStatus';
import { useListRunDisplay } from '../useListRunDisplay';

const h = vi.hoisted(() => {
  // A Request needs an absolute URL.
  (globalThis as { getConfig?: unknown }).getConfig = () => ({
    apiURL: 'http://localhost/api/v1',
    basePath: '',
    version: 'test',
  });
  return { fetch: vi.fn() };
});

vi.mock('@/lib/requestTimeout', () => ({
  fetchWithTimeout: (input: Request) => {
    return h.fetch(input.url);
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const definitionFetches = () =>
  h.fetch.mock.calls.filter(([url]) => String(url).includes('/dags/'));

beforeEach(() => {
  vi.useFakeTimers({
    toFake: [
      'setTimeout',
      'clearTimeout',
      'requestAnimationFrame',
      'cancelAnimationFrame',
      'Date',
    ],
  });
  h.fetch.mockImplementation(
    async () =>
      new Response(JSON.stringify({ dag: { steps: [{}] } }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
  );
});

afterEach(() => {
  vi.useRealTimers();
  h.fetch.mockReset();
});

// The definition of a workflow is reused for 5 minutes and read again by the
// first row that mounts after that.
// A row of a list.
function Row() {
  useListRunDisplay('etl', 'r1', Status.Running);
  return null;
}

// The list stays mounted, as in the app: its rows mount and unmount.
function List({ rows }: { rows: number }) {
  return (
    <SWRConfig value={{ provider: () => new Map() }}>
      {Array.from({ length: rows }, (_, i) => (
        <Row key={i} />
      ))}
    </SWRConfig>
  );
}

const settle = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });

// The definition of a workflow is reused for 5 minutes and read again by the
// first row that mounts after that.
describe('useListRunDisplay definition cache', () => {
  it('reads the definition again only after the dedupe interval', async () => {
    const { rerender } = render(<List rows={3} />);
    await settle(50);
    // The three rows of one workflow share one request.
    expect(definitionFetches()).toHaveLength(1);

    rerender(<List rows={0} />);
    await settle(4 * 60_000);
    rerender(<List rows={1} />);
    await settle(50);
    expect(definitionFetches()).toHaveLength(1);

    rerender(<List rows={0} />);
    await settle(2 * 60_000);
    rerender(<List rows={1} />);
    await settle(50);
    expect(definitionFetches()).toHaveLength(2);
  });
});

// Only a service DAG (restart_policy in its definition) can read Healthy.
describe('useListRunDisplay of a continuous step', () => {
  const forever = { repeatPolicy: { repeat: 'while', interval: 10 } };

  function serve(definition: object) {
    h.fetch.mockImplementation(async (url: string) => {
      const body = String(url).includes('/dag-runs/')
        ? {
            dagRunDetails: {
              dagRunId: 'r1',
              status: Status.Running,
              nodes: [{ status: NodeStatus.Running, step: forever }],
            },
          }
        : { dag: { steps: [forever], ...definition } };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    });
  }

  async function read(): Promise<RunDisplayStatus | undefined> {
    let display: RunDisplayStatus | undefined;
    function Probe() {
      display = useListRunDisplay('etl', 'r1', Status.Running);
      return null;
    }
    render(
      <SWRConfig value={{ provider: () => new Map() }}>
        <Probe />
      </SWRConfig>
    );
    await settle(50);
    return display;
  }

  it('reads Healthy when the definition has a restart policy', async () => {
    serve({ restartPolicy: RestartPolicy.always });
    expect(await read()).toMatchObject({ healthy: true, text: 'Healthy' });
  });

  it('reads Running when the definition has no restart policy', async () => {
    serve({});
    expect(await read()).toMatchObject({ healthy: false, text: 'Running' });
    // The run is not read: it cannot turn Healthy.
    expect(
      h.fetch.mock.calls.filter(([url]) => String(url).includes('/dag-runs/'))
    ).toHaveLength(0);
  });
});
