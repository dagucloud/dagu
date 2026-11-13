// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  parseStoredRange,
  rangeBounds,
  rangeFor,
} from '../dock/runTimelineModel';
import { POLLED_PRESETS, presetView } from '../continuous/sampleBars';
import { presetsFor, RANGE_PRESETS } from '../rangePresets';

const ids = (use: Parameters<typeof presetsFor>[0]) =>
  presetsFor(use).map((p) => p.id);

describe('range presets', () => {
  it('has one list, shortest first, with one entry for each ID', () => {
    const ms = RANGE_PRESETS.map((p) => p.ms);
    expect(ms).toEqual([...ms].sort((a, b) => a - b));
    expect(new Set(RANGE_PRESETS.map((p) => p.id)).size).toBe(
      RANGE_PRESETS.length
    );
  });

  it('keeps the ranges that each use shows today', () => {
    expect(ids('dag')).toEqual(['1h', '6h', '24h', '7d']);
    expect(ids('continuous')).toEqual(['1h', '24h', '7d', '30d', '1y', '3y']);
  });

  it('gives a service DAG one hour to three years', () => {
    expect(ids('service')).toEqual([
      '1h',
      '6h',
      '24h',
      '7d',
      '30d',
      '1y',
      '3y',
    ]);
  });

  // The dock model and the chart read the same entry for an ID.
  it('gives the same window to the timeline and to the chart', () => {
    const now = Date.parse('2026-11-08T12:00:00Z');
    for (const id of ids('service')) {
      const view = presetView(id, now);
      expect(rangeBounds({ preset: id }, now)).toEqual({
        from: view.from,
        to: view.to,
      });
    }
  });

  it('polls the presets that end now and are short', () => {
    expect(POLLED_PRESETS).toEqual(['1h', '6h', '24h']);
  });

  it('keeps a stored preset only where the DAG offers it', () => {
    expect(rangeFor({ preset: '3y' }, true)).toEqual({ preset: '3y' });
    expect(rangeFor({ preset: '3y' }, false)).toEqual({ preset: '24h' });
    expect(rangeFor({ preset: '7d' }, false)).toEqual({ preset: '7d' });
    const custom = { preset: 'custom' as const, from: 1, to: 2 };
    expect(rangeFor(custom, false)).toBe(custom);
  });

  it('reads any preset of the list from the storage', () => {
    expect(parseStoredRange('{"preset":"1y"}')).toEqual({ preset: '1y' });
    expect(parseStoredRange('{"preset":"2w"}')).toEqual({ preset: '24h' });
  });
});
