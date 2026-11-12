// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useEffect } from 'react';
import { APIKeyAllowedSurfaces } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  useCanManageNotifications,
  useCanWrite,
  useIsAdmin,
} from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useQuery } from '@/hooks/api';
import { useLicenseState } from '@/hooks/useLicense';
import { useStoredValue } from '@/hooks/useStoredValue';
import { isLicenseManagedByEnv } from '@/lib/license';
import { WorkspaceKind } from '@/lib/workspace';
import {
  DAG_SAMPLE_SIZE,
  progressFromSample,
  RETRY_EXAMPLE_FILE,
} from '../lib/gettingStarted';

/** Stored per server; unset until the checklist is first evaluated. */
const STARTED = 'started';
const DISMISSED = 'dismissed';
const DONE = 'done';

export type GettingStartedStep =
  | { id: 'run'; done: boolean; exampleFile?: string }
  | { id: 'create'; done: boolean }
  | { id: 'schedule'; done: boolean }
  | { id: 'alert'; done: boolean; canConnect: boolean; canAddChannel: boolean }
  | { id: 'ai'; done: boolean; canUseCloud: boolean; canCreateKey: boolean };

export type GettingStarted =
  | { status: 'hidden' | 'pending' }
  | { status: 'visible'; steps: GettingStartedStep[]; dismiss: () => void };

const queryOptions = { revalidateOnFocus: false };

/**
 * Tracks the first steps on a new server for users who can write workflows.
 * A server whose first three steps are done when first seen never shows the
 * checklist. Otherwise it shows until dismissed or until every step is done,
 * and either outcome is remembered per server. While its data loads it is
 * pending; when a request fails it is hidden and nothing is remembered.
 */
export function useGettingStarted(): GettingStarted {
  const { selectedRemoteNode } = useContext(AppBarContext);
  const remoteNode = selectedRemoteNode || 'local';
  const canWrite = useCanWrite();
  const isAdmin = useIsAdmin();
  const { authMode } = useConfig();
  const canAddChannel = useCanManageNotifications();
  const { license, loading, error: licenseError } = useLicenseState();
  const [stored, setStored] = useStoredValue(
    `dagu.gettingStarted:${remoteNode}`
  );
  const active = canWrite && stored !== DISMISSED && stored !== DONE;
  const community = license.community;
  const canConnect = isAdmin && community && !isLicenseManagedByEnv(license);
  const hasAlertStep = canConnect || canAddChannel;
  // MCP clients reach the server through Dagu Cloud, once it connects, or
  // with an API key, which built-in auth issues. Only the local server's
  // auth mode is known here.
  const online = !loading && !licenseError && Boolean(license.serverId);
  const canUseCloud = canConnect || (isAdmin && online);
  const canCreateKey =
    isAdmin && authMode === 'builtin' && remoteNode === 'local';

  const { data: sample, error: sampleError } = useQuery(
    '/dags',
    active
      ? {
          params: {
            query: {
              perPage: DAG_SAMPLE_SIZE,
              remoteNode,
              workspace: WorkspaceKind.all,
            },
          },
        }
      : null,
    queryOptions
  );
  const progress = sample ? progressFromSample(sample) : undefined;
  const { data: scheduledList, error: scheduledError } = useQuery(
    '/dags',
    active && progress && progress.scheduled === undefined
      ? {
          params: {
            query: {
              active: true,
              perPage: 1,
              remoteNode,
              workspace: WorkspaceKind.all,
            },
          },
        }
      : null,
    queryOptions
  );
  const { data: channelList, error: channelsError } = useQuery(
    '/notification-channels',
    active && !loading && !licenseError && community && canAddChannel
      ? { params: { query: { remoteNode } } }
      : null,
    queryOptions
  );
  const { data: remoteAccess, error: remoteAccessError } = useQuery(
    '/license/remote-access',
    active && isAdmin && online ? { params: { query: { remoteNode } } } : null,
    queryOptions
  );
  const { data: keyList, error: keysError } = useQuery(
    '/api-keys',
    active && canCreateKey ? { params: { query: { remoteNode } } } : null,
    queryOptions
  );

  const failed =
    !!sampleError || !!scheduledError || !!channelsError || !!licenseError;
  // The AI app step is left out when what it reads fails, as on a server too
  // old to report remote access, rather than hiding the whole checklist.
  const aiUnknown = !!remoteAccessError || !!keysError;
  const scheduled =
    progress?.scheduled ??
    (scheduledList ? scheduledList.pagination.totalRecords > 0 : undefined);
  const alerted = !community || !!channelList?.channels?.length;
  // An app counts once a call of its reached the server, or once there is a
  // key it may use.
  const connectedAI =
    !!remoteAccess?.lastCall ||
    !!keyList?.apiKeys.some(
      // A key without surfaces, from an older server, is accepted on all.
      (key) =>
        !key.allowedSurfaces?.length ||
        key.allowedSurfaces.includes(APIKeyAllowedSurfaces.mcp)
    );
  const ready =
    !!progress &&
    scheduled !== undefined &&
    !loading &&
    (!community || !canAddChannel || !!channelList) &&
    (!isAdmin || !online || !!remoteAccess || !!remoteAccessError) &&
    (!canCreateKey || !!keyList || !!keysError);

  let steps: GettingStartedStep[] = [];
  let next: string | undefined;
  if (active && !failed && ready) {
    steps = [
      {
        id: 'run',
        done: progress.ran,
        exampleFile: progress.hasRetryExample ? RETRY_EXAMPLE_FILE : undefined,
      },
      { id: 'create', done: progress.created },
      { id: 'schedule', done: scheduled },
    ];
    if (hasAlertStep) {
      steps.push({ id: 'alert', done: alerted, canConnect, canAddChannel });
    }
    if ((canUseCloud || canCreateKey) && !aiUnknown) {
      steps.push({ id: 'ai', done: connectedAI, canUseCloud, canCreateKey });
    }
    if (stored !== STARTED) {
      // A server already in use when first seen never needs the checklist.
      next =
        progress.ran && progress.created && scheduled ? DISMISSED : STARTED;
    } else if (steps.every((step) => step.done)) {
      next = DONE;
    }
  }

  useEffect(() => {
    if (next) setStored(next);
  }, [next, setStored]);

  if (!active || failed) return { status: 'hidden' };
  if (!ready) return { status: 'pending' };
  if (next === DISMISSED || next === DONE) return { status: 'hidden' };
  return {
    status: 'visible',
    steps,
    dismiss: () => setStored(DISMISSED),
  };
}
