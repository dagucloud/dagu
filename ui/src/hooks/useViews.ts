// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useContext } from 'react';
import { components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient, useQuery } from '@/hooks/api';

export type View = components['schemas']['View'];
export type ViewSpec = components['schemas']['ViewSpec'];

async function refreshViews(mutate: () => Promise<unknown>): Promise<void> {
  try {
    await mutate();
  } catch {
    // The write already succeeded; a later revalidation can recover the list.
  }
}

/**
 * useViews loads the shared saved views and exposes CRUD mutations. It is
 * provider-safe: when the request fails (or no SWR provider is present) it
 * yields an empty list rather than throwing, so it can be called from the
 * sidebar and overview tabs unconditionally.
 */
export function useViews() {
  const client = useClient();
  const appBar = useContext(AppBarContext);
  const remoteNode = appBar.selectedRemoteNode || 'local';

  const { data, error, isLoading, mutate } = useQuery('/views', {
    params: { query: { remoteNode } },
  });

  const views: View[] = data?.views ?? [];

  const createView = useCallback(
    async (spec: ViewSpec): Promise<View> => {
      const res = await client.POST('/views', {
        params: { query: { remoteNode } },
        body: spec,
      });
      if (res.error) {
        throw new Error(res.error.message || 'Failed to create view');
      }
      const created = res.data as View;
      await refreshViews(mutate);
      return created;
    },
    [client, remoteNode, mutate]
  );

  const updateView = useCallback(
    async (id: string, spec: ViewSpec): Promise<View> => {
      const res = await client.PUT('/views/{viewId}', {
        params: { path: { viewId: id }, query: { remoteNode } },
        body: spec,
      });
      if (res.error) {
        throw new Error(res.error.message || 'Failed to update view');
      }
      const updated = res.data as View;
      await refreshViews(mutate);
      return updated;
    },
    [client, remoteNode, mutate]
  );

  const deleteView = useCallback(
    async (id: string): Promise<void> => {
      const res = await client.DELETE('/views/{viewId}', {
        params: { path: { viewId: id }, query: { remoteNode } },
      });
      if (res.error) {
        throw new Error(res.error.message || 'Failed to delete view');
      }
      await refreshViews(mutate);
    },
    [client, remoteNode, mutate]
  );

  return {
    views,
    isLoading,
    error,
    createView,
    updateView,
    deleteView,
    refresh: mutate,
  };
}
