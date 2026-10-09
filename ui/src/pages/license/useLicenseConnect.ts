// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useState } from 'react';
import { LicenseConnectStatusState, type components } from '@/api/v1/schema';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';

export type LicenseConnectStatus =
  components['schemas']['LicenseConnectStatus'];

const POLL_INTERVAL_MS = 2000;

/**
 * Drives a request to connect the selected server to Dagu Console: starts it,
 * opens the approval page, and follows it until it is approved, fails, or
 * expires.
 */
export function useLicenseConnect(remoteNode: string) {
  const client = useClient();
  const { ts } = useI18n();
  const [status, setStatus] = useState<LicenseConnectStatus | null>(null);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [popupBlocked, setPopupBlocked] = useState(false);

  const pending = status?.state === LicenseConnectStatusState.pending;
  useEffect(() => {
    if (!pending) return;
    let stopped = false;
    const timer = window.setInterval(async () => {
      const { data } = await client.GET('/license/connect', {
        params: { query: { remoteNode } },
      });
      if (!stopped && data) setStatus(data);
    }, POLL_INTERVAL_MS);
    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [pending, client, remoteNode]);

  const start = useCallback(async () => {
    // Opened before any await so the browser treats it as a user action.
    const tab = window.open('about:blank', '_blank');
    if (tab) tab.opener = null;
    setStarting(true);
    setError(null);
    setPopupBlocked(false);
    try {
      const { data, error: apiError } = await client.POST('/license/connect', {
        params: { query: { remoteNode } },
      });
      if (apiError || !data) {
        throw new Error(
          apiError?.message || ts('Could not start connecting to Dagu Console.')
        );
      }
      setStatus(data);
      if (tab && data.connectUrl) {
        tab.location.href = data.connectUrl;
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
  }, [client, remoteNode, ts]);

  const cancel = useCallback(async () => {
    setStatus(null);
    await client.DELETE('/license/connect', {
      params: { query: { remoteNode } },
    });
  }, [client, remoteNode]);

  return { status, starting, error, popupBlocked, start, cancel };
}
