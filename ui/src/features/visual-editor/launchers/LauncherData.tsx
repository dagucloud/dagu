// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useMemo } from 'react';
import { useCanManageWebhooks } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { isNotFoundQueryError } from '@/lib/queryErrors';
import { shouldRetryQueryError } from '@/lib/requestTimeout';
import { launcherStates, type LauncherState } from './model';

export interface LauncherData {
  states: LauncherState[];
  /** False when the instance has no webhooks for this user, or the node is remote. */
  webhooksAvailable: boolean;
  reloadWebhook(): void;
}

// A missing webhook is an answer, not a failure to retry.
const QUERY_CONFIG = {
  shouldRetryOnError: (error: unknown) =>
    !isNotFoundQueryError(error) && shouldRetryQueryError(error),
};

/**
 * Reads the webhook of the workflow and gives the launcher states to its
 * parent. It renders nothing. Webhooks need the builtin auth service, a
 * developer role and the local node: without them the webhook launcher is
 * absent, as the Webhook settings were.
 */
export function LauncherDataProbe({
  fileName,
  scheduleCount,
  onData,
}: {
  fileName: string;
  scheduleCount: number;
  onData(data: LauncherData): void;
}): null {
  const remoteNode = useRemoteNode();
  const webhooksAvailable = useCanManageWebhooks() && remoteNode === 'local';
  const init = useMemo(
    () =>
      whenEnabled(webhooksAvailable, {
        params: { path: { fileName }, query: { remoteNode } },
      }),
    [webhooksAvailable, fileName, remoteNode]
  );
  const { data, error, mutate } = useQuery(
    '/dags/{fileName}/webhook',
    init,
    QUERY_CONFIG
  );
  // The API gives one webhook for each workflow. SWR keeps the last data
  // next to a later error: a 404 after a delete wins.
  const webhookCount = !webhooksAvailable
    ? null
    : isNotFoundQueryError(error)
      ? 0
      : data
        ? 1
        : null;
  const states = useMemo(
    () => launcherStates({ scheduleCount, webhookCount, webhooksAvailable }),
    [scheduleCount, webhookCount, webhooksAvailable]
  );
  const reload = useMemo(() => () => void mutate?.(), [mutate]);
  useEffect(() => {
    onData({ states, webhooksAvailable, reloadWebhook: reload });
  }, [onData, states, webhooksAvailable, reload]);
  return null;
}
