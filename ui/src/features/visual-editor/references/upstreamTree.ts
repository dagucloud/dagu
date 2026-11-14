// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReferenceScope } from '../contracts';
import { directParents } from './candidates';

type ScopeStep = ReferenceScope['steps'][number];

/** One step of the dependency tree of an edited step. */
export interface UpstreamNode {
  step: ScopeStep;
  /**
   * The step is listed in full elsewhere in the tree. This entry only names
   * it: it has no children, and the panel shows no outputs for it.
   */
  repeated: boolean;
  /** The direct dependencies of the step, in the order of the scope. */
  children: UpstreamNode[];
}

/**
 * The dependency tree of a step: its direct dependencies, each with its own
 * direct dependencies under it, down to the first step. A step that two
 * branches share is listed in full the first time, in depth-first order, and
 * as a short entry after that, so the tree stays as small as the workflow.
 * Empty for an unknown step.
 */
export function upstreamTree(
  scope: ReferenceScope,
  stepName: string
): UpstreamNode[] {
  const steps = scope.steps ?? [];
  const byName = new Map(steps.map((step) => [step.name, step]));
  if (!byName.has(stepName)) return [];
  const rank = new Map(steps.map((step, index) => [step.name, index]));
  const parentsOf = directParents(scope);
  const inOrder = (names: readonly string[]): ScopeStep[] =>
    names
      .filter((name) => byName.has(name))
      .sort((a, b) => (rank.get(a) ?? 0) - (rank.get(b) ?? 0))
      .map((name) => byName.get(name) as ScopeStep);

  const direct = inOrder(parentsOf(stepName));
  // A step is expanded once, so a cycle ends and a shared step is not repeated.
  const listed = new Set<string>([stepName, ...direct.map((s) => s.name)]);
  const expand = (step: ScopeStep): UpstreamNode => {
    const children = inOrder(parentsOf(step.name)).map(
      (parent): UpstreamNode => {
        if (listed.has(parent.name)) {
          return { step: parent, repeated: true, children: [] };
        }
        listed.add(parent.name);
        return expand(parent);
      }
    );
    return { step, repeated: false, children };
  };
  return direct.map(expand);
}
