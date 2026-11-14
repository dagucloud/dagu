// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Upgrade Dagu from the web UI.
 *
 * @module hooks
 */
import * as React from 'react';
import { SelfUpgradeState, type components } from '@/api/v1/schema';
import { useClient } from './api';

/** How often an upgrade in progress is polled, in milliseconds. */
const UPGRADE_POLL_INTERVAL = 1000;
/** How often the restarting server is probed, in milliseconds. */
const RESTART_POLL_INTERVAL = 2000;
/** How long a restart may take before the UI says it is taking long. */
const SLOW_RESTART = 120_000;

export type SelfUpgradeStatus = components['schemas']['SelfUpgradeStatus'];

function reloadPage() {
  window.location.reload();
}

/**
 * Starts an upgrade from the web UI and follows it: while the release
 * downloads and installs, then until the restarted server reports a version
 * other than currentVersion, when onRestarted runs; by default it reloads
 * the page. Only the tab that started the upgrade polls, and only until then.
 */
export function useSelfUpgrade(
  currentVersion: string,
  onRestarted: () => void = reloadPage
) {
  const client = useClient();
  const [status, setStatus] = React.useState<SelfUpgradeStatus | null>(null);
  const [requestError, setRequestError] = React.useState<string | null>(null);
  const [slowRestart, setSlowRestart] = React.useState(false);

  const start = React.useCallback(async () => {
    setRequestError(null);
    const { data, error } = await client.POST('/system/upgrade', {
      params: { query: { remoteNode: 'local' } },
    });
    if (error || !data) {
      setRequestError(error?.message || 'Failed to start the update');
      return;
    }
    setStatus(data);
  }, [client]);

  const state = status?.state;
  React.useEffect(() => {
    if (
      state === SelfUpgradeState.downloading ||
      state === SelfUpgradeState.installing
    ) {
      const timer = window.setTimeout(async () => {
        try {
          const { data } = await client.GET('/system/upgrade', {
            params: { query: { remoteNode: 'local' } },
          });
          // A server that stopped answering is restarting.
          setStatus(
            (prev) =>
              data ??
              (prev ? { ...prev, state: SelfUpgradeState.restarting } : prev)
          );
        } catch {
          setStatus((prev) =>
            prev ? { ...prev, state: SelfUpgradeState.restarting } : prev
          );
        }
      }, UPGRADE_POLL_INTERVAL);
      return () => window.clearTimeout(timer);
    }
    if (state === SelfUpgradeState.restarting) {
      const slow = window.setTimeout(() => setSlowRestart(true), SLOW_RESTART);
      const probe = window.setInterval(async () => {
        try {
          const { data } = await client.GET('/health');
          if (data && data.version !== currentVersion) {
            onRestarted();
          }
        } catch {
          // The server is still restarting.
        }
      }, RESTART_POLL_INTERVAL);
      return () => {
        window.clearTimeout(slow);
        window.clearInterval(probe);
      };
    }
    return undefined;
    // status re-arms the poll after each answer while the state stays the same.
  }, [client, state, status, currentVersion, onRestarted]);

  return { status, requestError, slowRestart, start };
}
