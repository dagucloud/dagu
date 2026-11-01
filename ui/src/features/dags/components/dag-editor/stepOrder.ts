// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';

type ApiDAGDetails = components['schemas']['DAGDetails'];

/**
 * The step names in execution order, handlers last. The order is a
 * topological sort of the dependency graph from the start steps: a step
 * comes after every step that it depends on, explicitly or through an
 * output reference, steps of the same depth keep the order of the file,
 * and the depth of a step is one more than the depth of its deepest
 * dependency. A dependency that is no step is ignored, and a dependency
 * cycle does not stop the sort.
 */
export function executionOrder(dag: ApiDAGDetails | undefined): string[] {
  const steps = dag?.steps ?? [];
  const known = new Set(steps.map((step) => step.name));
  const depth = new Map<string, number>();
  const visiting = new Set<string>();
  const byName = new Map(steps.map((step) => [step.name, step]));

  const depthOf = (name: string): number => {
    const cached = depth.get(name);
    if (cached !== undefined) return cached;
    // A cycle: the step on it counts from the steps that lead to it.
    if (visiting.has(name)) return 0;
    visiting.add(name);
    let level = 0;
    const step = byName.get(name);
    for (const dependency of [
      ...(step?.depends ?? []),
      ...(step?.inferredDepends ?? []),
    ]) {
      if (known.has(dependency) && dependency !== name) {
        level = Math.max(level, depthOf(dependency) + 1);
      }
    }
    visiting.delete(name);
    depth.set(name, level);
    return level;
  };

  const names = steps
    .map((step, index) => ({
      name: step.name,
      index,
      level: depthOf(step.name),
    }))
    .sort((a, b) => a.level - b.level || a.index - b.index)
    .map((item) => item.name);
  for (const handler of Object.values(dag?.handlerOn ?? {})) {
    if (handler?.name && !names.includes(handler.name)) {
      names.push(handler.name);
    }
  }
  return names;
}

/**
 * The steps before and after a step in execution order: the previous step
 * is the one that runs before, the next step the one that runs after. Every
 * step is reachable, whatever its `depends`.
 */
export function stepNeighbours(
  dag: ApiDAGDetails | undefined,
  name: string | null
): { previous: string | null; next: string | null } {
  const order = executionOrder(dag);
  const index = name === null ? -1 : order.indexOf(name);
  if (index < 0) return { previous: null, next: null };
  return { previous: order[index - 1] ?? null, next: order[index + 1] ?? null };
}
