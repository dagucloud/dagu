// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import useSWR from 'swr';

import {
  PathsDagsGetParametersQueryOrder,
  PathsDagsGetParametersQuerySort,
} from '@/api/v1/schema';
import { useClient, useQuery } from '@/hooks/api';
import { controllerDAGOption, type ControllerDAGOption } from './dagOptions';
import { isControllerActive } from './types';

type ClientResponse<T> = {
  data?: T;
  error?: { message?: string };
  response: Response;
};

function unwrap<T>(response: ClientResponse<T>): T {
  if (response.error !== undefined) {
    throw new Error(
      response.error.message ??
        `Controller request failed (${response.response.status})`
    );
  }
  return response.data as T;
}

export function useControllerList(workspace: string) {
  return useQuery(
    '/controllers',
    { params: { query: { workspace } } },
    {
      revalidateOnFocus: true,
    }
  );
}

export function useControllerDetail(id: string | undefined) {
  return useQuery(
    '/controllers/{id}',
    id ? { params: { path: { id } } } : null,
    {
      refreshInterval: (detail) =>
        isControllerActive(detail?.runtime) ? 2_000 : 0,
      revalidateOnFocus: true,
    }
  );
}

export function useControllerDAGOptions(workspace: string) {
  const client = useClient();
  const effectiveWorkspace = workspace || 'default';
  const query = useSWR(
    ['controllers', 'dag-options', effectiveWorkspace],
    async () => {
      const options: ControllerDAGOption[] = [];
      let page = 1;

      for (;;) {
        const result = unwrap(
          await client.GET('/dags', {
            params: {
              query: {
                remoteNode: 'local',
                workspace: effectiveWorkspace,
                page,
                perPage: 200,
                sort: PathsDagsGetParametersQuerySort.name,
                order: PathsDagsGetParametersQueryOrder.asc,
              },
            },
          })
        );
        if (result.pagination.currentPage !== page) {
          throw new Error('DAG pagination is inconsistent');
        }
        for (const value of result.dags) {
          const option = controllerDAGOption(value);
          if (option) options.push(option);
        }
        if (result.pagination.currentPage >= result.pagination.totalPages) {
          return options;
        }
        page += 1;
      }
    },
    { revalidateOnFocus: true }
  );
  return { ...query, data: query.data ?? [] };
}

export function useControllerAPI() {
  const client = useClient();

  return React.useMemo(
    () => ({
      get: async (id: string) =>
        unwrap(
          await client.GET('/controllers/{id}', {
            params: { path: { id } },
          })
        ),
      create: async (spec: string) =>
        unwrap(await client.POST('/controllers', { body: { spec } })),
      update: async (id: string, spec: string) =>
        unwrap(
          await client.PUT('/controllers/{id}/spec', {
            params: { path: { id } },
            body: { spec },
          })
        ),
      delete: async (id: string): Promise<void> => {
        unwrap(
          await client.DELETE('/controllers/{id}', {
            params: { path: { id } },
          })
        );
      },
      start: async (id: string, prompt: string): Promise<void> => {
        unwrap(
          await client.POST('/controllers/{id}/start', {
            params: { path: { id } },
            body: { prompt },
          })
        );
      },
      prompt: async (id: string, prompt: string): Promise<void> => {
        unwrap(
          await client.POST('/controllers/{id}/prompt', {
            params: { path: { id } },
            body: { prompt },
          })
        );
      },
      stop: async (id: string): Promise<void> => {
        unwrap(
          await client.POST('/controllers/{id}/stop', {
            params: { path: { id } },
          })
        );
      },
    }),
    [client]
  );
}
