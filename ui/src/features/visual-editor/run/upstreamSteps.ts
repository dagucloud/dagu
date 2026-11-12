// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

interface StepLike {
  name: string;
  depends?: string[] | null;
  /** Steps whose outputs the step reads: they are upstream steps too. */
  inferredDepends?: string[] | null;
}

function needsOf(step: StepLike | undefined): string[] {
  return [...(step?.depends ?? []), ...(step?.inferredDepends ?? [])];
}

/**
 * The steps that a step needs, transitively. 'file' keeps the order of the
 * file; 'dependency' puts a step after the steps that it needs, the farthest
 * first, and keeps the order of the file between steps that do not depend on
 * each other.
 */
export function upstreamStepNames(
  steps: ReadonlyArray<StepLike> | undefined | null,
  step: string,
  order: 'file' | 'dependency' = 'file'
): string[] {
  if (!steps) return [];
  const byName = new Map(steps.map((s) => [s.name, s]));
  const needed = new Set<string>();
  const visit = (name: string) => {
    for (const dep of needsOf(byName.get(name))) {
      if (needed.has(dep) || !byName.has(dep)) continue;
      needed.add(dep);
      visit(dep);
    }
  };
  visit(step);
  needed.delete(step);
  return arranged(steps, byName, needed, order);
}

/**
 * The steps, and the steps that they need, in dependency order. A step in
 * `exclude` is not in the list, and its own needs are not listed for it.
 */
export function stepsWithUpstream(
  steps: ReadonlyArray<StepLike> | undefined | null,
  names: readonly string[],
  exclude: string
): string[] {
  if (!steps) return [];
  const byName = new Map(steps.map((s) => [s.name, s]));
  const needed = new Set<string>();
  const visit = (name: string) => {
    if (needed.has(name) || name === exclude || !byName.has(name)) return;
    needed.add(name);
    for (const dep of needsOf(byName.get(name))) visit(dep);
  };
  names.forEach(visit);
  return arranged(steps, byName, needed, 'dependency');
}

/**
 * The previous steps of the steps that a run executed: the steps that they
 * need, transitively, without the executed steps themselves. A step after
 * them, or on another branch, is not in the result.
 */
export function previousOfExecuted(
  steps: ReadonlyArray<StepLike> | undefined | null,
  executed: readonly string[]
): Set<string> {
  const previous = new Set(stepsWithUpstream(steps, executed, ''));
  for (const name of executed) previous.delete(name);
  return previous;
}

function arranged(
  steps: ReadonlyArray<StepLike>,
  byName: Map<string, StepLike>,
  needed: Set<string>,
  order: 'file' | 'dependency'
): string[] {
  const inFile = steps.map((s) => s.name).filter((name) => needed.has(name));
  if (order === 'file') return inFile;
  // Depth first, in the order of the file: a step comes after its needs.
  const sorted: string[] = [];
  const done = new Set<string>();
  const place = (name: string) => {
    if (done.has(name)) return;
    done.add(name);
    for (const dep of needsOf(byName.get(name))) {
      if (needed.has(dep)) place(dep);
    }
    sorted.push(name);
  };
  inFile.forEach(place);
  return sorted;
}
