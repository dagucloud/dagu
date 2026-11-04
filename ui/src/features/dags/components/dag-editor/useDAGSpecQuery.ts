// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { SWRConfiguration } from 'swr';
import { useQuery } from '@/hooks/api';

/**
 * The spec of a workflow file on a remote node. The workflow page asks for
 * it with the details of the workflow, and the editor reads the same
 * request: one key for both, so that the editor mounts on its answer.
 */
export function useDAGSpecQuery(
  fileName: string,
  remoteNode: string,
  options?: SWRConfiguration
) {
  return useQuery(
    '/dags/{fileName}/spec',
    { params: { query: { remoteNode }, path: { fileName } } },
    options
  );
}
