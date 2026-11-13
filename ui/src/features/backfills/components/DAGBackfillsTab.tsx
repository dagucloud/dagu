// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Link } from 'react-router-dom';
import { LoadErrorState } from '@/components/PageStates';
import { RefreshButton } from '@/components/ui/refresh-button';
import RelativeTime from '@/components/ui/relative-time';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { useLiveBackfill } from '../hooks/useBackfill';
import { backfillPath } from '../lib/backfillLinks';
import { type Backfill, slotsDone } from '../lib/backfillState';
import { BackfillProgress } from './BackfillProgress';
import { BackfillStatusChip } from './BackfillStatusChip';
import { DAGSlotsView } from './DAGSlotsView';

// Read again each time the tab opens, however soon after the last read: a
// backfill started from it opens its own page, and the list on return must
// have it.
const READ_ON_OPEN = {
  revalidateOnFocus: false,
  revalidateIfStale: true,
  dedupingInterval: 0,
  refreshInterval: 0,
} as const;

/**
 * A DAG's slots, to backfill from, and its backfills, newest first. The list
 * is read when the tab opens and on refresh; an open backfill in it follows
 * its own live feed.
 */
export function DAGBackfillsTab({
  dagName,
  fileName,
  canExecute,
}: {
  dagName: string;
  fileName: string;
  /** Whether the user may run the DAG in its workspace. */
  canExecute: boolean;
}) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const { data, error, mutate } = useQuery(
    '/backfills',
    { params: { query: { remoteNode, dag: dagName } } },
    READ_ON_OPEN
  );

  const backfills = data ? [...data.backfills].reverse() : [];
  return (
    <div className="flex flex-col gap-6">
      <DAGSlotsView fileName={fileName} canExecute={canExecute} />
      <div className="flex flex-col gap-3">
        <div className="flex items-center justify-between">
          <span className="text-xs font-medium text-muted-foreground">
            {ts('Backfills')}
          </span>
          <RefreshButton
            onRefresh={async () => {
              await mutate();
            }}
          />
        </div>
        {!data ? (
          error ? (
            <LoadErrorState
              title={ts('Failed to load the backfills')}
              error={error}
              onRetry={() => void mutate()}
            />
          ) : (
            <p className="text-sm text-muted-foreground">{ts('Loading...')}</p>
          )
        ) : backfills.length === 0 ? (
          <div className="rounded-md border border-dashed px-3 py-4 text-sm text-muted-foreground">
            <p className="font-medium text-foreground">
              {ts('No backfills yet.')}
            </p>
            <p>
              {canExecute
                ? ts(
                    'A backfill runs the past slots of this schedule. Start one from the slots above.'
                  )
                : ts('A backfill runs the past slots of this schedule.')}
            </p>
          </div>
        ) : (
          <ul className="flex flex-col divide-y rounded-md border">
            {backfills.map((backfill) => (
              <BackfillRow
                key={backfill.id}
                backfill={backfill}
                remoteNode={remoteNode}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function BackfillRow({
  backfill: listed,
  remoteNode,
}: {
  backfill: Backfill;
  remoteNode: string;
}) {
  const backfill = useLiveBackfill(listed, remoteNode) ?? listed;
  return (
    <li>
      <Link
        to={backfillPath(backfill.id, remoteNode)}
        className="flex flex-col gap-1.5 px-3 py-2 outline-none transition-colors hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
      >
        <div className="flex items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2">
            <BackfillStatusChip status={backfill.status} size="xs" />
            <span className="font-mono text-xs">{backfill.id}</span>
          </div>
          <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
            {slotsDone(backfill)}/{backfill.slotCount}
          </span>
        </div>
        <BackfillProgress backfill={backfill} compact />
        <span className="text-xs text-muted-foreground">
          <RelativeTime timestamp={backfill.submittedAt} />
          {backfill.actor && ` · ${backfill.actor}`}
        </span>
      </Link>
    </li>
  );
}
