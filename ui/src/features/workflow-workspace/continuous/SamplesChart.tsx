// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent,
  type ReactElement,
} from 'react';
import type { components } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { formatExactCount } from '@/lib/formatCompactCount';
import { toneClasses } from '@/lib/statusPalette';
import { layoutLabels, timeTicks } from '../dock/runTimelineModel';
import { formatResolution, formatSpanMs } from './format';
import { barAt, barModel, p50Path, type Bar } from './sampleBars';

type StepSamples = components['schemas']['StepSamples'];

/** The height of the plot, in px. */
export const PLOT_HEIGHT_PX = 96;
/** The width that the latency labels take at the left of the plot. */
export const AXIS_PX = 44;
const LABEL_ROW_PX = 28;
/** A drag of less than this many px is a click. */
const CLICK_SLOP_PX = 4;

export interface SamplesChartProps {
  /** The response for the window. */
  samples: StepSamples;
  /** The window that the plot shows, in ms. */
  window: { from: number; to: number };
  /** The width of the whole chart, in px. */
  width: number;
  /** The start (ms) of the selected bucket, or null. */
  selected: number | null;
  onSelect(bucket: { time: number; resolutionSeconds: number } | null): void;
  /** `factor` under 1 zooms in; `ratio` is where the pointer is in the plot. */
  onZoom(factor: number, ratio: number): void;
  /** The window moves by this many ms. */
  onPan(deltaMs: number): void;
}

/**
 * The chart of the samples of a continuous step: one bar for each bucket of
 * the response, as tall as the p95 latency, with a thin p50 line and a red
 * part for the failures. It draws the buckets of the response and nothing
 * else, so the page weight does not follow the count of samples. The wheel
 * zooms, a drag moves the window, a click on a bar selects its bucket.
 */
export function SamplesChart({
  samples,
  window: view,
  width,
  selected,
  onSelect,
  onZoom,
  onPan,
}: SamplesChartProps): ReactElement {
  const { ts, locale } = useI18n();
  const plotWidth = Math.max(40, width - AXIS_PX);
  const model = useMemo(
    () =>
      barModel({
        buckets: samples.buckets,
        resolutionSeconds: samples.resolutionSeconds,
        from: view.from,
        to: view.to,
        width: plotWidth,
        height: PLOT_HEIGHT_PX,
      }),
    [samples, view.from, view.to, plotWidth]
  );
  const labels = useMemo(() => {
    const span = Math.max(1, view.to - view.from);
    return layoutLabels(
      timeTicks(view.from, view.to, plotWidth),
      (t) => ((t - view.from) / span) * plotWidth,
      plotWidth
    );
  }, [view.from, view.to, plotWidth]);

  const wrapRef = useRef<HTMLDivElement>(null);
  const drag = useRef<{ x: number; start: number; moved: boolean } | null>(
    null
  );
  const [hover, setHover] = useState<{ bar: Bar; x: number } | null>(null);

  // The wheel needs preventDefault, which a React handler cannot do.
  const zoom = useRef(onZoom);
  zoom.current = onZoom;
  useEffect(() => {
    const element = wrapRef.current;
    if (!element) return;
    const onWheel = (event: WheelEvent) => {
      // The ratio is over the plot, right of the latency labels.
      const rect = element.getBoundingClientRect();
      const plot = rect.width - AXIS_PX;
      // A horizontal swipe is not a zoom.
      if (plot <= 0 || event.deltaY === 0) return;
      event.preventDefault();
      const ratio = Math.min(
        1,
        Math.max(0, (event.clientX - rect.left - AXIS_PX) / plot)
      );
      zoom.current(Math.exp(Math.sign(event.deltaY) * 0.25), ratio);
    };
    element.addEventListener('wheel', onWheel, { passive: false });
    return () => element.removeEventListener('wheel', onWheel);
  }, []);

  const localX = (event: PointerEvent<SVGSVGElement>) =>
    event.clientX - event.currentTarget.getBoundingClientRect().left;

  const onPointerDown = (event: PointerEvent<SVGSVGElement>) => {
    drag.current = { x: event.clientX, start: event.clientX, moved: false };
    event.currentTarget.setPointerCapture?.(event.pointerId);
  };
  const onPointerMove = (event: PointerEvent<SVGSVGElement>) => {
    const state = drag.current;
    if (state) {
      if (
        !state.moved &&
        Math.abs(event.clientX - state.start) < CLICK_SLOP_PX
      ) {
        return;
      }
      state.moved = true;
      setHover(null);
      const span = view.to - view.from;
      onPan((-(event.clientX - state.x) / plotWidth) * span);
      state.x = event.clientX;
      return;
    }
    const x = localX(event);
    const bar = barAt(model.bars, x);
    setHover(bar ? { bar, x } : null);
  };
  // The pointer left, or the browser took it (pointercancel): no drag, no hover.
  const endDrag = () => {
    drag.current = null;
    setHover(null);
  };
  const onPointerUp = (event: PointerEvent<SVGSVGElement>) => {
    const state = drag.current;
    drag.current = null;
    if (!state || state.moved) return;
    const bar = barAt(model.bars, localX(event));
    if (!bar || bar.time === selected) {
      onSelect(null);
      return;
    }
    onSelect({ time: bar.time, resolutionSeconds: samples.resolutionSeconds });
  };

  const summary = ts('{count} buckets of {resolution}, from {from} to {to}', {
    count: formatExactCount(model.bars.length, locale),
    resolution: formatResolution(samples.resolutionSeconds),
    from: formatAbsoluteTime(view.from) ?? '',
    to: formatAbsoluteTime(view.to) ?? '',
  });

  return (
    <div
      ref={wrapRef}
      data-testid="continuous-chart"
      data-buckets={model.bars.length}
      className="relative select-none"
      style={{ height: PLOT_HEIGHT_PX + LABEL_ROW_PX }}
    >
      <div
        aria-hidden="true"
        className="absolute left-0 top-0 text-right text-[10px] tabular-nums text-muted-foreground"
        style={{ width: AXIS_PX - 6, height: PLOT_HEIGHT_PX }}
      >
        {model.ticks.map((tick) => (
          <span
            key={tick.ms}
            className="absolute right-0 -translate-y-1/2 leading-none"
            style={{ top: PLOT_HEIGHT_PX - tick.y }}
          >
            {formatSpanMs(tick.ms)}
          </span>
        ))}
      </div>
      <svg
        role="img"
        aria-label={summary}
        width={plotWidth}
        height={PLOT_HEIGHT_PX}
        className="absolute top-0 cursor-crosshair touch-none border-b border-border"
        style={{ left: AXIS_PX }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerLeave={endDrag}
        onPointerCancel={endDrag}
      >
        {model.ticks.map((tick) => (
          <line
            key={tick.ms}
            x1={0}
            x2={plotWidth}
            y1={PLOT_HEIGHT_PX - tick.y}
            y2={PLOT_HEIGHT_PX - tick.y}
            strokeDasharray="2 3"
            className="stroke-border"
          />
        ))}
        <g className={toneClasses('success').text} fill="currentColor">
          {model.bars.map((bar) => (
            <rect
              key={bar.time}
              x={bar.x}
              y={PLOT_HEIGHT_PX - bar.height}
              width={bar.width}
              height={bar.height}
              opacity={0.55}
            />
          ))}
        </g>
        <g className={toneClasses('error').text} fill="currentColor">
          {model.bars
            .filter((bar) => bar.failHeight > 0)
            .map((bar) => (
              <rect
                key={bar.time}
                data-testid="continuous-fail"
                x={bar.x}
                y={PLOT_HEIGHT_PX - bar.failHeight}
                width={bar.width}
                height={bar.failHeight}
              />
            ))}
        </g>
        <path
          d={p50Path(model, PLOT_HEIGHT_PX)}
          fill="none"
          strokeWidth={1}
          className="stroke-foreground"
        />
        {selected !== null &&
          model.bars
            .filter((bar) => bar.time === selected)
            .map((bar) => (
              <rect
                key={bar.time}
                x={bar.x - 1}
                y={0}
                width={bar.width + 2}
                height={PLOT_HEIGHT_PX}
                fill="none"
                strokeWidth={1}
                className="stroke-foreground"
              />
            ))}
      </svg>
      <div
        aria-hidden="true"
        className="absolute text-[10px] leading-3 text-muted-foreground"
        style={{
          left: AXIS_PX,
          top: PLOT_HEIGHT_PX + 2,
          width: plotWidth,
          height: LABEL_ROW_PX - 2,
        }}
      >
        {labels.map((label) => (
          <span key={label.v}>
            <span
              className="absolute -translate-x-1/2 whitespace-nowrap tabular-nums"
              style={{ left: label.x }}
            >
              {label.label}
            </span>
            {label.context && (
              <span
                className="absolute top-3 -translate-x-1/2 whitespace-nowrap"
                style={{ left: label.cx }}
              >
                {label.context}
              </span>
            )}
          </span>
        ))}
      </div>
      {hover && (
        <BucketTooltip
          bar={hover.bar}
          resolutionSeconds={samples.resolutionSeconds}
          left={Math.min(Math.max(0, AXIS_PX + hover.x - 80), width - 216)}
        />
      )}
    </div>
  );
}

function BucketTooltip({
  bar,
  resolutionSeconds,
  left,
}: {
  bar: Bar;
  resolutionSeconds: number;
  left: number;
}): ReactElement {
  const { ts, locale } = useI18n();
  const { bucket } = bar;
  const rows: Array<[string, string]> = [
    [ts('Resolution'), formatResolution(resolutionSeconds)],
    [ts('Executions'), formatExactCount(bucket.count, locale)],
    [ts('Failed'), formatExactCount(bucket.failures, locale)],
    [ts('Min'), formatSpanMs(bucket.minMs)],
    [ts('Median (p50)'), formatSpanMs(bucket.p50Ms)],
    [ts('Slow tail (p95)'), formatSpanMs(bucket.p95Ms)],
    [ts('Max'), formatSpanMs(bucket.maxMs)],
  ];
  return (
    <div
      role="tooltip"
      data-testid="continuous-tooltip"
      className="pointer-events-none absolute z-10 w-52 rounded-md border border-border bg-popover px-2 py-1.5 text-xs text-popover-foreground shadow-md"
      style={{ left, top: 4 }}
    >
      <div className="mb-1 font-medium tabular-nums">
        {formatAbsoluteTime(bar.time) ?? ''}
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 tabular-nums">
        {rows.map(([label, value]) => (
          <div key={label} className="contents">
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="text-end">{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
