// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CalendarRange, FlaskConical, History, ListFilter } from 'lucide-react';
import {
  memo,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { createPortal } from 'react-dom';
import type { TriggerType } from '@/api/v1/schema';
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { IconButton } from '@/components/ui/icon-button';
import { triggerTypeLabels } from '@/features/dags/components/common/TriggerTypeIndicator';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import { cn } from '@/lib/utils';
import { Z } from '../zLayers';
import {
  RANGE_PRESETS,
  type RangePresetId,
  type RunRange,
} from './runTimelineModel';

const INPUT_FORMAT = 'YYYY-MM-DDTHH:mm';
const PANEL_WIDTH = 248;
const MARGIN = 8;
/** The value of the option that stands for the custom range. */
const CUSTOM_VALUE = 'range:custom';

const toInput = (ms: number) => dayjs(ms).format(INPUT_FORMAT);
/** The time of a datetime-local text, in local time; null when it is not complete. */
const fromInput = (text: string): number | null => {
  if (!text) return null;
  const ms = Date.parse(text);
  return Number.isFinite(ms) ? ms : null;
};

/**
 * The two ends of a custom range, as two datetime-local boxes in a panel
 * above the button. A range with the start after the end is not applied: the
 * panel says so. Esc and a pointer down outside close it.
 */
function CustomRangePanel({
  range,
  anchor,
  onApply,
  onClose,
}: {
  range: Extract<RunRange, { preset: 'custom' }>;
  anchor: HTMLElement | null;
  onApply(range: RunRange): void;
  onClose(): void;
}) {
  const { ts } = useI18n();
  const id = useId();
  const panelRef = useRef<HTMLDivElement>(null);
  const [from, setFrom] = useState(() => toInput(range.from));
  const [to, setTo] = useState(() => toInput(range.to));
  const [pos, setPos] = useState<{ bottom: number; left: number } | null>(null);
  const fromMs = fromInput(from);
  const toMs = fromInput(to);
  const invalid = fromMs !== null && toMs !== null && fromMs > toMs;

  useLayoutEffect(() => {
    const place = () => {
      const rect = anchor?.getBoundingClientRect();
      if (!rect) return;
      const width = Math.min(PANEL_WIDTH, window.innerWidth - 2 * MARGIN);
      setPos({
        bottom: window.innerHeight - rect.top + 4,
        left: Math.max(
          MARGIN,
          Math.min(rect.right - width, window.innerWidth - width - MARGIN)
        ),
      });
    };
    place();
    window.addEventListener('resize', place);
    return () => window.removeEventListener('resize', place);
  }, [anchor]);

  useEffect(() => {
    const onPointerDown = (event: Event) => {
      const target = event.target as Node;
      if (panelRef.current?.contains(target) || anchor?.contains(target)) {
        return;
      }
      onClose();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.stopPropagation();
      onClose();
    };
    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('keydown', onKeyDown, true);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown, true);
    };
  }, [anchor, onClose]);

  // A valid pair is applied at once; an invalid or a partial one waits.
  const change = (nextFrom: string, nextTo: string) => {
    setFrom(nextFrom);
    setTo(nextTo);
    const a = fromInput(nextFrom);
    const b = fromInput(nextTo);
    if (a !== null && b !== null && a <= b) {
      onApply({ preset: 'custom', from: a, to: b });
    }
  };

  const inputClass =
    'h-6 w-full rounded-md border border-input bg-background px-1.5 text-[11px] text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring aria-[invalid=true]:border-destructive';
  return createPortal(
    <div
      ref={panelRef}
      role="dialog"
      aria-label={ts('Custom range')}
      data-testid="ww-run-timeline-range-panel"
      className={cn(
        'fixed flex flex-col gap-2 rounded-lg border border-border bg-popover p-3 text-xs text-popover-foreground shadow-lg',
        Z.menu
      )}
      style={{
        ...(pos ?? { bottom: 0, left: 0, visibility: 'hidden' }),
        width: `min(${PANEL_WIDTH}px, calc(100vw - ${2 * MARGIN}px))`,
      }}
    >
      <label className="flex flex-col gap-1" htmlFor={`${id}-from`}>
        <span className="text-muted-foreground">{ts('From')}</span>
        <input
          id={`${id}-from`}
          type="datetime-local"
          data-testid="ww-run-timeline-range-from"
          value={from}
          max={to || undefined}
          aria-invalid={invalid}
          onChange={(event) => change(event.target.value, to)}
          className={inputClass}
        />
      </label>
      <label className="flex flex-col gap-1" htmlFor={`${id}-to`}>
        <span className="text-muted-foreground">{ts('To')}</span>
        <input
          id={`${id}-to`}
          type="datetime-local"
          data-testid="ww-run-timeline-range-to"
          value={to}
          min={from || undefined}
          aria-invalid={invalid}
          onChange={(event) => change(from, event.target.value)}
          className={inputClass}
        />
      </label>
      {invalid && (
        <p
          role="alert"
          data-testid="ww-run-timeline-range-error"
          className="text-destructive"
        >
          {ts('The start must not be after the end.')}
        </p>
      )}
    </div>,
    document.body
  );
}

export interface RunTimelineControlsProps {
  /** The count of the newest runs and the counts that the select offers. */
  count: number;
  counts: readonly number[];
  onCountChange(count: number): void;
  /** The time range that picks the runs, or null while the count does. */
  range?: RunRange | null;
  /** Offers the time range; without it the select has the counts only. */
  onRangeChange?(range: RunRange): void;
  /** The note of the range: the history does not reach back that far. */
  truncatedNote?: string | null;
  hasTestRuns?: boolean;
  showTests?: boolean;
  onShowTestsChange?(show: boolean): void;
  hasCachedTime?: boolean;
  showCached?: boolean;
  onShowCachedChange?(show: boolean): void;
  /**
   * The trigger types that the history holds and those that the timeline
   * hides. The filter menu shows when the history has more than one type.
   */
  triggerTypes?: readonly TriggerType[];
  hiddenTypes?: readonly TriggerType[];
  onHiddenTypesChange?(types: readonly TriggerType[]): void;
}

/**
 * The one header row of the timeline. Its slots keep their place whatever
 * the state: the note, the test runs toggle, the cached time toggle, the
 * custom range button and the select of the runs (the newest ones, or a time
 * range). A toggle that has nothing to act on leaves an empty slot.
 */
export const RunTimelineControls = memo(function RunTimelineControls({
  count,
  counts,
  onCountChange,
  range = null,
  onRangeChange,
  truncatedNote = null,
  hasTestRuns = false,
  showTests = true,
  onShowTestsChange,
  hasCachedTime = false,
  showCached = true,
  onShowCachedChange,
  triggerTypes = [],
  hiddenTypes = [],
  onHiddenTypesChange,
}: RunTimelineControlsProps) {
  const { ts } = useI18n();
  const [panelOpen, setPanelOpen] = useState(false);
  // A state, not a ref object: the panel opens in the render that adds the button.
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const custom = range?.preset === 'custom' ? range : null;
  const shownCounts = counts.includes(count) ? counts : [...counts, count];
  const value = range ? `range:${range.preset}` : String(count);

  const onSelect = (next: string) => {
    if (next.startsWith('range:')) {
      const preset = next.slice('range:'.length);
      if (preset === 'custom') {
        // Starts from the last custom range, else from the last 24 hours.
        const now = Date.now();
        onRangeChange?.(
          custom ?? { preset: 'custom', from: now - 24 * 3_600_000, to: now }
        );
        setPanelOpen(true);
        return;
      }
      setPanelOpen(false);
      onRangeChange?.({ preset: preset as RangePresetId });
      return;
    }
    setPanelOpen(false);
    onCountChange(Number(next));
  };

  return (
    <div
      data-testid="ww-run-timeline-controls"
      className="flex h-6 items-center justify-end gap-1.5"
    >
      <p
        data-testid="ww-run-timeline-note"
        title={truncatedNote ?? undefined}
        className="min-w-0 flex-1 truncate text-right text-[11px] text-muted-foreground"
      >
        {truncatedNote}
      </p>
      {onHiddenTypesChange ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <IconButton
              size="sm"
              icon={ListFilter}
              label={ts('Filter runs by trigger')}
              // With one trigger type there is nothing to choose.
              disabledReason={
                triggerTypes.length <= 1 && hiddenTypes.length === 0
                  ? ts('Only one trigger type in these runs')
                  : undefined
              }
              pressed={hiddenTypes.length > 0}
              data-testid="ww-run-timeline-types"
            />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-40">
            <DropdownMenuLabel>{ts('Triggered by')}</DropdownMenuLabel>
            <DropdownMenuSeparator />
            {triggerTypes.map((type) => (
              <DropdownMenuCheckboxItem
                key={type}
                checked={!hiddenTypes.includes(type)}
                data-testid={`ww-run-timeline-type-${type}`}
                // The menu stays open: several types are changed in a row.
                onSelect={(event) => event.preventDefault()}
                onCheckedChange={(on) =>
                  onHiddenTypesChange(
                    on
                      ? hiddenTypes.filter((t) => t !== type)
                      : [...hiddenTypes, type]
                  )
                }
              >
                {ts(triggerTypeLabels[type])}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      ) : (
        <span className="size-6 shrink-0" aria-hidden="true" />
      )}
      {onShowTestsChange ? (
        <IconButton
          size="sm"
          icon={FlaskConical}
          label={ts(showTests ? 'Hide test runs' : 'Show test runs')}
          disabledReason={
            !hasTestRuns && showTests
              ? ts('No test run in these runs')
              : undefined
          }
          pressed={showTests}
          data-testid="ww-run-timeline-tests"
          onClick={() => onShowTestsChange(!showTests)}
        />
      ) : (
        <span className="size-6 shrink-0" aria-hidden="true" />
      )}
      {onShowCachedChange ? (
        <IconButton
          size="sm"
          icon={History}
          label={ts(showCached ? 'Hide cached time' : 'Show cached time')}
          disabledReason={
            !hasCachedTime && showCached
              ? ts('No cached time in these runs')
              : undefined
          }
          pressed={showCached}
          data-testid="ww-run-timeline-cached"
          onClick={() => onShowCachedChange(!showCached)}
        />
      ) : (
        <span className="size-6 shrink-0" aria-hidden="true" />
      )}
      {custom && onRangeChange ? (
        <span ref={setAnchor} className="inline-flex shrink-0">
          <IconButton
            size="sm"
            icon={CalendarRange}
            label={ts('Edit custom range')}
            pressed={panelOpen}
            aria-expanded={panelOpen}
            aria-haspopup="dialog"
            data-testid="ww-run-timeline-range-edit"
            onClick={() => setPanelOpen((open) => !open)}
          />
        </span>
      ) : (
        <span className="size-6 shrink-0" aria-hidden="true" />
      )}
      <select
        data-testid="ww-run-timeline-count"
        aria-label={ts('Number of runs shown')}
        value={value}
        onChange={(event) => onSelect(event.target.value)}
        className="h-6 w-[8.75rem] shrink-0 rounded-md border border-input bg-background px-1.5 text-[11px] text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        {onRangeChange ? (
          <>
            <optgroup label={ts('Newest runs')}>
              {shownCounts.map((n) => (
                <option key={n} value={n}>
                  {ts('Last {n} runs', { n })}
                </option>
              ))}
            </optgroup>
            <optgroup label={ts('Time range')}>
              {RANGE_PRESETS.map((preset) => (
                <option key={preset.id} value={`range:${preset.id}`}>
                  {ts(preset.label)}
                </option>
              ))}
              <option value={CUSTOM_VALUE}>{ts('Custom range')}</option>
            </optgroup>
          </>
        ) : (
          shownCounts.map((n) => (
            <option key={n} value={n}>
              {ts('Last {n} runs', { n })}
            </option>
          ))
        )}
      </select>
      {custom && onRangeChange && panelOpen && (
        <CustomRangePanel
          range={custom}
          anchor={anchor}
          onApply={onRangeChange}
          onClose={() => setPanelOpen(false)}
        />
      )}
    </div>
  );
});
