// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import type { AIEndpointChoice } from './useEndpointChoices';

/**
 * What the server knows about one model of an endpoint: its name, its display
 * name and the effort levels that it supports. This file is the only place
 * that reads the field `modelInfo` of a choice of `/ai/endpoint-choices`.
 */
export type ModelInfo = components['schemas']['AIModelInfo'];

/** The effort levels that a step can name, from the lowest. */
export const EFFORT_LEVELS: readonly string[] = [
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
];

const NONE: ModelInfo[] = [];

/**
 * The models of a choice with their details, in the order of `models`. A model
 * that `modelInfo` does not describe gives the bare name.
 */
export function modelInfos(
  choice: Pick<AIEndpointChoice, 'models' | 'modelInfo'> | undefined
): ModelInfo[] {
  const names = choice?.models ?? [];
  if (names.length === 0) return NONE;
  const known = new Map(
    (choice?.modelInfo ?? []).map((info) => [info.id, info])
  );
  return names.map((id) => known.get(id) ?? { id });
}

/**
 * The effort levels that a step can offer for a model: the levels that the
 * model supports (in the order of the ladder), or all levels when the model
 * has no info and `fallback` is true. A model with no level gives none.
 */
export function offeredEfforts(
  info: ModelInfo | undefined,
  fallback: boolean
): string[] {
  if (info?.efforts === undefined) return fallback ? [...EFFORT_LEVELS] : [];
  const supported = info.efforts;
  return EFFORT_LEVELS.filter((level) => supported.includes(level));
}
