// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { SWRConfiguration } from 'swr';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';

/**
 * The details of a workflow file on a remote node. The workflow page reads
 * them, and the overlay asks for them at the click that opens the page: one
 * key for both, so that the page mounts on their answer. A disabled query
 * asks for nothing.
 */
export function useDAGDetailsQuery(
  fileName: string,
  remoteNode: string,
  options?: SWRConfiguration,
  enabled = true
) {
  return useQuery(
    '/dags/{fileName}',
    whenEnabled(enabled, {
      params: { query: { remoteNode }, path: { fileName } },
    }),
    options
  );
}
