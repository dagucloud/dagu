// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ApiDAGDetails } from '../contracts';
import { parseDagDocLenient, resolveStepNames } from '../doc';
import { canonical, headOf } from './runSpecDiff';

/** A 53-bit hash of a text, in hex. It is not a security function. */
export function hashText(text: string): string {
  let h1 = 0xdeadbeef;
  let h2 = 0x41c6ce57;
  for (let i = 0; i < text.length; i++) {
    const ch = text.charCodeAt(i);
    h1 = Math.imul(h1 ^ ch, 2654435761);
    h2 = Math.imul(h2 ^ ch, 1597334677);
  }
  h1 =
    Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^
    Math.imul(h2 ^ (h2 >>> 13), 3266489909);
  h2 =
    Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^
    Math.imul(h1 ^ (h1 >>> 13), 3266489909);
  return (4294967296 * (2097151 & h2) + (h1 >>> 0)).toString(16);
}

// A reference to the output of another step: ${steps.<id>.outputs.<name>}.
const STEP_REFERENCE = /\$\{steps\.([A-Za-z0-9_-]+)\./g;

// The targets of a router: `with.routes`, or the deprecated top-level
// `routes`. Targets are trimmed, as `buildStepRouter` in internal/spec/step.go
// trims them.
function routeTargets(value: Record<string, unknown>): string[] {
  const withBlock = value.with;
  const table =
    isRecord(withBlock) && isRecord(withBlock.routes)
      ? withBlock.routes
      : isRecord(value.routes)
        ? value.routes
        : {};
  return Object.values(table).flatMap((targets) =>
    (Array.isArray(targets) ? targets : [targets]).flatMap((target) =>
      typeof target === 'string' ? [target.trim()] : []
    )
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

interface Item {
  /** Undefined for a step with no name: it is hashed but not returned. */
  name: string | undefined;
  id: string | undefined;
  canon: string;
  written: readonly string[];
  /** Router targets as written: each one needs this step. */
  routes: readonly string[];
  /** The index of its entry in the steps list (see `entries`). */
  entry: number;
}

// The rows of a panel ask for the hashes of the same few texts: one parse
// serves them all.
const RECENT_SIZE = 8;
const recent: {
  text: string;
  details: ApiDAGDetails | undefined;
  hashes: ReadonlyMap<string, string>;
}[] = [];

/**
 * A hash of each step of a definition, by step name. The hash covers the
 * parsed step (comments and spacing do not count), the head of the
 * definition (`headOf`: the top-level keys other than the steps, and the
 * later documents), and the hashes of the steps that it needs: its
 * `depends`, the routers that route to it, the steps whose outputs it
 * references, and in a chain the steps of the entry before it (one step, or
 * a parallel group). A change of a step, or of a step upstream of it, gives it
 * another hash. A step that has no name stays out of the result. A text that
 * cannot be read gives an empty map, so nothing counts as fresh. Calls with
 * the same text and details share one result.
 */
export function stepHashes(
  text: string,
  details?: ApiDAGDetails
): ReadonlyMap<string, string> {
  const known = recent.find(
    (entry) => entry.text === text && entry.details === details
  );
  if (known) return known.hashes;
  const hashes = computeStepHashes(text, details);
  recent.unshift({ text, details, hashes });
  if (recent.length > RECENT_SIZE) recent.pop();
  return hashes;
}

function computeStepHashes(
  text: string,
  details: ApiDAGDetails | undefined
): Map<string, string> {
  const out = new Map<string, string>();
  const model = parseDagDocLenient(text);
  if (!model.ok) return out;
  const head = headOf(model);
  if (head === null) return out;
  const names = resolveStepNames(model, details).byKey;

  const items: Item[] = [];
  // The entries of the steps list: one step, or the steps of a parallel group.
  const entries: Item[][] = [];
  model.steps.forEach((step, index) => {
    const group = step.parallelGroup;
    if (
      group === undefined ||
      model.steps[index - 1]?.parallelGroup !== group
    ) {
      entries.push([]);
    }
    // A step with no name still links the steps around it in a chain.
    const name = names[step.key] ?? step.explicitName ?? step.id;
    const item: Item = {
      name,
      id: step.id,
      canon: canonical(step.value) + (step.broken ? `!${step.broken}` : ''),
      written: step.depends,
      routes: step.kind === 'router' ? routeTargets(step.value) : [],
      entry: entries.length - 1,
    };
    items.push(item);
    entries[item.entry]?.push(item);
  });
  const byName = new Map(
    items.flatMap((item) =>
      item.name !== undefined ? [[item.name, item] as const] : []
    )
  );
  const byId = new Map(
    items.flatMap((item) => (item.id ? [[item.id, item] as const] : []))
  );
  const lookup = (ref: string): Item | undefined =>
    byName.get(ref) ?? byId.get(ref);

  const needs = new Map<Item, Set<Item>>(
    items.map((item) => [item, new Set()])
  );
  for (const item of items) {
    const found = needs.get(item) as Set<Item>;
    for (const ref of item.written) {
      const target = lookup(ref);
      if (target && target !== item) found.add(target);
    }
    for (const match of item.canon.matchAll(STEP_REFERENCE)) {
      const target = lookup(match[1] as string);
      if (target && target !== item) found.add(target);
    }
    if (model.dagType === 'chain' && item.written.length === 0) {
      for (const previous of entries[item.entry - 1] ?? []) found.add(previous);
    }
    // `transformRouterSteps` in internal/spec/dag.go makes each target of a
    // router depend on it.
    for (const ref of item.routes) {
      const target = byName.get(ref);
      if (target && target !== item) needs.get(target)?.add(item);
    }
  }

  const memo = new Map<Item, string>();
  const visiting = new Set<Item>();
  const hashOf = (item: Item): string => {
    const known = memo.get(item);
    if (known !== undefined) return known;
    // A cycle is a fault of the definition: it hashes to a fixed text.
    if (visiting.has(item)) return 'cycle';
    visiting.add(item);
    const upstream = [...(needs.get(item) ?? [])].map(hashOf).sort();
    visiting.delete(item);
    const hash = hashText([head, item.canon, ...upstream].join('\u0000'));
    memo.set(item, hash);
    return hash;
  };
  for (const item of items) {
    if (item.name !== undefined) out.set(item.name, hashOf(item));
  }
  return out;
}
