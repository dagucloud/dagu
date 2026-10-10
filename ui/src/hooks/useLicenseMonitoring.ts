// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback } from 'react';
import type { components, LicenseMonitoringLevel } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { useClient, useQuery } from './api';

export type LicenseMonitoring = components['schemas']['LicenseMonitoring'];

/**
 * Reads what a server reports to Dagu Console and lets an administrator
 * change it. Nothing is requested while `enabled` is false.
 */
export function useLicenseMonitoring(
  remoteNode: string,
  { enabled = true, refreshInterval = 0 } = {}
) {
  const client = useClient();
  const { ts } = useI18n();
  const { data, error, mutate } = useQuery(
    '/license/monitoring',
    enabled ? { params: { query: { remoteNode } } } : null,
    { refreshInterval }
  );

  const setLevel = useCallback(
    async (level: LicenseMonitoringLevel) => {
      const { data: next, error: apiError } = await client.PUT(
        '/license/monitoring',
        { params: { query: { remoteNode } }, body: { level } }
      );
      if (apiError || !next) {
        throw new Error(
          apiError?.message || ts('Could not change what this server reports.')
        );
      }
      await mutate(next, { revalidate: false });
    },
    [client, mutate, remoteNode, ts]
  );

  const dismissNotice = useCallback(async () => {
    const { data: next, error: apiError } = await client.POST(
      '/license/monitoring/dismiss-notice',
      { params: { query: { remoteNode } } }
    );
    if (apiError || !next) {
      throw new Error(apiError?.message || ts('Could not dismiss the notice.'));
    }
    await mutate(next, { revalidate: false });
  }, [client, mutate, remoteNode, ts]);

  return {
    monitoring: data,
    error,
    refresh: mutate,
    setLevel,
    dismissNotice,
  };
}

/**
 * Reports whether an administrator should be asked what the server reports:
 * configuration leaves the level open and nobody has chosen one yet.
 */
export function shouldAskMonitoring(monitoring?: LicenseMonitoring): boolean {
  return Boolean(monitoring && !monitoring.configured && !monitoring.chosen);
}
