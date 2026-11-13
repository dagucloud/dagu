// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { AXIS_PX, SamplesChart } from '../SamplesChart';

type StepSamples = components['schemas']['StepSamples'];

const FROM = Date.parse('2026-10-28T00:00:00Z');
const TO = FROM + 3_600_000;
const WIDTH = 644;

const samples: StepSamples = {
  resolutionSeconds: 60,
  from: new Date(FROM).toISOString(),
  to: new Date(TO).toISOString(),
  buckets: [
    {
      time: new Date(FROM + 60_000).toISOString(),
      count: 60,
      failures: 0,
      minMs: 3,
      maxMs: 90,
      p50Ms: 12,
      p95Ms: 40,
    },
  ],
};

function renderChart(onZoom: (factor: number, ratio: number) => void) {
  render(
    <UserPreferencesProvider>
      <I18nProvider>
        <SamplesChart
          samples={samples}
          window={{ from: FROM, to: TO }}
          width={WIDTH}
          selected={null}
          onSelect={() => {}}
          onZoom={onZoom}
          onPan={() => {}}
        />
      </I18nProvider>
    </UserPreferencesProvider>
  );
  const chart = screen.getByTestId('continuous-chart');
  chart.getBoundingClientRect = () =>
    ({ left: 100, top: 0, width: WIDTH, height: 124 }) as DOMRect;
  return chart;
}

describe('SamplesChart wheel zoom', () => {
  // The axis column is at the left of the plot: the ratio is in the plot only.
  it('gives a ratio of 0 at the left edge of the plot and 1 at the right edge', () => {
    const onZoom = vi.fn<(factor: number, ratio: number) => void>();
    const chart = renderChart(onZoom);
    fireEvent.wheel(chart, { deltaY: -100, clientX: 100 + AXIS_PX });
    fireEvent.wheel(chart, { deltaY: -100, clientX: 100 + WIDTH });
    expect(onZoom.mock.calls[0]![1]).toBe(0);
    expect(onZoom.mock.calls[1]![1]).toBe(1);
  });

  it('gives the position of the pointer in the plot', () => {
    const onZoom = vi.fn<(factor: number, ratio: number) => void>();
    const chart = renderChart(onZoom);
    const plot = WIDTH - AXIS_PX;
    fireEvent.wheel(chart, {
      deltaY: -100,
      clientX: 100 + AXIS_PX + plot / 4,
    });
    expect(onZoom.mock.calls[0]![1]).toBeCloseTo(0.25, 10);
  });

  it('clamps a pointer over the axis to 0', () => {
    const onZoom = vi.fn<(factor: number, ratio: number) => void>();
    const chart = renderChart(onZoom);
    fireEvent.wheel(chart, { deltaY: 100, clientX: 100 + 10 });
    expect(onZoom.mock.calls[0]![1]).toBe(0);
  });
});
