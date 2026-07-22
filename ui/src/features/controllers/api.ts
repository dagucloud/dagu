// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import useSWR, { type SWRResponse } from 'swr';

import { useClient } from '@/hooks/api';
import { isControllerActive } from './types';
import type { ControllerDetail, ControllerListResponse } from './types';

export type ControllerDAGOption = {
  fileName: string;
  name: string;
  description?: string;
  params: string[];
};

type ClientResponse = {
  data?: unknown;
  error?: unknown;
  response: Response;
};

type ControllerClient = {
  GET: (
    path: string,
    init?: Record<string, unknown>
  ) => Promise<ClientResponse>;
  POST: (
    path: string,
    init?: Record<string, unknown>
  ) => Promise<ClientResponse>;
  PUT: (
    path: string,
    init?: Record<string, unknown>
  ) => Promise<ClientResponse>;
  DELETE: (
    path: string,
    init?: Record<string, unknown>
  ) => Promise<ClientResponse>;
};

export class ControllerAPIError extends Error {
  readonly status: number;
  readonly details?: unknown;

  constructor(message: string, status: number, details?: unknown) {
    super(message);
    this.name = 'ControllerAPIError';
    this.status = status;
    this.details = details;
  }
}

function errorFields(value: unknown): { message?: string; details?: unknown } {
  if (!value || typeof value !== 'object') return {};
  const record = value as Record<string, unknown>;
  return {
    message: typeof record.message === 'string' ? record.message : undefined,
    details: record.details,
  };
}

function unwrap<T>(response: ClientResponse): T {
  if (response.error !== undefined) {
    const fields = errorFields(response.error);
    throw new ControllerAPIError(
      fields.message ??
        `Controller request failed (${response.response.status})`,
      response.response.status,
      fields.details
    );
  }
  return response.data as T;
}

function unwrapVoid(response: ClientResponse): void {
  if (response.error !== undefined) {
    unwrap<never>(response);
  }
}

function useRawControllerClient(): ControllerClient {
  return useClient() as unknown as ControllerClient;
}

export function useControllerList(
  workspace: string
): SWRResponse<ControllerListResponse, ControllerAPIError> {
  const client = useRawControllerClient();
  return useSWR<ControllerListResponse, ControllerAPIError>(
    ['controllers', 'list', workspace],
    async () =>
      unwrap<ControllerListResponse>(
        await client.GET('/controllers', {
          params: { query: { workspace } },
        })
      ),
    { revalidateOnFocus: true }
  );
}

export function useControllerDetail(
  id: string | undefined,
  workspaceKey: string
): SWRResponse<ControllerDetail, ControllerAPIError> {
  const client = useRawControllerClient();
  return useSWR<ControllerDetail, ControllerAPIError>(
    id ? ['controllers', 'detail', workspaceKey, id] : null,
    async () =>
      unwrap<ControllerDetail>(
        await client.GET('/controllers/{id}', {
          params: { path: { id } },
        })
      ),
    {
      refreshInterval: (detail) =>
        isControllerActive(detail?.runtime) ? 2_000 : 0,
      revalidateOnFocus: true,
    }
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function useControllerDAGOptions(
  workspace: string
): SWRResponse<ControllerDAGOption[], ControllerAPIError> {
  const client = useRawControllerClient();
  return useSWR<ControllerDAGOption[], ControllerAPIError>(
    ['controllers', 'dag-options', workspace || 'default'],
    async () => {
      const result = unwrap<{ dags?: unknown[] }>(
        await client.GET('/dags', {
          params: {
            query: {
              remoteNode: 'local',
              workspace: workspace || 'default',
              page: 1,
              perPage: 100,
              sort: 'name',
              order: 'asc',
            },
          },
        })
      );
      return (result.dags ?? []).flatMap((value): ControllerDAGOption[] => {
        if (!isRecord(value) || typeof value.fileName !== 'string') return [];
        const dag = isRecord(value.dag) ? value.dag : {};
        return [
          {
            fileName: value.fileName,
            name: typeof dag.name === 'string' ? dag.name : value.fileName,
            description:
              typeof dag.description === 'string' ? dag.description : undefined,
            params: Array.isArray(dag.params)
              ? dag.params.filter(
                  (param): param is string => typeof param === 'string'
                )
              : [],
          },
        ];
      });
    },
    { revalidateOnFocus: true }
  );
}

export function useControllerAPI() {
  const client = useRawControllerClient();

  return React.useMemo(
    () => ({
      get: async (id: string): Promise<ControllerDetail> =>
        unwrap<ControllerDetail>(
          await client.GET('/controllers/{id}', {
            params: { path: { id } },
          })
        ),
      create: async (spec: string): Promise<{ id: string }> =>
        unwrap<{ id: string }>(
          await client.POST('/controllers', { body: { spec } })
        ),
      update: async (id: string, spec: string): Promise<ControllerDetail> =>
        unwrap<ControllerDetail>(
          await client.PUT('/controllers/{id}/spec', {
            params: { path: { id } },
            body: { spec },
          })
        ),
      delete: async (id: string): Promise<void> => {
        unwrapVoid(
          await client.DELETE('/controllers/{id}', {
            params: { path: { id } },
          })
        );
      },
      start: async (id: string, prompt: string): Promise<void> => {
        unwrap<void>(
          await client.POST('/controllers/{id}/start', {
            params: { path: { id } },
            body: { prompt },
          })
        );
      },
      prompt: async (id: string, prompt: string): Promise<void> => {
        unwrap<void>(
          await client.POST('/controllers/{id}/prompt', {
            params: { path: { id } },
            body: { prompt },
          })
        );
      },
      stop: async (id: string): Promise<void> => {
        unwrap<void>(
          await client.POST('/controllers/{id}/stop', {
            params: { path: { id } },
          })
        );
      },
    }),
    [client]
  );
}
