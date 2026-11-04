// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { SWRConfiguration } from 'swr';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';

/**
 * The spec of a workflow file on a remote node. The workflow page asks for
 * it with the details of the workflow, and the editor reads the same
 * request: one key for both, so that the editor mounts on its answer. A
 * disabled query asks for nothing.
 */
export function useDAGSpecQuery(
  fileName: string,
  remoteNode: string,
  options?: SWRConfiguration,
  enabled = true
) {
  return useQuery(
    '/dags/{fileName}/spec',
    whenEnabled(enabled, {
      params: { query: { remoteNode }, path: { fileName } },
    }),
    options
  );
}
