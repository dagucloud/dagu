// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The workers list of System status.
 *
 * @module features/workers/hooks
 */
import { useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';

/** How often the list is read while the page's auto refresh is on. */
const WORKERS_REFRESH_MS = 2000;

/**
 * Reads the workers of a node, every 2 seconds while `live`. A failed read,
 * or an answer that carries errors, shows the page's own words in the list,
 * not the server's, and raises no page notice: a removal reads the list
 * again also while the refresh is paused, and its failure belongs to the
 * list.
 */
export function useWorkersList(remoteNode: string, live: boolean) {
  const { ts } = useI18n();
  const { data, error, isLoading, mutate } = useQuery(
    '/workers',
    { params: { query: { remoteNode } } },
    {
      refreshInterval: live ? WORKERS_REFRESH_MS : 0,
      onError: () => undefined,
    }
  );
  return {
    workers: data?.workers,
    errors: error
      ? [
          ts(
            'The workers list could not be read. Refresh the page, or check the server connection.'
          ),
        ]
      : data?.errors?.length
        ? [
            ts(
              'The coordinator could not report the workers. Check the coordinator log, then refresh the page.'
            ),
          ]
        : undefined,
    isLoading: !data && !error,
    settled: !isLoading,
    mutate,
  };
}
