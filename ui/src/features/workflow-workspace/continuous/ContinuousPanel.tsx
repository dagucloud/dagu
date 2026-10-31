// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactElement,
} from 'react';
import type { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { PanelMessage } from '@/components/ui/panel-message';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { useElementWidth } from '@/hooks/useElementWidth';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { AvailabilityStrip } from './AvailabilityStrip';
import { EventsList } from './EventsList';
import { AXIS_PX, SamplesChart } from './SamplesChart';
import {
  bucketWindow,
  panView,
  POLLED_PRESETS,
  pointsForWindow,
  presetView,
  RANGE_PRESETS,
  viewAt,
  zoomView,
  type RangePresetId,
  type View,
} from './sampleBars';
import {
  useReadAtEnd,
  useStepAvailability,
  useStepSamples,
  type ContinuousTarget,
} from './useContinuousData';

type Node = components['schemas']['Node'];

/** The width of the chart where the host has no layout (a test, a hidden tab). */
const DEFAULT_WIDTH_PX = 640;
const DEFAULT_PRESET: RangePresetId = '24h';
/** The padding of the panel, left and right. */
const PANEL_PAD_PX = 24;
/** How long a zoom or a pan rests before the chart asks for its window. */
const SETTLE_MS = 300;

/** The window as fixed times: a zoom or a pan starts from what the chart shows. */
function fixed(view: View, now: number): View {
  return { ...view, ...viewAt(view, now) };
}

export interface ContinuousPanelProps {
  /** The run: the root run that holds the step. */
  name: string;
  runId: string;
  /** The continuous step. */
  node: Node;
  /** The run is active: the short windows and the lists poll. */
  active: boolean;
  /** The start of the run, for the preset "All". */
  startedAt?: string;
}

/**
 * The Continuous panel: how a step that repeats without a limit went. The
 * availability of four windows, the chart of the samples over a window that
 * zooms from one hour to years, and the events. Samples and events come from
 * the API in buckets and pages, so the panel stays small for a run of years.
 */
export function ContinuousPanel({
  name,
  runId,
  node,
  active,
  startedAt,
}: ContinuousPanelProps): ReactElement {
  const { ts } = useI18n();
  const target = useMemo<ContinuousTarget>(
    () => ({ name, runId, step: node.step.name }),
    [name, runId, node.step.name]
  );
  const startMs = startedAt ? Date.parse(startedAt) : undefined;

  const [view, setView] = useState<View>(() =>
    presetView(DEFAULT_PRESET, Date.now())
  );
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

  const [selected, setSelected] = useState<{
    time: number;
    resolutionSeconds: number;
  } | null>(null);

  const rootRef = useRef<HTMLDivElement>(null);
  const measured = useElementWidth(rootRef);
  const width = measured ?? DEFAULT_WIDTH_PX;

  const chartWidth = width - PANEL_PAD_PX;
  const points = pointsForWindow(chartWidth - AXIS_PX, asked.to - asked.from);
  const availability = useStepAvailability(target, active);
  const samples = useStepSamples(
    target,
    asked.preset === null
      ? { from: asked.from, to: asked.to }
      : { preset: asked.preset, startedAt: startMs },
    points,
    polled
  );
  useReadAtEnd(active, availability.mutate);
  useReadAtEnd(active, samples.mutate);

  const choose = (id: RangePresetId) => {
    setSelected(null);
    setView(presetView(id, Date.now(), startMs));
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
        {ts('Could not load {thing}', {
          thing: ts('the samples of this step'),
        })}
      </PanelMessage>
    );
  }

  return (
    <div
      ref={rootRef}
      data-testid="continuous-panel"
      data-step={node.step.name}
      className="h-full min-h-0 space-y-3 overflow-auto p-3"
    >
      {availability.data ? (
        <AvailabilityStrip data={availability.data} since={node.startedAt} />
      ) : availability.error ? (
        <PanelMessage tone="error" align="start" className="p-0">
          {ts('Could not load {thing}', {
            thing: ts('the availability of this step'),
          })}
        </PanelMessage>
      ) : (
        <PanelMessage tone="muted" align="start" className="p-0">
          {ts('Loading availability...')}
        </PanelMessage>
      )}

      <section aria-label={ts('Samples')} className="space-y-2">
        <ToggleGroup aria-label={ts('Period')} className="flex-wrap">
          {[
            ...RANGE_PRESETS.map((p) => ({
              id: p.id as RangePresetId,
              label: p.label,
            })),
            { id: 'all' as RangePresetId, label: ts('All') },
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
        <div className="pt-2">
          {samples.error && !samples.data ? (
            <PanelMessage tone="error" align="start" className="p-0">
              {ts('Could not load {thing}', {
                thing: ts('the samples of this step'),
              })}
            </PanelMessage>
          ) : !samples.data ? (
            <PanelMessage tone="muted" align="start" className="p-0">
              {ts('Loading samples...')}
            </PanelMessage>
          ) : samples.data.buckets.length === 0 ? (
            <div data-testid="continuous-empty">
              <PanelMessage tone="muted" align="center" className="py-8">
                {ts('No samples yet')}
              </PanelMessage>
            </div>
          ) : (
            <SamplesChart
              samples={samples.data}
              window={shown}
              width={chartWidth}
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
        <EventsList target={target} window={eventsWindow} poll={active} />
      </section>
    </div>
  );
}
