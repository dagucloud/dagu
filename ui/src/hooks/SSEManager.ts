// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { getAuthHeaders } from '@/lib/authHeaders';
import {
  getAuthToken,
  handleAuthResponse,
  isBuiltinAuthMode,
} from '@/lib/authSession';

const MAX_RETRY_DELAY_MS = 16000;
const CONNECT_TIMEOUT_MS = 15000;
const MUTATION_DEBOUNCE_MS = 200;
const DRAINING_GRACE_PERIOD_MS = 2000;
const FALLBACK_AFTER_RETRIES = 5;

export interface SSEConnectionState {
  isConnected: boolean;
  isConnecting: boolean;
  shouldUseFallback: boolean;
  error: Error | null;
}

export interface SubscriberCallbacks {
  onData: (data: unknown) => void;
  onStateChange: (state: SSEConnectionState) => void;
}

interface TopicSubscription {
  subscribers: Map<string, SubscriberCallbacks>;
  lastPayload: unknown | null;
}

interface ManagedConnection {
  key: string;
  apiURL: string;
  remoteNode: string;
  eventSource: EventSource | null;
  sessionId: string | null;
  lastEventId: string;
  retryCount: number;
  retryTimeout: ReturnType<typeof setTimeout> | null;
  connectTimeout: ReturnType<typeof setTimeout> | null;
  drainTimeout: ReturnType<typeof setTimeout> | null;
  mutationTimeout: ReturnType<typeof setTimeout> | null;
  mutationInFlight: boolean;
  topics: Map<string, TopicSubscription>;
  serverTopics: Set<string>;
  pendingAdd: Set<string>;
  pendingRemove: Set<string>;
  state: SSEConnectionState;
}

interface ControlEvent {
  sessionID: string;
  subscribed?: string[];
  errors?: Array<{ topic: string; code: string; message: string }>;
}

interface TopicMutationResponse {
  subscribed?: string[];
  errors?: Array<{ topic: string; code: string; message: string }>;
}

let subscriberIdCounter = 0;

function buildConnectionKey(apiURL: string, remoteNode: string): string {
  return `${apiURL}|${remoteNode}`;
}

function buildTopic(topicType: string, identifier: string = ''): string {
  return `${topicType}:${identifier}`;
}

function canonicalizeQuery(params: URLSearchParams): string {
  const entries = Array.from(params.entries())
    .filter(([key]) => key !== 'token' && key !== 'remoteNode')
    .sort(([keyA, valueA], [keyB, valueB]) => {
      if (keyA === keyB) {
        return valueA.localeCompare(valueB);
      }
      return keyA.localeCompare(keyB);
    });

  const next = new URLSearchParams();
  for (const [key, value] of entries) {
    next.append(key, value);
  }
  return next.toString();
}

function decodePathSegments(path: string): string[] {
  return path
    .split('/')
    .filter(Boolean)
    .map((segment) => decodeURIComponent(segment));
}

export function endpointToTopic(endpoint: string): string {
  const url = new URL(endpoint, window.location.origin);
  const query = canonicalizeQuery(url.searchParams);
  const segments = decodePathSegments(url.pathname);

  if (segments[0] !== 'events') {
    throw new Error(`Unsupported SSE endpoint: ${endpoint}`);
  }

  if (segments.length === 2 && segments[1] === 'dags') {
    return buildTopic('dagslist', query);
  }
  if (segments.length === 3 && segments[1] === 'dags') {
    return buildTopic('dag', segments[2]);
  }
  if (
    segments.length === 4 &&
    segments[1] === 'dags' &&
    segments[3] === 'dag-runs'
  ) {
    return buildTopic('daghistory', segments[2]);
  }
  if (segments.length === 2 && segments[1] === 'dag-runs') {
    return buildTopic('dagruns', query);
  }
  if (segments.length === 4 && segments[1] === 'dag-runs') {
    return buildTopic('dagrun', `${segments[2]}/${segments[3]}`);
  }
  if (
    segments.length === 6 &&
    segments[1] === 'dag-runs' &&
    segments[4] === 'sub-dag-runs'
  ) {
    return buildTopic(
      'subdagrun',
      `${segments[2]}/${segments[3]}/${segments[5]}`
    );
  }
  if (
    segments.length === 5 &&
    segments[1] === 'dag-runs' &&
    segments[4] === 'logs'
  ) {
    const identifier = `${segments[2]}/${segments[3]}`;
    return buildTopic(
      'dagrunlogs',
      query ? `${identifier}?${query}` : identifier
    );
  }
  if (
    segments.length === 7 &&
    segments[1] === 'dag-runs' &&
    segments[4] === 'logs' &&
    segments[5] === 'steps'
  ) {
    return buildTopic(
      'steplog',
      `${segments[2]}/${segments[3]}/${segments[6]}`
    );
  }
  if (segments.length === 2 && segments[1] === 'queues') {
    return buildTopic('queues', query);
  }
  if (
    segments.length === 4 &&
    segments[1] === 'queues' &&
    segments[3] === 'items'
  ) {
    return buildTopic('queueitems', segments[2]);
  }
  if (segments.length === 2 && segments[1] === 'docs-tree') {
    return buildTopic('doctree', query);
  }
  if (segments.length >= 3 && segments[1] === 'docs') {
    const identifier = segments.slice(2).join('/');
    return buildTopic('doc', query ? `${identifier}?${query}` : identifier);
  }

  throw new Error(`Unsupported SSE endpoint: ${endpoint}`);
}

function buildStreamUrl(
  apiURL: string,
  remoteNode: string,
  topics: string[],
  lastEventId: string
): string {
  const url = new URL(`${apiURL}/events/stream`, window.location.origin);
  for (const topic of [...topics].sort()) {
    url.searchParams.append('topic', topic);
  }
  url.searchParams.set('remoteNode', remoteNode);

  const token = getAuthToken();
  if (token) {
    url.searchParams.set('token', token);
  }
  if (lastEventId) {
    url.searchParams.set('lastEventId', lastEventId);
  }

  return url.toString();
}

function buildMutationUrl(apiURL: string, remoteNode: string): string {
  const url = new URL(`${apiURL}/events/stream/topics`, window.location.origin);
  url.searchParams.set('remoteNode', remoteNode);
  return url.toString();
}

function buildTopicState(
  conn: ManagedConnection,
  topic: string
): SSEConnectionState {
  const isSubscribed = conn.serverTopics.has(topic);
  const isPendingAdd = conn.pendingAdd.has(topic);

  return {
    isConnected: conn.state.isConnected && isSubscribed,
    isConnecting: conn.state.isConnecting || isPendingAdd,
    shouldUseFallback: conn.state.shouldUseFallback,
    error: conn.state.error,
  };
}

function calculateRetryDelay(retryCount: number): number {
  return Math.min(1000 * 2 ** retryCount, MAX_RETRY_DELAY_MS);
}

export class SSEManager {
  private connections = new Map<string, ManagedConnection>();

  subscribe(
    endpoint: string,
    remoteNode: string,
    apiURL: string,
    callbacks: SubscriberCallbacks
  ): () => void {
    return this.subscribeTopic(
      endpointToTopic(endpoint),
      remoteNode,
      apiURL,
      callbacks
    );
  }

  subscribeTopic(
    topic: string,
    remoteNode: string,
    apiURL: string,
    callbacks: SubscriberCallbacks
  ): () => void {
    const key = buildConnectionKey(apiURL, remoteNode);
    const subscriberId = String(++subscriberIdCounter);
    const conn = this.getOrCreateConnection(key, apiURL, remoteNode);

    if (conn.drainTimeout) {
      clearTimeout(conn.drainTimeout);
      conn.drainTimeout = null;
    }

    let topicEntry = conn.topics.get(topic);
    const isNewTopic = !topicEntry;
    if (!topicEntry) {
      topicEntry = {
        subscribers: new Map(),
        lastPayload: null,
      };
      conn.topics.set(topic, topicEntry);
    }

    topicEntry.subscribers.set(subscriberId, callbacks);

    if (isNewTopic) {
      this.handleTopicAdded(conn, topic);
    } else {
      this.ensureConnected(conn);
    }

    callbacks.onStateChange(buildTopicState(conn, topic));
    if (topicEntry.lastPayload !== null) {
      callbacks.onData(topicEntry.lastPayload);
    }

    return () => this.unsubscribe(key, topic, subscriberId);
  }

  private getOrCreateConnection(
    key: string,
    apiURL: string,
    remoteNode: string
  ): ManagedConnection {
    const existing = this.connections.get(key);
    if (existing) {
      return existing;
    }

    const conn: ManagedConnection = {
      key,
      apiURL,
      remoteNode,
      eventSource: null,
      sessionId: null,
      lastEventId: '',
      retryCount: 0,
      retryTimeout: null,
      connectTimeout: null,
      drainTimeout: null,
      mutationTimeout: null,
      mutationInFlight: false,
      topics: new Map(),
      serverTopics: new Set(),
      pendingAdd: new Set(),
      pendingRemove: new Set(),
      state: {
        isConnected: false,
        isConnecting: false,
        shouldUseFallback: false,
        error: null,
      },
    };

    this.connections.set(key, conn);
    return conn;
  }

  private handleTopicAdded(conn: ManagedConnection, topic: string): void {
    if (conn.sessionId && conn.state.isConnected) {
      if (!conn.serverTopics.has(topic)) {
        conn.pendingRemove.delete(topic);
        conn.pendingAdd.add(topic);
        this.notifyTopicState(conn, topic);
        this.scheduleMutation(conn);
      }
      return;
    }

    if (conn.eventSource && conn.state.isConnecting) {
      conn.pendingRemove.delete(topic);
      conn.pendingAdd.add(topic);
      this.notifyTopicState(conn, topic);
      return;
    }

    this.ensureConnected(conn);
  }

  private unsubscribe(key: string, topic: string, subscriberId: string): void {
    const conn = this.connections.get(key);
    if (!conn) {
      return;
    }

    const topicEntry = conn.topics.get(topic);
    if (!topicEntry) {
      return;
    }

    topicEntry.subscribers.delete(subscriberId);
    if (topicEntry.subscribers.size > 0) {
      return;
    }

    conn.topics.delete(topic);
    conn.pendingAdd.delete(topic);

    if (
      conn.sessionId &&
      (conn.state.isConnected ||
        conn.state.isConnecting ||
        conn.serverTopics.has(topic))
    ) {
      conn.pendingRemove.add(topic);
      this.scheduleMutation(conn);
    }

    if (conn.topics.size === 0) {
      this.startDraining(conn);
    }
  }

  private startDraining(conn: ManagedConnection): void {
    if (conn.drainTimeout) {
      return;
    }

    conn.drainTimeout = setTimeout(() => {
      conn.drainTimeout = null;
      if (conn.topics.size > 0) {
        return;
      }
      this.disposeConnection(conn);
    }, DRAINING_GRACE_PERIOD_MS);
  }

  private ensureConnected(conn: ManagedConnection): void {
    if (conn.topics.size === 0) {
      return;
    }
    if (conn.eventSource || conn.retryTimeout) {
      return;
    }
    this.connect(conn);
  }

  private connect(conn: ManagedConnection): void {
    if (conn.topics.size === 0) {
      return;
    }

    if (isBuiltinAuthMode() && !getAuthToken()) {
      this.updateState(conn, {
        isConnected: false,
        isConnecting: false,
        shouldUseFallback: false,
        error: new Error('Authentication required'),
      });
      return;
    }

    if (conn.eventSource) {
      conn.eventSource.close();
      conn.eventSource = null;
    }
    if (conn.connectTimeout) {
      clearTimeout(conn.connectTimeout);
      conn.connectTimeout = null;
    }

    const url = buildStreamUrl(
      conn.apiURL,
      conn.remoteNode,
      Array.from(conn.topics.keys()),
      conn.lastEventId
    );
    const eventSource = new EventSource(url);
    conn.eventSource = eventSource;
    conn.sessionId = null;
    conn.serverTopics.clear();

    this.updateState(conn, {
      isConnected: false,
      isConnecting: true,
      shouldUseFallback: conn.retryCount >= FALLBACK_AFTER_RETRIES,
      error: null,
    });

    conn.connectTimeout = setTimeout(() => {
      if (conn.eventSource !== eventSource || conn.sessionId) {
        return;
      }
      eventSource.close();
      conn.eventSource = null;
      this.handleDisconnect(conn, new Error('SSE connect timeout'));
    }, CONNECT_TIMEOUT_MS);

    eventSource.addEventListener('control', (event) => {
      if (conn.eventSource !== eventSource) {
        return;
      }

      try {
        const control = JSON.parse(
          (event as MessageEvent).data
        ) as ControlEvent;
        conn.sessionId = control.sessionID;
        conn.serverTopics = new Set(control.subscribed ?? []);
        conn.retryCount = 0;

        if (conn.connectTimeout) {
          clearTimeout(conn.connectTimeout);
          conn.connectTimeout = null;
        }

        // Clear pendingAdd for topics the server already subscribed via the
        // initial URL params. Without this, scheduleMutation would fire a
        // redundant POST that wastes an HTTP connection slot.
        for (const topic of conn.serverTopics) {
          conn.pendingAdd.delete(topic);
        }

        this.updateState(conn, {
          isConnected: true,
          isConnecting: false,
          shouldUseFallback: false,
          error: null,
        });

        if (control.errors && control.errors.length > 0) {
          console.warn('SSE control errors:', control.errors);
        }

        this.scheduleMutation(conn, 0);
      } catch (error) {
        this.handleDisconnect(
          conn,
          error instanceof Error ? error : new Error('Invalid control event')
        );
      }
    });

    eventSource.addEventListener('message', (event) => {
      if (conn.eventSource !== eventSource) {
        return;
      }

      const messageEvent = event as MessageEvent;
      if (messageEvent.lastEventId) {
        conn.lastEventId = messageEvent.lastEventId;
      }

      try {
        const parsed = JSON.parse(messageEvent.data) as {
          topic?: string;
          payload?: unknown;
        };
        if (!parsed.topic) {
          return;
        }
        const topicEntry = conn.topics.get(parsed.topic);
        if (!topicEntry) {
          return;
        }
        topicEntry.lastPayload = parsed.payload ?? null;
        for (const subscriber of topicEntry.subscribers.values()) {
          subscriber.onData(parsed.payload ?? null);
        }
      } catch (error) {
        this.updateState(conn, {
          error:
            error instanceof Error
              ? error
              : new Error('Invalid JSON response from SSE'),
        });
      }
    });

    eventSource.onerror = () => {
      if (conn.eventSource !== eventSource) {
        return;
      }

      eventSource.close();
      conn.eventSource = null;
      this.handleDisconnect(conn, new Error('SSE connection lost'));
    };
  }

  private handleDisconnect(conn: ManagedConnection, error: Error): void {
    if (conn.connectTimeout) {
      clearTimeout(conn.connectTimeout);
      conn.connectTimeout = null;
    }

    conn.sessionId = null;
    conn.serverTopics.clear();
    conn.pendingAdd.clear();
    conn.pendingRemove.clear();

    this.updateState(conn, {
      isConnected: false,
      isConnecting: false,
      shouldUseFallback: conn.retryCount >= FALLBACK_AFTER_RETRIES,
      error,
    });

    if (conn.topics.size === 0) {
      return;
    }

    const delay = calculateRetryDelay(conn.retryCount);
    conn.retryCount += 1;

    conn.retryTimeout = setTimeout(() => {
      conn.retryTimeout = null;
      this.connect(conn);
    }, delay);
  }

  private scheduleMutation(
    conn: ManagedConnection,
    delay: number = MUTATION_DEBOUNCE_MS
  ): void {
    if (!conn.sessionId || !conn.state.isConnected) {
      return;
    }
    if (conn.mutationTimeout) {
      clearTimeout(conn.mutationTimeout);
    }

    conn.mutationTimeout = setTimeout(() => {
      conn.mutationTimeout = null;
      void this.flushMutation(conn);
    }, delay);
  }

  private async flushMutation(conn: ManagedConnection): Promise<void> {
    if (conn.mutationInFlight || !conn.sessionId || !conn.state.isConnected) {
      return;
    }

    const mutationSessionId = conn.sessionId;
    const mutationEventSource = conn.eventSource;
    const add = Array.from(conn.pendingAdd).filter((topic) =>
      conn.topics.has(topic)
    );
    const remove = Array.from(conn.pendingRemove);
    if (add.length === 0 && remove.length === 0) {
      return;
    }

    conn.mutationInFlight = true;
    try {
      const response = await fetch(
        buildMutationUrl(conn.apiURL, conn.remoteNode),
        {
          method: 'POST',
          headers: getAuthHeaders(),
          body: JSON.stringify({
            sessionID: mutationSessionId,
            add,
            remove,
          }),
        }
      );
      handleAuthResponse(response);

      const isStaleMutation = () =>
        conn.sessionId !== mutationSessionId ||
        conn.eventSource !== mutationEventSource;

      if (isStaleMutation()) {
        return;
      }

      if (response.status === 404) {
        conn.pendingAdd.clear();
        conn.pendingRemove.clear();
        if (conn.eventSource) {
          conn.eventSource.close();
          conn.eventSource = null;
        }
        this.handleDisconnect(conn, new Error('SSE session expired'));
        return;
      }

      const body = (await response.json()) as
        | TopicMutationResponse
        | { error?: string; message?: string };

      if (isStaleMutation()) {
        return;
      }

      if (!response.ok && response.status !== 403) {
        throw new Error(
          'message' in body && typeof body.message === 'string'
            ? body.message
            : 'Failed to update SSE topics'
        );
      }

      if ('subscribed' in body) {
        conn.serverTopics = new Set(body.subscribed ?? []);
      }
      for (const topic of add) {
        conn.pendingAdd.delete(topic);
      }
      for (const topic of remove) {
        conn.pendingRemove.delete(topic);
      }

      if ('errors' in body && body.errors && body.errors.length > 0) {
        console.warn('SSE topic mutation errors:', body.errors);
      }
      this.notifyAllTopicStates(conn);
    } catch (error) {
      this.updateState(conn, {
        error:
          error instanceof Error
            ? error
            : new Error('Failed to update SSE topics'),
      });
    } finally {
      conn.mutationInFlight = false;
      if (
        (conn.pendingAdd.size > 0 || conn.pendingRemove.size > 0) &&
        conn.sessionId &&
        conn.state.isConnected
      ) {
        this.scheduleMutation(conn, 0);
      }
    }
  }

  private updateState(
    conn: ManagedConnection,
    partial: Partial<SSEConnectionState>
  ): void {
    conn.state = {
      ...conn.state,
      ...partial,
    };

    this.notifyAllTopicStates(conn);
  }

  private notifyAllTopicStates(conn: ManagedConnection): void {
    for (const [topic, topicEntry] of conn.topics.entries()) {
      this.notifyTopicEntryState(conn, topic, topicEntry);
    }
  }

  private notifyTopicState(conn: ManagedConnection, topic: string): void {
    const topicEntry = conn.topics.get(topic);
    if (!topicEntry) {
      return;
    }
    this.notifyTopicEntryState(conn, topic, topicEntry);
  }

  private notifyTopicEntryState(
    conn: ManagedConnection,
    topic: string,
    topicEntry: TopicSubscription
  ): void {
    const topicState = buildTopicState(conn, topic);
    for (const subscriber of topicEntry.subscribers.values()) {
      subscriber.onStateChange(topicState);
    }
  }

  private disposeConnection(conn: ManagedConnection): void {
    if (conn.eventSource) {
      conn.eventSource.close();
      conn.eventSource = null;
    }
    if (conn.retryTimeout) {
      clearTimeout(conn.retryTimeout);
      conn.retryTimeout = null;
    }
    if (conn.connectTimeout) {
      clearTimeout(conn.connectTimeout);
      conn.connectTimeout = null;
    }
    if (conn.drainTimeout) {
      clearTimeout(conn.drainTimeout);
      conn.drainTimeout = null;
    }
    if (conn.mutationTimeout) {
      clearTimeout(conn.mutationTimeout);
      conn.mutationTimeout = null;
    }

    this.connections.delete(conn.key);
  }

  disposeAll(): void {
    for (const conn of Array.from(this.connections.values())) {
      this.disposeConnection(conn);
    }
  }
}

export const sseManager = new SSEManager();
