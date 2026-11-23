// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The one list of the time ranges that a time axis offers: the range menu of
// the run timeline and the period buttons of the Continuous tab. Each use
// shows a part of the list.

const HOUR_MS = 60 * 60_000;
const DAY_MS = 24 * HOUR_MS;

/** The presets, shortest first. `label` is a menu text, `short` a button text. */
export const RANGE_PRESETS = [
  { id: '1h', ms: HOUR_MS, label: 'Last hour', short: '1h' },
  { id: '6h', ms: 6 * HOUR_MS, label: 'Last 6 hours', short: '6h' },
  { id: '24h', ms: DAY_MS, label: 'Last 24 hours', short: '24h' },
  { id: '7d', ms: 7 * DAY_MS, label: 'Last 7 days', short: '7d' },
  { id: '30d', ms: 30 * DAY_MS, label: 'Last 30 days', short: '30d' },
  { id: '1y', ms: 365 * DAY_MS, label: 'Last year', short: '1y' },
  { id: '3y', ms: 3 * 365 * DAY_MS, label: 'Last 3 years', short: '3y' },
] as const;

export type PresetId = (typeof RANGE_PRESETS)[number]['id'];
export type RangePreset = (typeof RANGE_PRESETS)[number];

/** The use of a time axis: it decides which presets the user can choose. */
export type RangeUse = 'dag' | 'service' | 'continuous';

const USE_IDS: Record<RangeUse, readonly PresetId[]> = {
  // The runs of a normal DAG: only the retained runs fill the axis.
  dag: ['1h', '6h', '24h', '7d'],
  // The run history of a service DAG goes back three years.
  service: ['1h', '6h', '24h', '7d', '30d', '1y', '3y'],
  continuous: ['1h', '24h', '7d', '30d', '1y', '3y'],
};

/** The presets of a use, shortest first. */
export function presetsFor(use: RangeUse): readonly RangePreset[] {
  return RANGE_PRESETS.filter((p) => USE_IDS[use].includes(p.id));
}

/** The preset of an ID, or undefined for any other text. */
export function presetById(id: unknown): RangePreset | undefined {
  return RANGE_PRESETS.find((p) => p.id === id);
}
