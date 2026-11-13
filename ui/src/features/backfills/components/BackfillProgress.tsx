// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { BackfillSlotState } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import {
  type Backfill,
  SLOT_STATE_LOOK,
  SLOT_STATES,
  slotsIn,
} from '../lib/backfillState';

type Props = {
  backfill: Backfill;
  /** The state the slot list shows; null shows every slot. */
  selected?: BackfillSlotState | null;
  /** Picks a state to list, or null for every slot. Without it the legend only reads. */
  onSelect?: (state: BackfillSlotState | null) => void;
  /** A bar without a legend, for a list row. */
  compact?: boolean;
};

/**
 * The backfill's slots by state: a bar whose parts follow the share of each
 * state, and a legend with the count, label, and icon of each state present.
 */
export function BackfillProgress({
  backfill,
  selected = null,
  onSelect,
  compact = false,
}: Props) {
  const { ts } = useI18n();
  const total = Math.max(backfill.slotCount, 1);
  const present = SLOT_STATES.filter((state) => slotsIn(backfill, state) > 0);
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div
        className={cn(
          'flex w-full overflow-hidden rounded-full bg-muted',
          compact ? 'h-1.5' : 'h-2'
        )}
        role="img"
        aria-label={present
          .map(
            (state) =>
              `${ts(SLOT_STATE_LOOK[state].label)} ${slotsIn(backfill, state)}`
          )
          .join(', ')}
      >
        {present.map((state) => (
          <div
            key={state}
            className="h-full"
            style={{
              width: `${(slotsIn(backfill, state) / total) * 100}%`,
              backgroundColor: SLOT_STATE_LOOK[state].color,
            }}
          />
        ))}
      </div>
      {!compact && (
        <ul
          className="flex flex-wrap gap-x-1 gap-y-1 text-xs"
          aria-label={ts('Slot states')}
        >
          {present.map((state) => {
            const look = SLOT_STATE_LOOK[state];
            const Icon = look.icon;
            const active = selected === state;
            const content = (
              <>
                <Icon
                  className="h-3.5 w-3.5 shrink-0"
                  style={{ color: look.color }}
                  aria-hidden="true"
                />
                <span className="tabular-nums text-foreground">
                  {slotsIn(backfill, state)}
                </span>
                <span className="text-muted-foreground">{ts(look.label)}</span>
              </>
            );
            return (
              <li key={state}>
                {onSelect ? (
                  <button
                    type="button"
                    aria-pressed={active}
                    onClick={() => onSelect(active ? null : state)}
                    className={cn(
                      'flex items-center gap-1 rounded-md px-1.5 py-0.5 outline-none transition-colors hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring',
                      active && 'bg-muted ring-1 ring-border'
                    )}
                  >
                    {content}
                  </button>
                ) : (
                  <span className="flex items-center gap-1 px-1.5 py-0.5">
                    {content}
                  </span>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
