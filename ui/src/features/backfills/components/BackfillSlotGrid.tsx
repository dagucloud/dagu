// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import dayjs from '@/lib/dayjs';
import {
  type BackfillSlot,
  groupSlots,
  SLOT_STATE_LOOK,
} from '../lib/backfillState';

type Props = {
  dagName: string;
  slots: readonly BackfillSlot[];
};

const slotKey = (slot: BackfillSlot, unit: 'day' | 'month') =>
  dayjs(slot.slot).format(unit === 'day' ? 'YYYY-MM-DD' : 'YYYY-MM');

/**
 * The slots of a backfill as cells, a row per day or per month. Each cell
 * shows its state by icon and color, names its time and state, and opens
 * the slot's run when it has one.
 */
export function BackfillSlotGrid({ dagName, slots }: Props) {
  const groups = useMemo(() => groupSlots(slots, slotKey), [slots]);
  return (
    <div className="flex flex-col gap-1.5">
      {groups.map((group) => (
        <div key={group.key} className="flex items-start gap-3">
          <span className="w-20 shrink-0 pt-0.5 font-mono text-xs tabular-nums text-muted-foreground">
            {group.key}
          </span>
          <div className="flex min-w-0 flex-wrap gap-1">
            {group.slots.map((slot) => (
              <SlotCell key={slot.slot} dagName={dagName} slot={slot} />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

function SlotCell({ dagName, slot }: { dagName: string; slot: BackfillSlot }) {
  const { ts } = useI18n();
  const look = SLOT_STATE_LOOK[slot.state];
  const Icon = look.icon;
  const label = `${ts('Slot {time}', {
    time: formatAbsoluteTime(slot.slot, { seconds: false }) ?? slot.slot,
  })} · ${ts(look.label)}`;
  const cell = (
    <span
      className="flex h-5 w-5 items-center justify-center rounded"
      style={{
        backgroundColor: `color-mix(in srgb, ${look.color} 16%, transparent)`,
      }}
    >
      <Icon
        className="h-3.5 w-3.5"
        style={{ color: look.color }}
        aria-hidden="true"
      />
    </span>
  );
  if (!slot.runId) {
    return (
      <span role="img" title={label} aria-label={label}>
        {cell}
      </span>
    );
  }
  return (
    <Link
      to={`/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(slot.runId)}`}
      title={label}
      aria-label={label}
      className="rounded outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      {cell}
    </Link>
  );
}
