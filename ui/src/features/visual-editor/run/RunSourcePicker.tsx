// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Archive, ChevronDown, FlaskConical } from 'lucide-react';
import type { ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import RelativeTime from '@/components/ui/relative-time';
import StatusIcon from '@/components/ui/status-icon';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime, formatElapsed } from '@/lib/dagRunTiming';
import { shortRunId } from '@/lib/shortRunId';
import { cn } from '@/lib/utils';
import type { SourceOption } from './runSourceOptions';

type Translate = ReturnType<typeof useI18n>['ts'];

/** The text of the cached values: the run that they were read from, and the time. */
export function cacheOptionText(option: SourceOption, ts: Translate): string {
  const cache = option.cache;
  if (!cache) return ts('Cached values');
  const time = formatAbsoluteTime(cache.capturedAt) ?? '';
  const ids = cache.runIds.map(shortRunId);
  return ids.length === 1
    ? ts('Cached values (from run {id}, {time})', { id: ids[0] ?? '', time })
    : ts('Cached values (from runs {ids}, {time})', {
        ids: ids.join(', '),
        time,
      });
}

/** The reason that an option cannot be chosen. */
export function blockedText(
  option: SourceOption,
  ts: Translate
): string | null {
  const blocked = option.blocked;
  if (!blocked) return null;
  return blocked.reason === 'active'
    ? ts('still running')
    : ts('missing: {steps}', { steps: blocked.steps.join(', ') });
}

function RunLine({ option }: { option: SourceOption }): ReactElement {
  const { ts } = useI18n();
  const elapsed = formatElapsed(option.startedAt, option.finishedAt);
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      {option.status !== undefined && (
        <StatusIcon status={option.status} className="size-3.5 shrink-0" />
      )}
      <span className="font-mono" title={option.runId}>
        {shortRunId(option.runId ?? '')}
      </span>
      {option.startedAt && (
        <span className="text-muted-foreground">
          <RelativeTime timestamp={option.startedAt} compact ago />
        </span>
      )}
      {elapsed && (
        <span className="tabular-nums text-muted-foreground">{elapsed}</span>
      )}
      {option.ephemeral && (
        <FlaskConical
          role="img"
          aria-label={ts('Test run')}
          data-testid="run-source-test"
          className="size-3 shrink-0 text-muted-foreground"
        />
      )}
    </span>
  );
}

/**
 * The run that a step run takes the outputs of the previous steps from, as a
 * compact menu: one line for each run that can hold them, newest first, and
 * the cached values last, with the run that they were read from. The first
 * line is the automatic choice. A line that cannot be chosen says why. The
 * one component of the step run dialog and of the INPUT column.
 */
export function RunSourcePicker({
  options,
  selectedId,
  onSelect,
  className,
}: {
  options: readonly SourceOption[];
  /** The id of the option that the step run uses now; null with none. */
  selectedId: string | null;
  /** The user chose an option. `auto` is true for the automatic one. */
  onSelect(option: SourceOption): void;
  className?: string;
}): ReactElement | null {
  const { ts } = useI18n();
  const current = options.find((o) => o.id === selectedId);
  // A control that offers no choice does not show.
  if (options.length === 0) return null;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="outline"
          size="xs"
          data-testid="run-source-picker"
          aria-label={ts('Run to take the inputs from')}
          className={cn(
            'h-6 max-w-full justify-between gap-1 px-1.5',
            className
          )}
        >
          <span className="flex min-w-0 items-center gap-1.5 truncate">
            {current?.kind === 'run' ? (
              <RunLine option={current} />
            ) : current?.kind === 'cache' ? (
              <span className="flex min-w-0 items-center gap-1.5">
                <Archive aria-hidden="true" className="size-3.5" />
                {ts('Cached values')}
              </span>
            ) : (
              <span className="text-muted-foreground">{ts('No run')}</span>
            )}
          </span>
          <ChevronDown aria-hidden="true" className="size-3 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="start"
        data-testid="run-source-menu"
        className="max-h-72 w-max max-w-[min(26rem,90vw)] overflow-y-auto"
      >
        {options.map((option) => {
          const reason = blockedText(option, ts);
          return (
            <DropdownMenuItem
              key={option.id}
              // A plain menu item has no checked state for aria-checked.
              role="menuitemradio"
              disabled={reason !== null}
              data-testid={`run-source-option-${option.id}`}
              data-selected={option.id === selectedId ? 'true' : undefined}
              aria-checked={option.id === selectedId}
              onSelect={() => onSelect(option)}
              className={cn(
                'flex-col items-start gap-0.5 text-xs',
                option.id === selectedId && 'bg-accent/60'
              )}
            >
              {option.kind === 'run' ? (
                <span className="flex items-center gap-2">
                  <RunLine option={option} />
                  {option.auto && (
                    <span className="rounded-sm bg-muted px-1 text-[10px] text-muted-foreground">
                      {ts('Automatic')}
                    </span>
                  )}
                </span>
              ) : (
                <span className="flex items-center gap-1.5">
                  <Archive aria-hidden="true" className="size-3.5" />
                  {cacheOptionText(option, ts)}
                </span>
              )}
              {reason && (
                <span
                  data-testid="run-source-reason"
                  className="text-[10px] text-muted-foreground"
                >
                  {reason}
                </span>
              )}
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
