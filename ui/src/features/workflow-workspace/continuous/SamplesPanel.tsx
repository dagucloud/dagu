// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import { Button } from '@/components/ui/button';
import { PanelMessage } from '@/components/ui/panel-message';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { useElementWidth } from '@/hooks/useElementWidth';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import type { RangePreset } from '../rangePresets';
import { AvailabilityStrip } from './AvailabilityStrip';
import { EventsList, type RunLinks } from './EventsList';
import { AXIS_PX, SamplesChart } from './SamplesChart';
import {
  bucketWindow,
  panView,
  POLLED_PRESETS,
  pointsForWindow,
  presetView,
  viewAt,
  zoomView,
  type RangePresetId,
  type View,
} from './sampleBars';
import { unitOf, type SampleSource } from './sampleSource';
import {
  useReadAtEnd,
  useSampleAvailability,
  useSamples,
} from './useContinuousData';

/** The width of the chart where the host has no layout (a test, a hidden tab). */
const DEFAULT_WIDTH_PX = 640;
const DEFAULT_PRESET: RangePresetId = '24h';
/** How long a zoom or a pan rests before the chart asks for its window. */
const SETTLE_MS = 300;

/** The window as fixed times: a zoom or a pan starts from what the chart shows. */
function fixed(view: View, now: number): View {
  return { ...view, ...viewAt(view, now) };
}

/** The window that a host chooses: a preset that ends now, or fixed times. */
export type PanelRange =
  | { preset: Exclude<RangePresetId, 'all'> }
  | { from: number; to: number };

function viewOf(range: PanelRange, now: number): View {
  return 'preset' in range
    ? presetView(range.preset, now)
    : { from: range.from, to: range.to, preset: null };
}

const rangeKey = (range: PanelRange | undefined) =>
  range === undefined
    ? null
    : 'preset' in range
      ? range.preset
      : `${range.from}-${range.to}`;

/** The English texts that name what a source holds. */
interface Subject {
  title: string;
  samples: string;
  availability: string;
  events: string;
  empty: string;
}

const STEP_SUBJECT: Subject = {
  title: 'Samples',
  samples: 'the samples of this step',
  availability: 'the availability of this step',
  events: 'the events of this step',
  empty: 'No samples yet',
};

const HISTORY_SUBJECT: Subject = {
  title: 'Run history',
  samples: 'the run history',
  availability: 'the availability of the runs',
  events: 'the events of the run history',
  empty: 'No runs in this window',
};

export interface SamplesPanelProps {
  source: SampleSource;
  /** The short windows and the lists poll. */
  active: boolean;
  /** The start of the run in ms, for the period "All". */
  startedAt?: number;
  /** The start of the streak when no sample failed. */
  since?: string;
  /**
   * The periods that the panel offers as buttons, and "All". Without them
   * the host chooses the window with `range`.
   */
  presets?: readonly RangePreset[];
  /** The window of the host. The panel starts again at it when it changes. */
  range?: PanelRange;
  /** Opens the runs that an event names. */
  links?: RunLinks;
  /** A note above the chart, for the window that the chart shows. */
  notice?(window: { from: number; to: number }): ReactNode;
  testId?: string;
  /** The `data-step` of the root, for a step. */
  step?: string;
  className?: string;
}

/**
 * The panel of a series of samples: the availability, the chart over a
 * window that zooms from one hour to years, and the events of the selected
 * bucket. The source is a continuous step or the run history of a service
 * DAG. Samples and events come from the API in buckets and pages, so the
 * panel stays small for a window of years.
 */
export function SamplesPanel({
  source,
  active,
  startedAt,
  since,
  presets,
  range,
  links,
  notice,
  testId = 'continuous-panel',
  step,
  className = 'h-full min-h-0 space-y-3 overflow-auto p-3',
}: SamplesPanelProps): ReactElement {
  const { ts } = useI18n();
  const subject = source.kind === 'step' ? STEP_SUBJECT : HISTORY_SUBJECT;
  const unit = unitOf(source);

  const [view, setView] = useState<View>(() =>
    range ? viewOf(range, Date.now()) : presetView(DEFAULT_PRESET, Date.now())
  );
  const [selected, setSelected] = useState<{
    time: number;
    resolutionSeconds: number;
  } | null>(null);
  // A window that the host chooses again starts the view again.
  const [seenRange, setSeenRange] = useState(rangeKey(range));
  if (rangeKey(range) !== seenRange) {
    setSeenRange(rangeKey(range));
    if (range) {
      setView(viewOf(range, Date.now()));
      setSelected(null);
    }
  }
  // The window of the request: a preset at once, a zoom or a pan once it
  // rests, so that a drag does not ask for each of its moves.
  const [asked, setAsked] = useState(view);
  useEffect(() => {
    if (view.preset !== null) {
      setAsked(view);
      return;
    }
    const timer = setTimeout(() => setAsked(view), SETTLE_MS);
    return () => clearTimeout(timer);
  }, [view]);
  const polled =
    active && asked.preset !== null && POLLED_PRESETS.includes(asked.preset);

  const rootRef = useRef<HTMLDivElement>(null);
  const measured = useElementWidth(rootRef);
  const width = measured ?? DEFAULT_WIDTH_PX;

  // The measured width is the content width: the padding of the root is out.
  const points = pointsForWindow(width - AXIS_PX, asked.to - asked.from);
  const availability = useSampleAvailability(source, active);
  const samples = useSamples(
    source,
    asked.preset === null
      ? { from: asked.from, to: asked.to }
      : { preset: asked.preset, startedAt },
    points,
    polled
  );
  useReadAtEnd(active, availability.mutate);
  useReadAtEnd(active, samples.mutate);

  const choose = (id: RangePresetId) => {
    setSelected(null);
    setView(presetView(id, Date.now(), startedAt));
  };
  const zoom = useCallback((factor: number, ratio: number) => {
    const now = Date.now();
    setView((current) => zoomView(fixed(current, now), factor, ratio, now));
  }, []);
  const pan = useCallback((deltaMs: number) => {
    const now = Date.now();
    setView((current) => panView(fixed(current, now), deltaMs, now));
  }, []);

  // The server may cut a window to the buckets that it can serve: the chart
  // draws the window of the response, so that each bar is at its time.
  const shown = useMemo(() => {
    const from = samples.data ? Date.parse(samples.data.from) : Number.NaN;
    const to = samples.data ? Date.parse(samples.data.to) : Number.NaN;
    return Number.isFinite(from) && Number.isFinite(to) && to > from
      ? { from, to }
      : { from: asked.from, to: asked.to };
  }, [samples.data, asked]);

  const eventsWindow = selected
    ? bucketWindow(selected.time, selected.resolutionSeconds)
    : null;
  const failed =
    availability.error && !availability.data && samples.error && !samples.data;

  if (failed) {
    return (
      <PanelMessage tone="error" align="start">
        {ts('Could not load {thing}', { thing: ts(subject.samples) })}
      </PanelMessage>
    );
  }

  return (
    <div
      ref={rootRef}
      data-testid={testId}
      data-step={step}
      className={className}
    >
      {availability.data ? (
        <AvailabilityStrip data={availability.data} since={since} unit={unit} />
      ) : availability.error ? (
        <PanelMessage tone="error" align="start" className="p-0">
          {ts('Could not load {thing}', { thing: ts(subject.availability) })}
        </PanelMessage>
      ) : (
        <PanelMessage tone="muted" align="start" className="p-0">
          {ts('Loading availability...')}
        </PanelMessage>
      )}

      <section aria-label={ts(subject.title)} className="space-y-2">
        {presets && (
          <ToggleGroup aria-label={ts('Period')} className="flex-wrap">
            {[
              ...presets.map((p) => ({
                id: p.id as RangePresetId,
                label: p.short,
              })),
              // "All" starts at the start of the run: a history has none.
              ...(startedAt === undefined
                ? []
                : [{ id: 'all' as RangePresetId, label: ts('All') }]),
            ].map((p) => (
              <ToggleButton
                key={p.id}
                value={p.id}
                groupValue={view.preset ?? undefined}
                onClick={() => choose(p.id)}
                className="h-6 px-2"
              >
                {p.label}
              </ToggleButton>
            ))}
          </ToggleGroup>
        )}
        {notice?.(asked)}
        <div className="pt-2">
          {samples.error && !samples.data ? (
            <PanelMessage tone="error" align="start" className="p-0">
              {ts('Could not load {thing}', { thing: ts(subject.samples) })}
            </PanelMessage>
          ) : !samples.data ? (
            <PanelMessage tone="muted" align="start" className="p-0">
              {ts('Loading samples...')}
            </PanelMessage>
          ) : samples.data.buckets.every((b) => b.count <= 0) ? (
            <div data-testid="continuous-empty">
              <PanelMessage tone="muted" align="center" className="py-8">
                {ts(subject.empty)}
              </PanelMessage>
            </div>
          ) : (
            <SamplesChart
              samples={samples.data}
              window={shown}
              width={width}
              unit={unit}
              selected={selected?.time ?? null}
              onSelect={setSelected}
              onZoom={zoom}
              onPan={pan}
            />
          )}
        </div>
      </section>

      <section aria-label={ts('Events')} className="space-y-1">
        <div className="flex min-h-6 items-center gap-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">{ts('Events')}</span>
          {eventsWindow && (
            <>
              <span className="tabular-nums">
                {ts('{from} to {to}', {
                  from: formatAbsoluteTime(eventsWindow.from) ?? '',
                  to: formatAbsoluteTime(eventsWindow.to) ?? '',
                })}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="xs"
                onClick={() => setSelected(null)}
              >
                {ts('Show all events')}
              </Button>
            </>
          )}
        </div>
        <EventsList
          source={source}
          subject={subject}
          links={links}
          window={eventsWindow}
          poll={active}
        />
      </section>
    </div>
  );
}
