// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useRef } from 'react';
import { type components, LicenseMonitoringLevel } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { useClient, useQuery } from './api';

export type LicenseMonitoring = components['schemas']['LicenseMonitoring'];

/**
 * How often a server set to report is read while its first report has not
 * shown, as right after it connects, and for how long.
 */
const FIRST_REPORT_REFRESH_MS = 3_000;
const FIRST_REPORT_WAIT_MS = 2 * 60_000;

/**
 * Reads what a server reports to Dagu Console and lets an administrator
 * change it. Nothing is requested while `enabled` is false. A refreshing
 * read refreshes sooner until the first report shows.
 */
export function useLicenseMonitoring(
  remoteNode: string,
  { enabled = true, refreshInterval = 0 } = {}
) {
  const client = useClient();
  const { ts } = useI18n();
  // When the server began to wait for its first report to show.
  const awaitingSince = useRef<number | null>(null);
  const { data, error, mutate } = useQuery(
    '/license/monitoring',
    enabled ? { params: { query: { remoteNode } } } : null,
    {
      refreshInterval: (latest?: LicenseMonitoring) => {
        const awaiting =
          refreshInterval > 0 &&
          latest !== undefined &&
          latest.level !== LicenseMonitoringLevel.off &&
          !latest.lastReportAt;
        if (!awaiting) {
          awaitingSince.current = null;
          return refreshInterval;
        }
        awaitingSince.current ??= Date.now();
        return Date.now() - awaitingSince.current < FIRST_REPORT_WAIT_MS
          ? FIRST_REPORT_REFRESH_MS
          : refreshInterval;
      },
    }
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

  const dismissErrorsNotice = useCallback(async () => {
    const { data: next, error: apiError } = await client.POST(
      '/license/monitoring/dismiss-errors-notice',
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
    dismissErrorsNotice,
  };
}

/**
 * Reports whether an administrator should be asked what the server reports:
 * configuration leaves the level open and nobody has chosen one yet.
 */
export function shouldAskMonitoring(monitoring?: LicenseMonitoring): boolean {
  return Boolean(monitoring && !monitoring.configured && !monitoring.chosen);
}

/**
 * Reports whether an administrator should be offered to also send error
 * messages: they chose run status, and have not dismissed the offer.
 */
export function shouldOfferErrors(monitoring?: LicenseMonitoring): boolean {
  return Boolean(
    monitoring &&
      !monitoring.configured &&
      monitoring.chosen &&
      monitoring.level === LicenseMonitoringLevel.runs &&
      !monitoring.errorsOffered
  );
}
