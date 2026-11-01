// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus } from '@/api/v1/schema';
import { useClock } from '@/hooks/useClock';
import { useI18n } from '@/i18n/I18nProvider';
import { formatStepSpan } from '@/lib/formatStepSpan';
import { isActiveNodeStatus } from '@/lib/status-utils';

/**
 * Width of the duration slot: the longest form of formatElapsed ("59m 59s",
 * "23h 59m") in a monospaced face, so a ticking value never moves its
 * neighbours.
 */
export const ELAPSED_SLOT = 'inline-block min-w-[7ch] font-mono tabular-nums';

/** The fields of a step run that the duration reads. */
export interface StepElapsedSource {
  status: NodeStatus;
  startedAt?: string;
  finishedAt?: string;
}

/**
 * The duration of the step, ticking every second while it runs: the one text
 * that the canvas card, the run list and the facts show for one node. Null
 * for a skipped step, for one that has no start time and for no step. Only a
 * running step starts a timer. A step of 0 ms between whole seconds reads
 * "<1s" (formatStepSpan).
 */
export function useStepElapsed(
  node: StepElapsedSource | null | undefined,
  /** False while nobody sees the text: the clock stops. Default true. */
  visible = true
): string | null {
  const { ts } = useI18n();
  const active = node ? isActiveNodeStatus(node.status) : false;
  const startedAt = node?.startedAt;
  const finishedAt = node?.finishedAt;
  const skipped = node?.status === NodeStatus.Skipped;
  const belowSecond = ts('<1s');
  // Only a running step reads the shared clock; the text is the snapshot, so
  // the component renders when the text changes.
  return useClock(active && startedAt && visible ? 1000 : null, (now) =>
    !node || skipped
      ? null
      : formatStepSpan(startedAt, finishedAt, {
          live: active,
          belowSecond,
          now,
        })
  );
}
