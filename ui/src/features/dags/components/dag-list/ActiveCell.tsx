// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Webhook } from 'lucide-react';
import { useCallback, useMemo, useState } from 'react';
import type { components } from '@/api/v1/schema';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { useErrorModal } from '@/components/ui/error-modal';
import { Switch } from '@/components/ui/switch';
import { useCanManageWebhooks } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { isServiceDAG } from '@/lib/dagSchedule';
import LiveSwitch from '../common/LiveSwitch';
import { ScheduleLine, WebhookIndicator } from './ScheduleSummary';

type DAGFile = components['schemas']['DAGFile'];

/**
 * The schedule of a workflow with its switch (the suspension of the workflow)
 * and the next run; a service DAG shows its restart policy instead. A dash
 * without a schedule or a restart policy.
 */
export function ScheduleCell({
  dag,
  refresh,
}: {
  dag: DAGFile;
  refresh?: () => void;
}) {
  const hasSchedule = (dag.dag.schedule ?? []).length > 0;
  if (!hasSchedule && !isServiceDAG(dag.dag)) {
    return (
      <span
        data-testid="schedule-cell"
        className="text-xs text-muted-foreground"
      >
        -
      </span>
    );
  }
  return (
    <span
      data-testid="schedule-cell"
      onClick={(event) => event.stopPropagation()}
      className="flex min-w-0 items-center gap-1 text-xs"
    >
      <LiveSwitch dag={dag} refresh={refresh} />
      <ScheduleLine dag={dag} emptyLabel={false} indicator={false} />
    </span>
  );
}

/** The webhook of a workflow with its switch; a dash without a webhook. */
export function WebhookCell({ dag }: { dag: DAGFile }) {
  return (
    <div data-testid="webhook-cell" className="flex items-center text-xs">
      <WebhookSwitch dag={dag} />
    </div>
  );
}

/**
 * The switch of the webhook of a workflow. It shows only with a webhook and
 * the right to manage webhooks on the local server; a reader sees the marker
 * of the webhook without a switch.
 */
function WebhookSwitch({ dag }: { dag: DAGFile }) {
  const { ts } = useI18n();
  const config = useConfig();
  const client = useClient();
  const { showError } = useErrorModal();
  const canManage = useCanManageWebhooks();
  const remoteNode = useRemoteNode();
  // The read gate is local: a remote node can refuse it with a 401, which
  // signs the user out of the local server.
  const enabled = canManage && remoteNode === 'local';
  const init = useMemo(
    () => whenEnabled(enabled, { params: { query: { remoteNode } } }),
    [enabled, remoteNode]
  );
  const { data, mutate } = useQuery('/webhooks', init);
  const [pending, setPending] = useState<boolean | null>(null);
  const webhook = Array.isArray(data?.webhooks)
    ? data.webhooks.find((item) => item.dagName === dag.fileName)
    : undefined;

  const confirm = useCallback(async () => {
    const next = pending;
    setPending(null);
    if (next === null) return;
    const { error } = await client.POST('/dags/{fileName}/webhook/toggle', {
      params: { path: { fileName: dag.fileName }, query: { remoteNode } },
      body: { enabled: next },
    });
    if (error) {
      showError(
        error.message || ts('Could not update the webhook'),
        ts('Please try again or check the server connection.')
      );
      return;
    }
    await mutate();
  }, [client, dag.fileName, mutate, pending, remoteNode, showError, ts]);

  if (!enabled || !webhook) {
    return webhook ? (
      <WebhookIndicator fileName={dag.fileName} />
    ) : (
      <span className="text-muted-foreground">-</span>
    );
  }
  const name = dag.dag.name;
  const canToggle = config.permissions.runDags;
  return (
    <span
      onClick={(event) => event.stopPropagation()}
      className="flex shrink-0 items-center gap-1"
    >
      <Switch
        data-testid="webhook-switch"
        checked={webhook.enabled}
        disabled={!canToggle}
        onCheckedChange={canToggle ? (value) => setPending(value) : undefined}
        aria-label={ts('Webhook of {name}', { name })}
      />
      <Webhook aria-hidden="true" className="size-3.5 text-muted-foreground" />
      <I18nProps>
        <ConfirmModal
          title={pending ? 'Enable webhook' : 'Disable webhook'}
          buttonText={pending ? 'Enable' : 'Disable'}
          visible={pending !== null}
          dismissModal={() => setPending(null)}
          onSubmit={() => void confirm()}
        >
          <p>
            {ts('Are you sure you want to {action} the webhook for "{name}"?', {
              action: ts(pending ? 'enable' : 'disable'),
              name: dag.fileName,
            })}
          </p>
        </ConfirmModal>
      </I18nProps>
    </span>
  );
}

/**
 * The schedules of a group with one switch: on when every scheduled workflow
 * of the group is live. A change asks for the confirmation, then applies to all
 * of them at once. A dash when no workflow of the group has a schedule.
 */
export function GroupScheduleCell({
  name,
  members,
  refresh,
}: {
  name: string;
  members: DAGFile[];
  refresh?: () => void;
}) {
  const { ts } = useI18n();
  const client = useClient();
  const config = useConfig();
  const remoteNode = useRemoteNode();
  const { showError } = useErrorModal();
  const scheduled = members.filter(
    (dag) => (dag.dag.schedule ?? []).length > 0
  );
  const [pending, setPending] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const live = scheduled.filter((dag) => !dag.suspended).length;

  const confirm = useCallback(async () => {
    const next = pending;
    setPending(null);
    if (next === null) return;
    setBusy(true);
    try {
      const results = await Promise.all(
        scheduled
          .filter((dag) => !!dag.suspended === next)
          .map((dag) =>
            client.POST('/dags/{fileName}/suspend', {
              params: {
                path: { fileName: dag.fileName },
                query: { remoteNode },
              },
              body: { suspend: !next },
            })
          )
      );
      const failure = results.find((result) => result.error);
      if (failure?.error) {
        showError(
          failure.error.message || ts('Could not update the schedules'),
          ts('Please try again or check the server connection.')
        );
      }
    } finally {
      setBusy(false);
      refresh?.();
    }
  }, [client, pending, refresh, remoteNode, scheduled, showError, ts]);

  if (scheduled.length === 0) {
    return <span className="text-xs text-muted-foreground">-</span>;
  }
  const canToggle = config.permissions.runDags;
  return (
    <span
      data-testid="group-schedule-cell"
      onClick={(event) => event.stopPropagation()}
      className="flex min-w-0 items-center gap-1.5 text-xs"
    >
      <Switch
        checked={live === scheduled.length}
        disabled={!canToggle || busy}
        onCheckedChange={canToggle ? (value) => setPending(value) : undefined}
        aria-label={ts('Schedules of group {name}', { name })}
      />
      <span className="text-muted-foreground">
        {ts('{live} of {total} live', { live, total: scheduled.length })}
      </span>
      <I18nProps>
        <ConfirmModal
          title={pending ? 'Enable schedule' : 'Disable schedule'}
          buttonText={pending ? 'Enable' : 'Disable'}
          visible={pending !== null}
          dismissModal={() => setPending(null)}
          onSubmit={() => void confirm()}
        >
          <p>
            {ts(
              'Are you sure you want to {action} the schedules of the {count} workflows of "{name}"?',
              {
                action: ts(pending ? 'enable' : 'disable'),
                count: scheduled.length,
                name,
              }
            )}
          </p>
        </ConfirmModal>
      </I18nProps>
    </span>
  );
}

/**
 * The webhooks of a group with one switch: on when every webhook of the group
 * is enabled. It shows only with the right to manage webhooks on the local
 * server, and when a workflow of the group has a webhook.
 */
export function GroupWebhookCell({
  name,
  members,
}: {
  name: string;
  members: DAGFile[];
}) {
  const { ts } = useI18n();
  const config = useConfig();
  const client = useClient();
  const { showError } = useErrorModal();
  const canManage = useCanManageWebhooks();
  const remoteNode = useRemoteNode();
  const enabled = canManage && remoteNode === 'local';
  const init = useMemo(
    () => whenEnabled(enabled, { params: { query: { remoteNode } } }),
    [enabled, remoteNode]
  );
  const { data, mutate } = useQuery('/webhooks', init);
  const [pending, setPending] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const names = new Set(members.map((dag) => dag.fileName));
  const webhooks = Array.isArray(data?.webhooks)
    ? data.webhooks.filter((item) => names.has(item.dagName))
    : [];

  const confirm = useCallback(async () => {
    const next = pending;
    setPending(null);
    if (next === null) return;
    setBusy(true);
    try {
      const results = await Promise.all(
        webhooks
          .filter((webhook) => webhook.enabled !== next)
          .map((webhook) =>
            client.POST('/dags/{fileName}/webhook/toggle', {
              params: {
                path: { fileName: webhook.dagName },
                query: { remoteNode },
              },
              body: { enabled: next },
            })
          )
      );
      const failure = results.find((result) => result.error);
      if (failure?.error) {
        showError(
          failure.error.message || ts('Could not update the webhook'),
          ts('Please try again or check the server connection.')
        );
      }
    } finally {
      setBusy(false);
      await mutate();
    }
  }, [client, mutate, pending, remoteNode, showError, ts, webhooks]);

  if (!enabled || webhooks.length === 0) {
    return <span className="text-xs text-muted-foreground">-</span>;
  }
  const canToggle = config.permissions.runDags;
  const on = webhooks.filter((webhook) => webhook.enabled).length;
  return (
    <span
      data-testid="group-webhook-cell"
      onClick={(event) => event.stopPropagation()}
      className="flex min-w-0 items-center gap-1.5 text-xs"
    >
      <Switch
        checked={on === webhooks.length}
        disabled={!canToggle || busy}
        onCheckedChange={canToggle ? (value) => setPending(value) : undefined}
        aria-label={ts('Webhooks of group {name}', { name })}
      />
      <Webhook aria-hidden="true" className="size-3.5 text-muted-foreground" />
      <I18nProps>
        <ConfirmModal
          title={pending ? 'Enable webhook' : 'Disable webhook'}
          buttonText={pending ? 'Enable' : 'Disable'}
          visible={pending !== null}
          dismissModal={() => setPending(null)}
          onSubmit={() => void confirm()}
        >
          <p>
            {ts(
              'Are you sure you want to {action} the webhooks of the {count} workflows of "{name}"?',
              {
                action: ts(pending ? 'enable' : 'disable'),
                count: webhooks.length,
                name,
              }
            )}
          </p>
        </ConfirmModal>
      </I18nProps>
    </span>
  );
}
