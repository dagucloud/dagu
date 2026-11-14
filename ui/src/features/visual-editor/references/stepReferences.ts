// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { findReferenceTokens } from './tokens';

/** What the scan needs to know of a step of the workflow. */
export interface ReferableStep {
  name: string;
  id?: string | null;
  /** The output variable of the step: `output: ORDERS` makes ${ORDERS}. */
  output?: string | null;
}

/**
 * The steps whose outputs a text reads, in the order of `steps`. The scan
 * finds the same tokens as the editor: `${steps.<id>.outputs.<name>}`, the
 * older `${<id>.outputs.<name>}` and `${<id>.stdout}`, and `${<VARIABLE>}`
 * or `${<VARIABLE>.<path>}` for the output variable of a step. A token is
 * matched by the id of a step, then by its name. A token that no step owns
 * is ignored, and so is a reference to the step `self`.
 */
export function referencedStepNames(
  steps: ReadonlyArray<ReferableStep> | undefined | null,
  self: string,
  text: string
): string[] {
  if (!steps || text === '') return [];
  const byId = new Map<string, string>();
  const byName = new Map<string, string>();
  const byVariable = new Map<string, string>();
  for (const step of steps) {
    if (step.id) byId.set(step.id, step.name);
    byName.set(step.name, step.name);
    if (step.output) byVariable.set(step.output, step.name);
  }
  const owner = (key: string): string | undefined =>
    byId.get(key) ?? byName.get(key);
  const found = new Set<string>();
  for (const token of findReferenceTokens(text)) {
    const parts = token.path.split('.');
    let name: string | undefined;
    if (token.kind === 'canonical') {
      if (token.namespace === 'steps') name = owner(parts[1] ?? '');
    } else if (token.kind === 'variable') {
      // An alias: the variable stands for the output of its step.
      name = byVariable.get(token.path);
    } else if (token.kind === 'legacy') {
      const head = parts[0] ?? '';
      name = owner(head) ?? byVariable.get(head);
    }
    if (name !== undefined && name !== self) found.add(name);
  }
  return steps.map((s) => s.name).filter((name) => found.has(name));
}
