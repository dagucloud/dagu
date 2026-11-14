// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { History } from 'lucide-react';
import { useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { DAGSlotStatus } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { RefreshButton } from '@/components/ui/refresh-button';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { triggerTypeLabels } from '@/features/dags/components/common/TriggerTypeIndicator';
import {
  dateSummary,
  dateText,
} from '@/features/dags/components/dag-execution/SlotDatesPreview';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import dayjs from '@/lib/dayjs';
import { cn } from '@/lib/utils';
import { backfillPath, workflowPath } from '../lib/backfillLinks';
import { groupSlots } from '../lib/backfillState';
import {
  DAG_SLOT_LOOK,
  DAG_SLOT_STATES,
  type DAGSlot,
  selectSlots,
  slotsInStates,
} from '../lib/dagSlots';
import {
  type BackfillTarget,
  StartBackfillDialog,
} from './StartBackfillDialog';

/** The ranges the view offers, back from now. */
const RANGES = [
  { id: '1d', label: 'Last 24 hours', days: 1 },
  { id: '7d', label: 'Last 7 days', days: 7 },
  { id: '30d', label: 'Last 30 days', days: 30 },
  { id: '90d', label: 'Last 90 days', days: 90 },
] as const;
type RangeId = (typeof RANGES)[number]['id'];

/** The most cells drawn at once; a longer range shows its newest slots. */
const SHOWN_SLOTS = 2000;

const READ_ONCE = {
  revalidateOnFocus: false,
  revalidateIfStale: false,
  refreshInterval: 0,
} as const;

// labelDate returns the calendar date a slot loads: the day its first date
// names, when that date is one.
function labelDate(slot: DAGSlot): string | undefined {
  const first = slot.dates?.[0];
  const value = first?.list ? first.values?.[0] : first?.value;
  return value && /^\d{4}-\d{2}-\d{2}/.test(value)
    ? value.slice(0, 10)
    : undefined;
}

// A slot of a DAG with dates falls in the row of the day or month it loads,
// so choosing October chooses the cells that load October.
const slotKey = (slot: DAGSlot, unit: 'day' | 'month') => {
  const date = labelDate(slot);
  if (date) return unit === 'day' ? date : date.slice(0, 7);
  return dayjs(slot.slot).format(unit === 'day' ? 'YYYY-MM-DD' : 'YYYY-MM');
};

/**
 * A DAG's history by slot rather than by run: each fire time of its schedule
 * in a range, with the state its runs give it. A user who may run the DAG
 * picks slots, one by one or with Shift for a run of them, and backfills the
 * picked slots or the whole range. The slots are read when the view opens,
 * when the range changes, and on refresh.
 */
export function DAGSlotsView({
  fileName,
  canExecute,
}: {
  fileName: string;
  /** Whether the user may run the DAG, and so pick slots and backfill them. */
  canExecute: boolean;
}) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const navigate = useNavigate();
  const [range, setRange] = useState<RangeId>('7d');
  // The start of the range is fixed when it is chosen, so the query keeps
  // one key until then.
  const from = useMemo(() => {
    const days = RANGES.find((r) => r.id === range)?.days ?? 7;
    return dayjs().subtract(days, 'day').toISOString();
  }, [range]);
  const { data, error, mutate } = useQuery(
    '/dags/{fileName}/slots',
    { params: { path: { fileName }, query: { remoteNode, from } } },
    READ_ONCE
  );
  const [selected, setSelected] = useState<Set<string>>(new Set());
  // The slot a Shift-pick extends from.
  const [anchor, setAnchor] = useState<string | null>(null);
  const [target, setTarget] = useState<BackfillTarget | null>(null);

  const slots = useMemo(() => data?.slots ?? [], [data]);
  const shown = useMemo(() => slots.slice(-SHOWN_SLOTS), [slots]);
  const groups = useMemo(() => groupSlots(shown, slotKey), [shown]);
  const index = useMemo(
    () => new Map(shown.map((slot, i) => [slot.slot, i])),
    [shown]
  );
  // A refresh can move slots out of the window drawn: they are picked no
  // more.
  const picked = useMemo(
    () => new Set([...selected].filter((slot) => index.has(slot))),
    [selected, index]
  );
  const counts = useMemo(() => {
    const result = new Map<DAGSlotStatus, number>();
    for (const slot of slots) {
      result.set(slot.status, (result.get(slot.status) ?? 0) + 1);
    }
    return result;
  }, [slots]);

  const pickRange = (next: RangeId) => {
    setRange(next);
    setSelected(new Set());
    setAnchor(null);
  };
  const pick = (slot: DAGSlot, extend: boolean) => {
    const at = index.get(slot.slot);
    if (at === undefined) return;
    const from = anchor === null ? null : (index.get(anchor) ?? null);
    setSelected(selectSlots(shown, picked, at, from, extend));
    setAnchor(slot.slot);
  };
  const pickStates = (states: readonly DAGSlotStatus[]) => {
    setSelected(slotsInStates(shown, states));
    setAnchor(null);
  };

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-xs font-medium text-muted-foreground">
          {ts('Slots')}
        </span>
        <div className="flex items-center gap-1">
          <div
            role="group"
            aria-label={ts('Range')}
            className="flex rounded-md border p-0.5"
          >
            {RANGES.map((r) => (
              <button
                key={r.id}
                type="button"
                aria-pressed={range === r.id}
                onClick={() => pickRange(r.id)}
                className={cn(
                  'rounded px-2 py-0.5 text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring',
                  range === r.id
                    ? 'bg-muted text-foreground'
                    : 'text-muted-foreground hover:text-foreground'
                )}
              >
                {ts(r.label)}
              </button>
            ))}
          </div>
          <RefreshButton
            onRefresh={async () => {
              await mutate();
            }}
          />
        </div>
      </div>

      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error instanceof Error
            ? error.message
            : typeof error === 'object' && error && 'message' in error
              ? String((error as { message: unknown }).message)
              : ts('Failed to load the slots')}
        </p>
      ) : !data ? (
        <p className="text-sm text-muted-foreground">{ts('Loading...')}</p>
      ) : slots.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {ts('The schedule fires no slot in this range.')}
        </p>
      ) : (
        <>
          <ul
            className="flex flex-wrap gap-x-3 gap-y-1 text-xs"
            aria-label={ts('Slot states')}
          >
            {DAG_SLOT_STATES.filter((state) => counts.get(state)).map(
              (state) => {
                const look = DAG_SLOT_LOOK[state];
                const Icon = look.icon;
                return (
                  <li key={state} className="flex items-center gap-1">
                    <Icon
                      className="h-3.5 w-3.5"
                      style={{ color: look.color }}
                      aria-hidden="true"
                    />
                    <span className="tabular-nums">{counts.get(state)}</span>
                    <span className="text-muted-foreground">
                      {ts(look.label)}
                    </span>
                  </li>
                );
              }
            )}
          </ul>

          <div className="flex flex-col gap-1.5">
            {groups.map((group) => (
              <div key={group.key} className="flex items-start gap-3">
                <span className="w-20 shrink-0 pt-0.5 font-mono text-xs tabular-nums text-muted-foreground">
                  {group.key}
                </span>
                <div className="flex min-w-0 flex-wrap gap-1">
                  {group.slots.map((slot) => (
                    <SlotCell
                      key={slot.slot}
                      slot={slot}
                      dayLabel={group.key.length === 7}
                      fileName={fileName}
                      remoteNode={remoteNode}
                      selected={picked.has(slot.slot)}
                      onPick={canExecute ? pick : undefined}
                    />
                  ))}
                </div>
              </div>
            ))}
          </div>
          {slots.length > shown.length && (
            <p className="text-xs text-muted-foreground">
              {ts(
                'Showing the newest {shown} of {total} slots. Pick a shorter range to see the rest.',
                { shown: shown.length, total: slots.length }
              )}
            </p>
          )}

          {canExecute && (
            <div className="flex flex-wrap items-center gap-2 border-t pt-3">
              {counts.get(DAGSlotStatus.missing) ? (
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => pickStates([DAGSlotStatus.missing])}
                >
                  {ts('Pick missing')}
                </Button>
              ) : null}
              {counts.get(DAGSlotStatus.failed) ? (
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() =>
                    pickStates([DAGSlotStatus.missing, DAGSlotStatus.failed])
                  }
                >
                  {ts('Pick missing and failed')}
                </Button>
              ) : null}
              {picked.size > 0 && (
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => pickStates([])}
                >
                  {ts('Clear')}
                </Button>
              )}
              <span className="flex-1" />
              <Button
                variant="primary"
                size="sm"
                onClick={() =>
                  setTarget(
                    picked.size > 0
                      ? {
                          kind: 'slots',
                          slots: shown
                            .filter((slot) => picked.has(slot.slot))
                            .map((slot) => slot.slot),
                        }
                      : { kind: 'range', from }
                  )
                }
              >
                <History />
                {picked.size > 0
                  ? picked.size === 1
                    ? ts('Backfill 1 slot')
                    : ts('Backfill {count} slots', { count: picked.size })
                  : ts('Backfill this range')}
              </Button>
            </div>
          )}
        </>
      )}

      {target && (
        <StartBackfillDialog
          fileName={fileName}
          remoteNode={remoteNode}
          target={target}
          open
          onOpenChange={(open) => {
            if (!open) setTarget(null);
          }}
          onStarted={(backfill) =>
            navigate(backfillPath(backfill.id, remoteNode))
          }
        />
      )}
    </section>
  );
}

/**
 * A slot's cell: its state by icon and color, and, in a row of a month, the
 * day it loads. Its name gives the slot, the dates it loads, and its state;
 * hovering it adds the window, every date, and how its run started.
 */
function SlotCell({
  slot,
  dayLabel,
  fileName,
  remoteNode,
  selected,
  onPick,
}: {
  slot: DAGSlot;
  /** The row is a month, so the cell shows the day it loads. */
  dayLabel: boolean;
  fileName: string;
  remoteNode: string;
  selected: boolean;
  onPick?: (slot: DAGSlot, extend: boolean) => void;
}) {
  const { ts } = useI18n();
  const look = DAG_SLOT_LOOK[slot.status];
  const Icon = look.icon;
  const time = (value?: string) =>
    formatAbsoluteTime(value, { seconds: false }) ?? value ?? '';
  const first = slot.dates?.[0];
  const label = [
    ts('Slot {time}', { time: time(slot.slot) }),
    first && ts('loads {date}', { date: dateSummary(first) }),
    ts(look.label),
  ]
    .filter(Boolean)
    .join(' · ');
  const details = [
    label,
    slot.windowStart &&
      ts('window from {time}', { time: time(slot.windowStart) }),
    ...(slot.dates ?? []).map((date) => `${date.name} ${dateText(date)}`),
    slot.trigger && ts(triggerTypeLabels[slot.trigger] ?? slot.trigger),
  ]
    .filter(Boolean)
    .join('\n');
  const day = dayLabel ? labelDate(slot)?.slice(8, 10) : undefined;
  const cell = (
    <span
      className={cn(
        'flex h-5 min-w-5 items-center justify-center gap-0.5 rounded',
        day && 'px-1',
        selected && 'ring-2 ring-primary ring-offset-1 ring-offset-background'
      )}
      style={{
        backgroundColor: `color-mix(in srgb, ${look.color} 16%, transparent)`,
      }}
    >
      <Icon
        className="h-3.5 w-3.5"
        style={{ color: look.color }}
        aria-hidden="true"
      />
      {day && (
        <span className="font-mono text-[10px] tabular-nums text-foreground">
          {day}
        </span>
      )}
    </span>
  );
  const focus =
    'rounded outline-none focus-visible:ring-2 focus-visible:ring-ring';
  if (onPick) {
    return (
      <button
        type="button"
        title={details}
        aria-label={label}
        aria-pressed={selected}
        className={focus}
        onClick={(event) => onPick(slot, event.shiftKey)}
      >
        {cell}
      </button>
    );
  }
  if (slot.runId) {
    return (
      <Link
        to={`${workflowPath(fileName, remoteNode)}&run=${encodeURIComponent(slot.runId)}`}
        title={details}
        aria-label={label}
        className={focus}
      >
        {cell}
      </Link>
    );
  }
  return (
    <span role="img" title={details} aria-label={label}>
      {cell}
    </span>
  );
}
