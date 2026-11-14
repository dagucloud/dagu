// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';

type Node = components['schemas']['Node'];

/** The nodes of a run that are continuous steps (Node.continuous). */
export function continuousNodes(
  details: { nodes?: readonly Node[] } | null | undefined
): Node[] {
  return (details?.nodes ?? []).filter((node) => node.continuous === true);
}

/**
 * The continuous step that the panel shows: the selected step when it is
 * continuous, else the first continuous step of the run. null when the run
 * has none.
 */
export function pickContinuousNode(
  details: { nodes?: readonly Node[] } | null | undefined,
  step?: string | null
): Node | null {
  const nodes = continuousNodes(details);
  return nodes.find((node) => node.step.name === step) ?? nodes[0] ?? null;
}
