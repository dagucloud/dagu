// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  LicenseConnectStatusState,
  type components,
  type LicenseMonitoringLevel,
} from '@/api/v1/schema';
import { useClient } from '@/hooks/api';
import { subscribeClock } from '@/hooks/useClock';
import { useI18n } from '@/i18n/I18nProvider';

export type LicenseConnectStatus =
  components['schemas']['LicenseConnectStatus'];

const POLL_INTERVAL_MS = 2000;

/** Asks the approval page to offer the trial of paid features first. */
function withTrial(url: string | undefined, trial: boolean) {
  if (!url || !trial) return url;
  const next = new URL(url);
  next.searchParams.set('trial', '1');
  return next.toString();
}

/**
 * Drives a request to connect the selected server to Dagu Console: starts it,
 * opens the approval page, and follows it until it is approved, fails, or
 * expires. `start` takes what the server reports once connected, if chosen,
 * and whether to start the trial of paid features; `retry` starts again with
 * the same choices. `connectUrl` opens the approval page of the pending
 * request. `failure` says why the last request failed, if it did.
 */
export function useLicenseConnect(remoteNode: string) {
  const client = useClient();
  const { ts } = useI18n();
  const [status, setStatus] = useState<LicenseConnectStatus | null>(null);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [popupBlocked, setPopupBlocked] = useState(false);
  const lastReport = useRef<LicenseMonitoringLevel | undefined>(undefined);
  const [trial, setTrial] = useState(false);

  const pending = status?.state === LicenseConnectStatusState.pending;
  useEffect(() => {
    if (!pending) return;
    let stopped = false;
    const stopPolling = subscribeClock(POLL_INTERVAL_MS, async () => {
      try {
        const { data } = await client.GET('/license/connect', {
          params: { query: { remoteNode } },
        });
        if (!stopped && data) setStatus(data);
      } catch {
        // A failed poll is retried on the next tick.
      }
    });
    return () => {
      stopped = true;
      stopPolling();
    };
  }, [pending, client, remoteNode]);

  const start = useCallback(
    async (report?: LicenseMonitoringLevel, withTrialOffer = false) => {
      // Opened before any await so the browser treats it as a user action.
      const tab = window.open('about:blank', '_blank');
      if (tab) tab.opener = null;
      lastReport.current = report;
      setTrial(withTrialOffer);
      setStarting(true);
      setError(null);
      setPopupBlocked(false);
      try {
        const { data, error: apiError } = await client.POST(
          '/license/connect',
          {
            params: { query: { remoteNode } },
            ...(report && { body: { report } }),
          }
        );
        if (apiError || !data) {
          throw new Error(
            apiError?.message ||
              ts('Could not start connecting to Dagu Console.')
          );
        }
        setStatus(data);
        if (tab && data.connectUrl) {
          tab.location.href = withTrial(data.connectUrl, withTrialOffer)!;
        } else {
          tab?.close();
          setPopupBlocked(true);
        }
      } catch (err) {
        tab?.close();
        setError(
          err instanceof Error
            ? err.message
            : ts('Could not start connecting to Dagu Console.')
        );
      } finally {
        setStarting(false);
      }
    },
    [client, remoteNode, ts]
  );

  const retry = useCallback(
    () => start(lastReport.current, trial),
    [start, trial]
  );

  // The request stays pending here until the server confirms the cancel;
  // otherwise an approval could still install a license after "Cancel".
  const cancel = useCallback(async () => {
    setError(null);
    const failed = ts('Could not cancel the request. Try again.');
    try {
      const { data, error: apiError } = await client.DELETE(
        '/license/connect',
        { params: { query: { remoteNode } } }
      );
      if (apiError || !data) throw new Error(apiError?.message || failed);
      setStatus(data);
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : failed);
    }
  }, [client, remoteNode, ts]);

  const clear = useCallback(() => setStatus(null), []);

  // A cancel that fails while the request still waits is not a failure of
  // the request itself.
  const failure = pending
    ? undefined
    : error ||
      (status?.state === LicenseConnectStatusState.failed ||
      status?.state === LicenseConnectStatusState.expired
        ? status.error
        : undefined);

  return {
    status,
    connectUrl: withTrial(status?.connectUrl, trial),
    starting,
    error,
    failure,
    popupBlocked,
    start,
    retry,
    cancel,
    clear,
  };
}
