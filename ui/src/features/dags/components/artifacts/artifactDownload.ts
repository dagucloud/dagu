// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { useClient } from '@/hooks/api';

type Client = ReturnType<typeof useClient>;

/** Identifies the DAG run whose artifacts are read. */
export type ArtifactRunRef = {
  dagRunName: string;
  dagRunId: string;
  subDAGRunId?: string | null;
  remoteNode: string;
};

/** Downloads one run artifact as a blob, throwing when the request fails. */
export async function fetchArtifactDownload(
  client: Client,
  run: ArtifactRunRef,
  path: string,
  signal?: AbortSignal
) {
  const request = run.subDAGRunId
    ? await client.GET(
        '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/artifacts/download',
        {
          params: {
            path: {
              name: run.dagRunName,
              dagRunId: run.dagRunId,
              subDAGRunId: run.subDAGRunId,
            },
            query: { remoteNode: run.remoteNode, path },
          },
          parseAs: 'blob',
          signal,
        }
      )
    : await client.GET('/dag-runs/{name}/{dagRunId}/artifacts/download', {
        params: {
          path: { name: run.dagRunName, dagRunId: run.dagRunId },
          query: { remoteNode: run.remoteNode, path },
        },
        parseAs: 'blob',
        signal,
      });

  if (request.error) {
    throw new Error(
      request.error.message || request.response.statusText || 'Download failed'
    );
  }

  return request;
}
