// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CheckCircle2, CircleDashed, Loader2, XCircle } from 'lucide-react';
import { DAGSlotStatus, type components } from '@/api/v1/schema';
import type { SlotStateLook } from './backfillState';

export type DAGSlot = components['schemas']['DAGSlot'];

/** How the state of a DAG's slot reads, by icon and color as well as name. */
export const DAG_SLOT_LOOK: Record<DAGSlotStatus, SlotStateLook> = {
  [DAGSlotStatus.missing]: {
    label: 'Missing',
    color: 'var(--status-neutral)',
    icon: CircleDashed,
  },
  [DAGSlotStatus.succeeded]: {
    label: 'Succeeded',
    color: 'var(--status-success)',
    icon: CheckCircle2,
  },
  [DAGSlotStatus.failed]: {
    label: 'Failed',
    color: 'var(--status-error)',
    icon: XCircle,
  },
  [DAGSlotStatus.active]: {
    label: 'Active',
    color: 'var(--status-running)',
    icon: Loader2,
  },
};

/** The states in the order of the legend. */
export const DAG_SLOT_STATES: readonly DAGSlotStatus[] = [
  DAGSlotStatus.missing,
  DAGSlotStatus.failed,
  DAGSlotStatus.active,
  DAGSlotStatus.succeeded,
];

/**
 * The selection after a click on the slot at `index`: a plain click adds or
 * removes the slot, and a click with Shift adds every slot from the last one
 * clicked to it.
 */
export function selectSlots(
  slots: readonly DAGSlot[],
  selected: ReadonlySet<string>,
  index: number,
  anchor: number | null,
  extend: boolean
): Set<string> {
  const next = new Set(selected);
  const slot = slots[index];
  if (!slot) return next;
  if (extend && anchor !== null && slots[anchor]) {
    const [from, to] = anchor < index ? [anchor, index] : [index, anchor];
    for (const s of slots.slice(from, to + 1)) next.add(s.slot);
    return next;
  }
  if (next.has(slot.slot)) next.delete(slot.slot);
  else next.add(slot.slot);
  return next;
}

/** The slots in any of the states, as a selection. */
export function slotsInStates(
  slots: readonly DAGSlot[],
  states: readonly DAGSlotStatus[]
): Set<string> {
  return new Set(
    slots.filter((slot) => states.includes(slot.status)).map((s) => s.slot)
  );
}
