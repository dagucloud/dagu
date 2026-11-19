// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { History, X } from 'lucide-react';
import { Link } from 'react-router-dom';
import { BackfillStatus, type components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useI18n } from '@/i18n/I18nProvider';
import { useDismissedBackfills } from '../hooks/useBackfillNotice';
import { backfillPath } from '../lib/backfillLinks';

type Props = {
  backfill: components['schemas']['DAGBackfillSummary'];
};

/**
 * The backfill a DAG list row mentions: an open one with its progress, or
 * one that ended failed, in red until its failed slots succeed or someone
 * dismisses it. It opens the backfill's page.
 */
export function DAGBackfillChip({ backfill }: Props) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const { dismissed, dismiss } = useDismissedBackfills();
  const failed = backfill.status === BackfillStatus.failed;
  if (failed && dismissed.includes(backfill.id)) {
    return null;
  }
  const label = failed
    ? ts('Backfill failed: {failed} slots', { failed: backfill.failed })
    : backfill.failed > 0
      ? ts('Backfill {done}/{total}, {failed} failed', {
          done: backfill.done,
          total: backfill.slots,
          failed: backfill.failed,
        })
      : ts('Backfill {done}/{total}', {
          done: backfill.done,
          total: backfill.slots,
        });
  const color = failed ? 'var(--status-error)' : 'var(--status-running)';
  return (
    <span
      className="inline-flex h-[18px] max-w-full items-center gap-1 rounded px-1.5 text-[11px]"
      style={{
        color,
        backgroundColor: `color-mix(in srgb, ${color} 12%, transparent)`,
      }}
    >
      <Link
        to={backfillPath(backfill.id, remoteNode)}
        className="inline-flex min-w-0 items-center gap-1 truncate hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        onClick={(event) => event.stopPropagation()}
      >
        <History className="h-3 w-3 shrink-0" aria-hidden="true" />
        <span className="truncate">{label}</span>
      </Link>
      {failed && (
        <button
          type="button"
          aria-label={ts('Dismiss')}
          className="shrink-0 rounded opacity-70 hover:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onClick={(event) => {
            event.stopPropagation();
            dismiss(backfill.id);
          }}
        >
          <X className="h-3 w-3" aria-hidden="true" />
        </button>
      )}
    </span>
  );
}
