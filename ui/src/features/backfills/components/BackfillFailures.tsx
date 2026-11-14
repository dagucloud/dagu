// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Link } from 'react-router-dom';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { runPath } from '../lib/backfillLinks';
import type { Backfill } from '../lib/backfillState';

/**
 * A backfill's failed slots grouped by how their runs failed: the slot
 * count, the step, and the error, the largest group first. Each group links
 * to the run of its earliest slot, whose page holds the log.
 */
export function BackfillFailures({
  backfill,
  remoteNode,
}: {
  backfill: Backfill;
  /** The node of the backfill, which the links to runs keep. */
  remoteNode: string;
}) {
  const { ts } = useI18n();
  const failures = backfill.failures ?? [];
  if (failures.length === 0) return null;
  return (
    <section
      aria-label={ts('Failures')}
      className="card-obsidian space-y-2 px-4 py-3"
    >
      <h2 className="text-xs font-medium text-muted-foreground">
        {ts('Failures')}
      </h2>
      <ul className="space-y-1.5 text-sm">
        {failures.map((failure) => (
          <li
            key={`${failure.step ?? ''}\n${failure.error ?? ''}`}
            className="flex flex-wrap items-baseline gap-x-2"
          >
            <span className="font-medium tabular-nums">
              {ts(failure.slots === 1 ? '{count} slot' : '{count} slots', {
                count: failure.slots,
              })}
            </span>
            {failure.step && (
              <code className="min-w-0 max-w-full whitespace-normal break-words font-mono text-xs">
                {failure.step}
              </code>
            )}
            <span className="min-w-0 break-words text-muted-foreground">
              {failure.error || ts('No error recorded')}
            </span>
            <Link
              to={runPath(backfill.dag, failure.runId, remoteNode)}
              className="text-xs underline-offset-2 hover:underline"
            >
              {ts('Run of {time}', {
                time:
                  formatAbsoluteTime(failure.slot, { seconds: false }) ??
                  failure.slot,
              })}
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}
