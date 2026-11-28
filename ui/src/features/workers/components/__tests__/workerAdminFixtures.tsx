// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import createClient from 'openapi-fetch';
import React from 'react';
import { SWRConfig } from 'swr';
import { createQueryHook } from 'swr-openapi';
import { vi } from 'vitest';
import {
  WorkerConnection,
  WorkerHealthStatus,
  WorkerInviteKind,
  WorkerVersionDrift,
  type components,
  type paths,
} from '@/api/v1/schema';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';

type Worker = components['schemas']['Worker'];
type WorkerInvite = components['schemas']['WorkerInvite'];

interface Answer {
  status: number;
  body?: unknown;
}

interface Request {
  method: string;
  path: string;
  query: URLSearchParams;
}

/**
 * A fake server behind the real API client and SWR cache: it answers the
 * invites list with `invites` and every DELETE with `del`, and records
 * each request.
 */
export const server = {
  invites: { status: 200, body: { invites: [] } } as Answer,
  del: ((): Answer => ({ status: 204 })) as (request: Request) => Answer,
  requests: [] as Request[],
  /** The node System status shows. */
  node: 'local',
};

/**
 * The hooks/api module on the real API client and SWR query hook, with an
 * absolute base URL that the test fetch accepts.
 */
export function realApiModule() {
  const client = createClient<paths>({
    baseUrl: 'http://localhost/api/v1',
    fetch: (request) => globalThis.fetch(request),
  });
  return { useClient: () => client, useQuery: createQueryHook(client, '/') };
}

/** The requests with this method and API path, such as '/workers/invites'. */
export function requests(method: string, path: string): Request[] {
  return server.requests.filter(
    (request) => request.method === method && request.path === `/api/v1${path}`
  );
}

export function installServer() {
  server.invites = { status: 200, body: { invites: [] } };
  server.del = () => ({ status: 204 });
  server.requests = [];
  server.node = 'local';
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const target =
        typeof input === 'string'
          ? input
          : input instanceof URL
            ? input.href
            : input.url;
      const url = new URL(target, 'http://localhost');
      const method = (
        init?.method ?? (input instanceof Request ? input.method : 'GET')
      ).toUpperCase();
      const request = { method, path: url.pathname, query: url.searchParams };
      server.requests.push(request);
      const answer =
        method === 'DELETE'
          ? server.del(request)
          : url.pathname === '/api/v1/workers/invites'
            ? server.invites
            : { status: 404, body: { code: 'not_found', message: 'none' } };
      return answer.status === 204
        ? new Response(null, { status: 204 })
        : new Response(JSON.stringify(answer.body ?? {}), {
            status: answer.status,
            headers: { 'Content-Type': 'application/json' },
          });
    })
  );
}

/** An API error answer. */
export function refused(status: number, message = '', code = 'conflict') {
  return { status, body: { code, message } };
}

export function invitesAnswer(invites: WorkerInvite[]): Answer {
  return { status: 200, body: { invites } };
}

export function worker(id: string, extra: Partial<Worker> = {}): Worker {
  return {
    id,
    labels: {},
    totalPollers: 2,
    busyPollers: 0,
    runningTasks: [],
    lastHeartbeatAt: new Date().toISOString(),
    healthStatus: WorkerHealthStatus.healthy,
    versionDrift: WorkerVersionDrift.none,
    connection: WorkerConnection.certificate,
    certificateId: `cert-${id}`,
    ...extra,
  };
}

export function invite(
  id: string,
  extra: Partial<WorkerInvite> = {}
): WorkerInvite {
  return {
    id,
    kind: WorkerInviteKind.single_use,
    createdBy: 'admin',
    createdAt: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
    useCount: 0,
    ...extra,
  };
}

/** An ISO time `hours` from now; negative is in the past. */
export function hoursFromNow(hours: number): string {
  return new Date(Date.now() + hours * 60 * 60 * 1000).toISOString();
}

/** Sets whether the tab is visible, as the browser reports it. */
export function setTabVisible(visible: boolean) {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => (visible ? 'visible' : 'hidden'),
  });
  Object.defineProperty(document, 'hidden', {
    configurable: true,
    get: () => !visible,
  });
  document.dispatchEvent(new Event('visibilitychange'));
}

// Each test gets its own SWR cache, so request counts start at zero.
export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <SWRConfig value={{ provider: () => new Map() }}>
      <RemoteNodeProvider remoteNode={server.node}>
        <UserPreferencesProvider>
          <I18nProvider>{children}</I18nProvider>
        </UserPreferencesProvider>
      </RemoteNodeProvider>
    </SWRConfig>
  );
}
