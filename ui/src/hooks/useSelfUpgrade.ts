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
import { subscribeClock } from './useClock';

/** How often an upgrade in progress is polled, in milliseconds. */
const UPGRADE_POLL_INTERVAL = 1000;
/** How often the restarting server is probed, in milliseconds. */
const RESTART_POLL_INTERVAL = 2000;
/** How long a restart may take before the UI says it is taking long. */
const SLOW_RESTART = 120_000;
/** States a server that still answers can report while the UI waits. */
const RESUMED_STATES = new Set<SelfUpgradeState>([
  SelfUpgradeState.downloading,
  SelfUpgradeState.installing,
  SelfUpgradeState.failed,
]);

/** States of an upgrade that is still under way. */
const ACTIVE_STATES = new Set<SelfUpgradeState>([
  SelfUpgradeState.downloading,
  SelfUpgradeState.installing,
  SelfUpgradeState.restarting,
]);

export type SelfUpgradeStatus = components['schemas']['SelfUpgradeStatus'];

function reloadPage() {
  window.location.reload();
}

/**
 * Runs request on the shared clock every period ms, which pauses while the
 * tab is hidden, and skips a tick while the previous request is in flight.
 * Returns the function that stops it.
 */
function pollOnClock(period: number, request: () => Promise<void>) {
  let inFlight = false;
  return subscribeClock(period, () => {
    if (inFlight) return;
    inFlight = true;
    void request().finally(() => {
      inFlight = false;
    });
  });
}

/**
 * Starts an upgrade from the web UI and follows it: while the release
 * downloads and installs, then until the restarted server reports a version
 * other than currentVersion, when onRestarted runs; by default it reloads
 * the page. Only a tab that asked to start the upgrade polls, and only until
 * then.
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
    let failure = 'Failed to start the update';
    try {
      const { data, error } = await client.POST('/system/upgrade', {
        params: { query: { remoteNode: 'local' } },
      });
      if (!error && data) {
        setStatus(data);
        return;
      }
      failure = error?.message || failure;
    } catch (err) {
      if (err instanceof Error) failure = err.message;
    }
    // The server may already run an upgrade: one started before a reload, or
    // this start when only its answer was lost. Follow it instead.
    try {
      const { data } = await client.GET('/system/upgrade', {
        params: { query: { remoteNode: 'local' } },
      });
      if (data && ACTIVE_STATES.has(data.state)) {
        setStatus(data);
        return;
      }
    } catch {
      // The start failure below says more than this one.
    }
    setRequestError(failure);
  }, [client]);

  const state = status?.state;
  React.useEffect(() => {
    if (
      state === SelfUpgradeState.downloading ||
      state === SelfUpgradeState.installing
    ) {
      const restarting = (prev: SelfUpgradeStatus | null) =>
        prev ? { ...prev, state: SelfUpgradeState.restarting } : prev;
      return pollOnClock(UPGRADE_POLL_INTERVAL, async () => {
        try {
          const { data } = await client.GET('/system/upgrade', {
            params: { query: { remoteNode: 'local' } },
          });
          // A server that stopped answering is restarting, and one that
          // reports idle has already restarted.
          setStatus((prev) =>
            data && data.state !== SelfUpgradeState.idle
              ? data
              : restarting(prev)
          );
        } catch {
          setStatus(restarting);
        }
      });
    }
    if (state === SelfUpgradeState.restarting) {
      // probe asks whether the restarted server is back, and otherwise
      // whether the server that still answers reports the upgrade failed or
      // still running, as after one missed poll.
      const probe = async () => {
        try {
          const { data: health } = await client.GET('/health');
          if (health && health.version !== currentVersion) {
            onRestarted();
            return;
          }
          const { data } = await client.GET('/system/upgrade', {
            params: { query: { remoteNode: 'local' } },
          });
          if (data && RESUMED_STATES.has(data.state)) {
            setStatus(data);
          }
        } catch {
          // The server is still restarting.
        }
      };
      const stop = pollOnClock(RESTART_POLL_INTERVAL, probe);
      const slow = window.setTimeout(() => setSlowRestart(true), SLOW_RESTART);
      return () => {
        window.clearTimeout(slow);
        stop();
      };
    }
    return undefined;
  }, [client, state, currentVersion, onRestarted]);

  return { status, requestError, slowRestart, start };
}
