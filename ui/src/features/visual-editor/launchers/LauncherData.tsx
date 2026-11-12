// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useMemo } from 'react';
import { useCanManageWebhooks } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useWorkflowWebhook } from '@/hooks/useWorkflowWebhook';
import { launcherStates, type LauncherState } from './model';

export interface LauncherData {
  states: LauncherState[];
  /** False when the instance has no webhooks for this user, or the node is remote. */
  webhooksAvailable: boolean;
  reloadWebhook(): void;
}

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
  const { webhook, reload } = useWorkflowWebhook(fileName, webhooksAvailable);
  // The API gives one webhook for each workflow.
  const webhookCount = webhook === undefined ? null : webhook === null ? 0 : 1;
  const states = useMemo(
    () => launcherStates({ scheduleCount, webhookCount, webhooksAvailable }),
    [scheduleCount, webhookCount, webhooksAvailable]
  );
  useEffect(() => {
    onData({ states, webhooksAvailable, reloadWebhook: reload });
  }, [onData, states, webhooksAvailable, reload]);
  return null;
}
