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

const serviceListFetches = () =>
  h.fetch.mock.calls.filter(([url]) => String(url).includes('/service-dags'));

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
      new Response(JSON.stringify({ dags: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
  );
});

afterEach(() => {
  vi.useRealTimers();
  h.fetch.mockReset();
});

// A row of a list.
function Row({ name }: { name: string }) {
  useListRunDisplay(name, 'r1', Status.Running);
  return null;
}

// The list stays mounted, as in the app: its rows mount and unmount.
function List({ rows }: { rows: number }) {
  return (
    <SWRConfig value={{ provider: () => new Map() }}>
      {Array.from({ length: rows }, (_, i) => (
        <Row key={i} name={`wf-${i}`} />
      ))}
    </SWRConfig>
  );
}

const settle = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });

// The list of service DAGs is reused for 5 minutes and read again by the
// first row that mounts after that.
describe('useListRunDisplay service list cache', () => {
  it('reads the service list again only after the dedupe interval', async () => {
    const { rerender } = render(<List rows={3} />);
    await settle(50);
    // The rows of three workflows share one request.
    expect(serviceListFetches()).toHaveLength(1);

    rerender(<List rows={0} />);
    await settle(4 * 60_000);
    rerender(<List rows={1} />);
    await settle(50);
    expect(serviceListFetches()).toHaveLength(1);

    rerender(<List rows={0} />);
    await settle(2 * 60_000);
    rerender(<List rows={1} />);
    await settle(50);
    expect(serviceListFetches()).toHaveLength(2);
  });
});

// Only a service DAG (restart_policy) can read Healthy.
describe('useListRunDisplay of a running run', () => {
  const forever = { repeatPolicy: { repeat: 'while', interval: 10 } };

  function serve(
    services: object[],
    nodes: () => object[] = () => [
      { status: NodeStatus.Running, step: forever },
    ]
  ) {
    h.fetch.mockImplementation(async (url: string) => {
      const body = String(url).includes('/dag-runs/')
        ? {
            dagRunDetails: {
              dagRunId: 'r1',
              status: Status.Running,
              nodes: nodes(),
            },
          }
        : { dags: services };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    });
  }

  // Mounts a row and returns its latest display status.
  async function mount(): Promise<() => RunDisplayStatus | undefined> {
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
    return () => display;
  }

  const read = async () => (await mount())();

  it('reads Healthy when the service list names the workflow', async () => {
    serve([{ name: 'etl', restartPolicy: RestartPolicy.always }]);
    expect(await read()).toMatchObject({ healthy: true, text: 'Healthy' });
  });

  it('reads Running when the service list does not name the workflow', async () => {
    serve([{ name: 'other', restartPolicy: RestartPolicy.always }]);
    expect(await read()).toMatchObject({ healthy: false, text: 'Running' });
    // The run is not read: it cannot turn Healthy.
    expect(
      h.fetch.mock.calls.filter(([url]) => String(url).includes('/dag-runs/'))
    ).toHaveLength(0);
  });

  // A step that fails leaves the run Running in the list: only a new read of
  // the details shows the failure, also when the run has no continuous step.
  it('turns Running when a later read shows a failed step', async () => {
    const work = { status: NodeStatus.Running, step: {} };
    let nodes: object[] = [work];
    serve([{ name: 'etl', restartPolicy: RestartPolicy.always }], () => nodes);
    const display = await mount();
    expect(display()).toMatchObject({ healthy: true, text: 'Healthy' });

    nodes = [work, { status: NodeStatus.Failed, step: {} }];
    await settle(60_000);
    await settle(50);
    expect(display()).toMatchObject({ healthy: false, text: 'Running' });
  });
});
