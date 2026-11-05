// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown } from 'lucide-react';
import type { ReactNode } from 'react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import RelativeTime from '@/components/ui/relative-time';
import { StatusDot } from '@/components/ui/status-dot';
import { useI18n } from '@/i18n/I18nProvider';
import { shortRunId } from '@/lib/shortRunId';
import { cn, FOCUS_RING } from '@/lib/utils';
import type { ApiDAGRunDetails } from '../contracts';
import { runTime } from './runTimelineModel';

/** The facts of a run on one line: status, start and short ID. */
function RunLine({ run, status }: { run: ApiDAGRunDetails; status: string }) {
  return (
    <>
      <StatusDot kind="run" status={run.status} decorative />
      <span className="min-w-0 truncate font-medium">{status}</span>
      <RelativeTime
        compact
        ago
        timestamp={runTime(run)}
        className="shrink-0 text-muted-foreground"
      />
      <span className="ml-auto shrink-0 font-mono text-[11px] text-muted-foreground">
        {shortRunId(run.dagRunId)}
      </span>
    </>
  );
}

export interface RunPickerProps {
  /** The runs to choose from, oldest first, as the timeline draws them. */
  runs: readonly ApiDAGRunDetails[];
  selectedId: string | null;
  statusOf(run: ApiDAGRunDetails): string;
  onSelect(runId: string): void;
  /** Controls at the end of the row, such as the View menu. */
  trailing?: ReactNode;
}

/**
 * The run that the Run panel shows, as one row with a menu: the timeline of
 * a phone, where the bars of the runs have no room. The menu lists the runs
 * newest first, each with its status, start and short ID.
 */
export function RunPicker({
  runs,
  selectedId,
  statusOf,
  onSelect,
  trailing,
}: RunPickerProps) {
  const { ts } = useI18n();
  const selected = runs.find((run) => run.dagRunId === selectedId);
  return (
    <div
      data-testid="ww-run-picker-row"
      className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-1.5"
    >
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            data-testid="ww-run-picker"
            disabled={runs.length === 0}
            className={cn(
              'flex h-9 min-w-0 flex-1 items-center gap-2 rounded-md border border-input bg-background px-2.5 text-left text-xs disabled:opacity-50',
              FOCUS_RING
            )}
          >
            <span className="sr-only">{ts('Run')}: </span>
            {selected ? (
              <RunLine run={selected} status={statusOf(selected)} />
            ) : (
              <span className="text-muted-foreground">{ts('No run')}</span>
            )}
            <ChevronDown
              aria-hidden="true"
              className="size-3.5 shrink-0 text-muted-foreground"
            />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="start"
          className="max-h-80 w-(--radix-dropdown-menu-trigger-width) overflow-y-auto"
        >
          <DropdownMenuRadioGroup
            value={selectedId ?? ''}
            onValueChange={onSelect}
          >
            {[...runs].reverse().map((run) => (
              <DropdownMenuRadioItem
                key={run.dagRunId}
                value={run.dagRunId}
                className="gap-2 py-2 text-xs"
              >
                <RunLine run={run} status={statusOf(run)} />
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {trailing}
    </div>
  );
}
