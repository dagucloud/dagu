// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ComponentProps, ReactNode } from 'react';
import {
  ALL_INLINE_FACTS,
  RunMetaLine,
  type InlineFact,
} from '@/features/dag-runs/components/common/RunMetaLine';

type RunFields = ComponentProps<typeof RunMetaLine>['dagRun'];

export type { InlineFact };

export interface RunFactsProps {
  dagRun: RunFields;
  /**
   * `inline` is the row of the bar. `stacked` is the tooltip of the status
   * icon: it lists only what the row does not show. Both come from the one
   * renderer of the run facts.
   */
  layout: 'inline' | 'stacked';
  /**
   * The facts that the bar has room for. The default is every fact. See
   * factsShownAt in useFactTiers.
   */
  shown?: ReadonlySet<InlineFact>;
  /**
   * The run failed: another control of the bar names the failed step in the
   * place of the trigger, so the row leaves the trigger out.
   */
  failed?: boolean;
  /** The inline row: what stands in front of the run ID in place of the #. */
  idMark?: ReactNode;
}

/** The facts that the row shows: the facts of the bar, without the trigger of a failed run. */
export function visibleFacts(
  shown: ReadonlySet<InlineFact> = ALL_INLINE_FACTS,
  failed = false
): ReadonlySet<InlineFact> {
  if (!failed || !shown.has('trigger')) return shown;
  const next = new Set(shown);
  next.delete('trigger');
  return next;
}

/**
 * The run as the tooltip reads it. The local worker is the default: it
 * says nothing, so it gives no reason for a tooltip.
 */
function tooltipRun(dagRun: RunFields): RunFields {
  return dagRun.workerId === 'local'
    ? { ...dagRun, workerId: undefined }
    : dagRun;
}

/** Whether the run has the fact at all (a manual run has no trigger to show twice, a queued run no duration). */
function hasFact(dagRun: RunFields, fact: InlineFact): boolean {
  const started = !!dagRun.startedAt && dagRun.startedAt !== '-';
  switch (fact) {
    case 'trigger':
      return !!dagRun.triggerType;
    case 'started':
    case 'duration':
      return started;
    case 'id':
      return true;
  }
}

/**
 * Whether the tooltip of the status icon has anything to say beyond the row:
 * a fact that the row leaves out, or one that the row never shows (the
 * worker, the profile, the queue wait, the parameters).
 */
export function hasHiddenFacts(
  dagRun: RunFields,
  visible: ReadonlySet<InlineFact>
): boolean {
  const fields = tooltipRun(dagRun);
  const queued = fields.queuedAt && fields.queuedAt !== '-';
  const waited =
    queued && fields.startedAt && fields.startedAt !== '-'
      ? Date.parse(fields.startedAt) - Date.parse(fields.queuedAt!) >= 1000
      : false;
  if (fields.workerId || fields.profileName || fields.params || waited) {
    return true;
  }
  return [...ALL_INLINE_FACTS].some(
    (fact) => !visible.has(fact) && hasFact(fields, fact)
  );
}

/**
 * The facts of a run in the workflow bar: how it started, when, for how long
 * and its ID. The same renderer gives the row that shows in the bar when it
 * has room and the tooltip of the status icon, which adds only the rest.
 */
export function RunFacts({
  dagRun,
  layout,
  shown = ALL_INLINE_FACTS,
  failed = false,
  idMark,
}: RunFactsProps) {
  const visible = visibleFacts(shown, failed);
  return layout === 'inline' ? (
    <RunMetaLine
      dagRun={dagRun}
      inline
      shown={visible}
      omitTrigger={failed}
      idMark={idMark}
    />
  ) : (
    <RunMetaLine
      dagRun={tooltipRun(dagRun)}
      omit={visible}
      startedAbsolute
      paramsOpen
    />
  );
}
