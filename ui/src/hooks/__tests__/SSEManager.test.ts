import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { SSEConnectionState } from '../SSEManager';
import { endpointToTopic, SSEManager } from '../SSEManager';
import { setAuthSession } from '../../lib/authSession';

class MockEventSource {
  static instances: MockEventSource[] = [];

  readonly url: string;
  readonly close = vi.fn();
  onerror: (() => void) | null = null;
  private listeners = new Map<
    string,
    Set<(event: MessageEvent<string>) => void>
  >();

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  addEventListener(
    type: string,
    listener: (event: MessageEvent<string>) => void
  ) {
    const listeners = this.listeners.get(type) ?? new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  emit(type: string, data: unknown, lastEventId = '') {
    const listeners = this.listeners.get(type);
    if (!listeners) {
      return;
    }

    const event = {
      data: JSON.stringify(data),
      lastEventId,
    } as MessageEvent<string>;
    for (const listener of listeners) {
      listener(event);
    }
  }
}

function snapshotState(state: SSEConnectionState): SSEConnectionState {
  return {
    isConnected: state.isConnected,
    isConnecting: state.isConnecting,
    shouldUseFallback: state.shouldUseFallback,
    error: state.error,
  };
}

function lastState(
  states: SSEConnectionState[]
): SSEConnectionState | undefined {
  return states[states.length - 1];
}

describe('endpointToTopic', () => {
  it('maps every supported legacy SSE endpoint to a canonical topic', () => {
    const cases: Array<[string, string]> = [
      ['/events/dags?perPage=100&page=1', 'dagslist:page=1&perPage=100'],
      ['/events/dags/example.yaml', 'dag:example.yaml'],
      ['/events/dags/example.yaml/dag-runs', 'daghistory:example.yaml'],
      [
        '/events/dag-runs?page=2&status=running',
        'dagruns:page=2&status=running',
      ],
      ['/events/dag-runs/mydag/run-1', 'dagrun:mydag/run-1'],
      [
        '/events/dag-runs/mydag/run-1/sub-dag-runs/sub-1',
        'subdagrun:mydag/run-1/sub-1',
      ],
      [
        '/events/dag-runs/mydag/run-1/logs?tail=500',
        'dagrunlogs:mydag/run-1?tail=500',
      ],
      [
        '/events/dag-runs/mydag/run-1/logs/steps/build',
        'steplog:mydag/run-1/build',
      ],
      ['/events/queues?status=active&page=3', 'queues:page=3&status=active'],
      ['/events/queues/default/items', 'queueitems:default'],
      ['/events/docs-tree?prefix=guides', 'doctree:prefix=guides'],
      ['/events/docs/runbooks/deploy%20guide', 'doc:runbooks/deploy guide'],
    ];

    for (const [endpoint, topic] of cases) {
      expect(endpointToTopic(endpoint)).toBe(topic);
    }
  });
});

describe('SSEManager', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    MockEventSource.instances = [];
    vi.stubGlobal('EventSource', MockEventSource);
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it('keeps a fresh topic pending until the server acknowledges the subscription', async () => {
    const manager = new SSEManager();
    const primaryStates: SSEConnectionState[] = [];
    const secondaryStates: SSEConnectionState[] = [];

    const unsubscribePrimary = manager.subscribeTopic(
      'dag:test.yaml',
      'local',
      '/api/v1',
      {
        onData: () => undefined,
        onStateChange: (state) => primaryStates.push(snapshotState(state)),
      }
    );

    expect(MockEventSource.instances).toHaveLength(1);
    const eventSource = MockEventSource.instances[0];
    expect(eventSource).toBeDefined();
    if (!eventSource) {
      throw new Error('expected EventSource instance');
    }
    eventSource.emit('control', {
      sessionID: 'session-1',
      subscribed: ['dag:test.yaml'],
    });

    expect(lastState(primaryStates)).toMatchObject({
      isConnected: true,
      isConnecting: false,
    });

    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        subscribed: ['dag:test.yaml', 'queueitems:default'],
        errors: [],
      }),
    } as Response);

    const unsubscribeSecondary = manager.subscribeTopic(
      'queueitems:default',
      'local',
      '/api/v1',
      {
        onData: () => undefined,
        onStateChange: (state) => secondaryStates.push(snapshotState(state)),
      }
    );

    expect(lastState(secondaryStates)).toMatchObject({
      isConnected: false,
      isConnecting: true,
      shouldUseFallback: false,
    });

    await vi.advanceTimersByTimeAsync(200);

    expect(lastState(secondaryStates)).toMatchObject({
      isConnected: true,
      isConnecting: false,
      shouldUseFallback: false,
    });

    unsubscribeSecondary();
    unsubscribePrimary();
  });

  it('leaves rejected topics offline after the mutation response arrives', async () => {
    const manager = new SSEManager();
    const secondaryStates: SSEConnectionState[] = [];

    const unsubscribePrimary = manager.subscribeTopic(
      'dag:test.yaml',
      'local',
      '/api/v1',
      {
        onData: () => undefined,
        onStateChange: () => undefined,
      }
    );

    const eventSource = MockEventSource.instances[0];
    expect(eventSource).toBeDefined();
    if (!eventSource) {
      throw new Error('expected EventSource instance');
    }
    eventSource.emit('control', {
      sessionID: 'session-1',
      subscribed: ['dag:test.yaml'],
    });

    vi.mocked(fetch).mockResolvedValue({
      ok: false,
      status: 403,
      json: async () => ({
        subscribed: ['dag:test.yaml'],
        errors: [
          {
            topic: 'queueitems:default',
            code: 'unauthorized',
            message: 'forbidden',
          },
        ],
      }),
    } as Response);

    const unsubscribeSecondary = manager.subscribeTopic(
      'queueitems:default',
      'local',
      '/api/v1',
      {
        onData: () => undefined,
        onStateChange: (state) => secondaryStates.push(snapshotState(state)),
      }
    );

    await vi.advanceTimersByTimeAsync(200);

    expect(lastState(secondaryStates)).toMatchObject({
      isConnected: false,
      isConnecting: false,
      shouldUseFallback: false,
    });

    unsubscribeSecondary();
    unsubscribePrimary();
  });

  it('ignores stale 404 mutation responses after a reconnect', async () => {
    const manager = new SSEManager();
    const secondaryStates: SSEConnectionState[] = [];
    let resolveFirstMutation:
      | ((value: Response | PromiseLike<Response>) => void)
      | undefined;

    vi.mocked(fetch).mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          resolveFirstMutation = resolve;
        })
    );

    const unsubscribePrimary = manager.subscribeTopic(
      'dag:test.yaml',
      'local',
      '/api/v1',
      {
        onData: () => undefined,
        onStateChange: () => undefined,
      }
    );

    const firstEventSource = MockEventSource.instances[0];
    if (!firstEventSource) {
      throw new Error('expected EventSource instance');
    }
    firstEventSource.emit('control', {
      sessionID: 'session-1',
      subscribed: ['dag:test.yaml'],
    });

    const unsubscribeSecondary = manager.subscribeTopic(
      'queueitems:default',
      'local',
      '/api/v1',
      {
        onData: () => undefined,
        onStateChange: (state) => secondaryStates.push(snapshotState(state)),
      }
    );

    await vi.advanceTimersByTimeAsync(200);
    firstEventSource.onerror?.();
    await vi.advanceTimersByTimeAsync(1000);

    const secondEventSource = MockEventSource.instances[1];
    if (!secondEventSource) {
      throw new Error('expected replacement EventSource instance');
    }
    secondEventSource.emit('control', {
      sessionID: 'session-2',
      subscribed: ['dag:test.yaml', 'queueitems:default'],
    });

    resolveFirstMutation?.({
      ok: false,
      status: 404,
      json: async () => ({
        message: 'unknown_session',
      }),
    } as Response);
    await Promise.resolve();
    await Promise.resolve();

    expect(secondEventSource.close).not.toHaveBeenCalled();
    expect(lastState(secondaryStates)).toMatchObject({
      isConnected: true,
      isConnecting: false,
    });

    unsubscribeSecondary();
    unsubscribePrimary();
  });

  it('ignores stale mutation payloads that refer to an older session', async () => {
    const manager = new SSEManager();
    const secondaryStates: SSEConnectionState[] = [];
    let resolveFirstMutation:
      | ((value: Response | PromiseLike<Response>) => void)
      | undefined;

    vi.mocked(fetch).mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          resolveFirstMutation = resolve;
        })
    );

    const unsubscribePrimary = manager.subscribeTopic(
      'dag:test.yaml',
      'local',
      '/api/v1',
      {
        onData: () => undefined,
        onStateChange: () => undefined,
      }
    );

    const firstEventSource = MockEventSource.instances[0];
    if (!firstEventSource) {
      throw new Error('expected EventSource instance');
    }
    firstEventSource.emit('control', {
      sessionID: 'session-1',
      subscribed: ['dag:test.yaml'],
    });

    const unsubscribeSecondary = manager.subscribeTopic(
      'queueitems:default',
      'local',
      '/api/v1',
      {
        onData: () => undefined,
        onStateChange: (state) => secondaryStates.push(snapshotState(state)),
      }
    );

    await vi.advanceTimersByTimeAsync(200);
    firstEventSource.onerror?.();
    await vi.advanceTimersByTimeAsync(1000);

    const secondEventSource = MockEventSource.instances[1];
    if (!secondEventSource) {
      throw new Error('expected replacement EventSource instance');
    }
    secondEventSource.emit('control', {
      sessionID: 'session-2',
      subscribed: ['dag:test.yaml', 'queueitems:default'],
    });

    resolveFirstMutation?.({
      ok: true,
      status: 200,
      json: async () => ({
        subscribed: ['dag:test.yaml'],
        errors: [],
      }),
    } as Response);
    await Promise.resolve();
    await Promise.resolve();

    expect(lastState(secondaryStates)).toMatchObject({
      isConnected: true,
      isConnecting: false,
    });

    unsubscribeSecondary();
    unsubscribePrimary();
  });

  it('does not open a builtin-auth stream when the local token is expired', () => {
    vi.stubGlobal('getConfig', () => ({ authMode: 'builtin' }));
    localStorage.setItem('dagu_auth_token', 'expired-token');
    localStorage.setItem(
      'dagu_auth_token_expires_at',
      new Date(Date.now() - 1000).toISOString()
    );

    const manager = new SSEManager();
    const states: SSEConnectionState[] = [];

    const unsubscribe = manager.subscribeTopic(
      'dag:test.yaml',
      'local',
      '/api/v1',
      {
        onData: () => undefined,
        onStateChange: (state) => states.push(snapshotState(state)),
      }
    );

    expect(MockEventSource.instances).toHaveLength(0);
    expect(localStorage.getItem('dagu_auth_token')).toBeNull();
    expect(lastState(states)).toMatchObject({
      isConnected: false,
      isConnecting: false,
      shouldUseFallback: false,
    });

    unsubscribe();
  });

  it('retries builtin-auth streams when a token becomes available', () => {
    vi.stubGlobal('getConfig', () => ({ authMode: 'builtin' }));

    const manager = new SSEManager();
    const states: SSEConnectionState[] = [];

    const unsubscribe = manager.subscribeTopic(
      'dag:test.yaml',
      'local',
      '/api/v1',
      {
        onData: () => undefined,
        onStateChange: (state) => states.push(snapshotState(state)),
      }
    );

    expect(MockEventSource.instances).toHaveLength(0);
    expect(lastState(states)?.error?.message).toBe('Authentication required');

    setAuthSession('fresh-token', new Date(Date.now() + 60_000).toISOString());

    expect(MockEventSource.instances).toHaveLength(1);
    expect(MockEventSource.instances[0]?.url).toContain('token=fresh-token');

    unsubscribe();
  });
});
