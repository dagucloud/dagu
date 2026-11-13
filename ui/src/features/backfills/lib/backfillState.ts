// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Ban,
  CheckCircle2,
  CircleDashed,
  Clock,
  Loader2,
  type LucideIcon,
  MinusCircle,
  PauseCircle,
  XCircle,
} from 'lucide-react';
import {
  BackfillOnFailure,
  BackfillRerun,
  BackfillSlotState,
  BackfillStatus,
  type components,
} from '@/api/v1/schema';

export type Backfill = components['schemas']['Backfill'];
export type BackfillSlot = components['schemas']['BackfillSlot'];

/**
 * How a slot state reads: a static message, a status color, and an icon, so
 * that a state never reads by color alone.
 */
export interface SlotStateLook {
  label: string;
  color: string;
  icon: LucideIcon;
}

export const SLOT_STATE_LOOK: Record<BackfillSlotState, SlotStateLook> = {
  [BackfillSlotState.succeeded]: {
    label: 'Succeeded',
    color: 'var(--status-success)',
    icon: CheckCircle2,
  },
  [BackfillSlotState.failed]: {
    label: 'Failed',
    color: 'var(--status-error)',
    icon: XCircle,
  },
  [BackfillSlotState.running]: {
    label: 'Running',
    color: 'var(--status-running)',
    icon: Loader2,
  },
  [BackfillSlotState.waiting]: {
    label: 'Waiting',
    color: 'var(--status-warning)',
    icon: PauseCircle,
  },
  [BackfillSlotState.queued]: {
    label: 'Queued',
    color: 'var(--status-neutral)',
    icon: Clock,
  },
  [BackfillSlotState.pending]: {
    label: 'Pending',
    color: 'var(--status-neutral)',
    icon: CircleDashed,
  },
  [BackfillSlotState.skipped]: {
    label: 'Skipped',
    color: 'var(--status-neutral)',
    icon: MinusCircle,
  },
  [BackfillSlotState.not_run]: {
    label: 'Not run',
    color: 'var(--status-aborted)',
    icon: Ban,
  },
  [BackfillSlotState.cancelled]: {
    label: 'Cancelled',
    color: 'var(--status-aborted)',
    icon: Ban,
  },
};

/** The slot states in the order of the progress bar and the filter. */
export const SLOT_STATES: readonly BackfillSlotState[] = [
  BackfillSlotState.succeeded,
  BackfillSlotState.failed,
  BackfillSlotState.running,
  BackfillSlotState.waiting,
  BackfillSlotState.queued,
  BackfillSlotState.pending,
  BackfillSlotState.skipped,
  BackfillSlotState.not_run,
  BackfillSlotState.cancelled,
];

/** The plain words of each rerun value. */
export const RERUN_LABEL: Record<BackfillRerun, string> = {
  [BackfillRerun.none]: 'Fill gaps',
  [BackfillRerun.failed]: 'Fill gaps and redo failed',
  [BackfillRerun.all]: 'Redo everything',
};

/** The plain words of each failure behavior. */
export const ON_FAILURE_LABEL: Record<BackfillOnFailure, string> = {
  [BackfillOnFailure.stop]: 'Stops after a slot fails',
  [BackfillOnFailure.continue]: 'Goes on after a slot fails',
};

/** The number of the backfill's slots in a state. */
export function slotsIn(backfill: Backfill, state: BackfillSlotState): number {
  return backfill.counts[state] ?? 0;
}

/** Reports whether the backfill has not ended. */
export function isOpenBackfill(backfill: Pick<Backfill, 'status'>): boolean {
  return (
    backfill.status === BackfillStatus.running ||
    backfill.status === BackfillStatus.waiting_for_scheduler
  );
}

/** The slots whose runs are queued, running, or waiting for a person. */
export function slotsInProgress(backfill: Backfill): number {
  return (
    slotsIn(backfill, BackfillSlotState.queued) +
    slotsIn(backfill, BackfillSlotState.running) +
    slotsIn(backfill, BackfillSlotState.waiting)
  );
}

/** The slots that have run their course: succeeded, failed, or skipped. */
export function slotsDone(backfill: Backfill): number {
  return (
    slotsIn(backfill, BackfillSlotState.succeeded) +
    slotsIn(backfill, BackfillSlotState.failed) +
    slotsIn(backfill, BackfillSlotState.skipped)
  );
}

export type SummaryTone = 'running' | 'success' | 'error' | 'warning';

/** One sentence that gives where a backfill stands: a static message and its values. */
export interface BackfillSummary {
  message: string;
  values: Record<string, number>;
  tone: SummaryTone;
}

/** The sentence of a backfill's state, as its page and notices show it. */
export function summarizeBackfill(backfill: Backfill): BackfillSummary {
  const total = backfill.slotCount;
  const done = slotsDone(backfill);
  const failed = slotsIn(backfill, BackfillSlotState.failed);
  switch (backfill.status) {
    case BackfillStatus.waiting_for_scheduler:
      return {
        message: 'Waiting for a scheduler: {done} of {total} slots done',
        values: { done, total },
        tone: 'warning',
      };
    case BackfillStatus.running: {
      if (
        slotsInProgress(backfill) === 0 &&
        slotsIn(backfill, BackfillSlotState.pending) === 0
      ) {
        return {
          message: 'Finishing: all {total} slots are done',
          values: { total },
          tone: 'running',
        };
      }
      return failed > 0
        ? {
            message: 'Running: {done} of {total} slots done, {failed} failed',
            values: { done, total, failed },
            tone: 'running',
          }
        : {
            message: 'Running: {done} of {total} slots done',
            values: { done, total },
            tone: 'running',
          };
    }
    case BackfillStatus.succeeded: {
      const skipped = slotsIn(backfill, BackfillSlotState.skipped);
      return skipped > 0
        ? {
            message: 'Succeeded: {ran} slots ran, {skipped} skipped',
            values: { ran: total - skipped, skipped },
            tone: 'success',
          }
        : {
            message: 'Succeeded: all {total} slots',
            values: { total },
            tone: 'success',
          };
    }
    case BackfillStatus.failed: {
      const notRun = slotsIn(backfill, BackfillSlotState.not_run);
      return notRun > 0
        ? {
            message: 'Stopped: {failed} failed, {notRun} not run',
            values: { failed, notRun },
            tone: 'error',
          }
        : {
            message: 'Failed: {failed} of {total} slots failed',
            values: { failed, total },
            tone: 'error',
          };
    }
    case BackfillStatus.cancelled:
      return {
        message: 'Cancelled: {cancelled} slots were not run',
        values: { cancelled: slotsIn(backfill, BackfillSlotState.cancelled) },
        tone: 'warning',
      };
  }
}

/** Reports whether resuming the backfill could run a slot again. */
export function canResume(backfill: Backfill): boolean {
  return backfill.status !== BackfillStatus.succeeded;
}

/** A row of a slot grid: the slots of one day, or of one month for slower cadences. */
export interface SlotGroup<T extends { slot: string } = BackfillSlot> {
  key: string;
  slots: T[];
}

/**
 * Groups slots, oldest first, the way their cadence reads: by day when a day
 * holds more than one slot, else by month. `keyOf` gives the day or month
 * of a slot in the zone the page shows times in.
 */
export function groupSlots<T extends { slot: string }>(
  slots: readonly T[],
  keyOf: (slot: T, unit: 'day' | 'month') => string
): SlotGroup<T>[] {
  const days = new Set(slots.map((slot) => keyOf(slot, 'day')));
  const unit = days.size < slots.length ? 'day' : 'month';
  const groups: SlotGroup<T>[] = [];
  for (const slot of slots) {
    const key = keyOf(slot, unit);
    const last = groups[groups.length - 1];
    if (last?.key === key) {
      last.slots.push(slot);
    } else {
      groups.push({ key, slots: [slot] });
    }
  }
  return groups;
}
