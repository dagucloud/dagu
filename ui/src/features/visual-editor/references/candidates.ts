// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  ReferenceCandidate,
  ReferenceGroup,
  ReferenceScope,
} from '../contracts';

/** The order of the groups in a candidate list. */
export const REFERENCE_GROUP_ORDER: readonly ReferenceGroup[] = [
  'steps',
  'params',
  'consts',
  'env',
  'secrets',
  'context',
];

/** English i18n key of each group title. The picker and the YAML hover show it. */
export const REFERENCE_GROUP_LABELS: Readonly<Record<ReferenceGroup, string>> =
  {
    steps: 'Step outputs',
    params: 'Parameters',
    consts: 'Constants',
    env: 'Environment variables',
    secrets: 'Secrets',
    context: 'Run context',
  };

type VariableGroup = 'params' | 'consts' | 'env' | 'secrets';

// The token of a variable is the server `reference`. The client never builds it.
function variableCandidates(
  scope: ReferenceScope,
  group: VariableGroup
): ReferenceCandidate[] {
  return (scope[group] ?? []).map((variable) => {
    const detail =
      variable.description ||
      (group === 'secrets' || !variable.defaultValue
        ? undefined
        : `= ${variable.defaultValue}`);
    return {
      token: variable.reference,
      label: variable.name,
      group,
      detail,
      known: 'static',
    };
  });
}

// Names of all steps that `start` reaches through `upstream` lists. A cycle ends the walk.
function closureOf(
  upstreamOf: Map<string, string[]>,
  start: string
): Set<string> {
  const seen = new Set<string>();
  const stack = [...(upstreamOf.get(start) ?? [])];
  while (stack.length > 0) {
    const name = stack.pop() as string;
    if (seen.has(name)) continue;
    seen.add(name);
    stack.push(...(upstreamOf.get(name) ?? []));
  }
  return seen;
}

/** The upstream lists of a scope: all ancestors, and the direct parents of a step. */
function parentIndex(scope: ReferenceScope): {
  has(name: string): boolean;
  closure(name: string): Set<string>;
  parentsOf(name: string): string[];
} {
  const upstreamOf = new Map<string, string[]>();
  for (const step of scope.steps ?? [])
    upstreamOf.set(step.name, step.upstream);

  // The server may list all ancestors in `upstream`, not only the direct parents.
  // A listed step that is an ancestor of another listed step is not a direct parent.
  // The walk uses these reduced parents, so the distance is the same for both shapes.
  const closures = new Map<string, Set<string>>();
  const closure = (name: string): Set<string> => {
    let set = closures.get(name);
    if (!set) {
      set = closureOf(upstreamOf, name);
      closures.set(name, set);
    }
    return set;
  };
  const parentsOf = (name: string): string[] => {
    const listed = [...new Set(upstreamOf.get(name) ?? [])].filter(
      (x) => x !== name
    );
    const direct = listed.filter(
      (x) => !listed.some((y) => y !== x && closure(y).has(x))
    );
    // Only a cycle can remove every parent. Keep the listed ones then.
    return direct.length > 0 ? direct : listed;
  };
  return { has: (name) => upstreamOf.has(name), closure, parentsOf };
}

/**
 * The direct parents of each step of a scope, by step name: the listed
 * upstream steps that no other listed step has as an ancestor. The names are
 * in the order of the list of the server.
 */
export function directParents(
  scope: ReferenceScope
): (name: string) => string[] {
  return parentIndex(scope).parentsOf;
}

/**
 * Ancestors of the step (by step name) with their distance: 1 = direct parent.
 * Empty map for an unknown step.
 */
export function upstreamDistances(
  scope: ReferenceScope,
  stepName: string
): Map<string, number> {
  const distances = new Map<string, number>();
  const { has, closure, parentsOf } = parentIndex(scope);
  if (!has(stepName)) return distances;

  let frontier = [stepName];
  const visited = new Set<string>([stepName]);
  for (let distance = 1; frontier.length > 0; distance++) {
    const next: string[] = [];
    for (const name of frontier) {
      for (const parent of parentsOf(name)) {
        if (visited.has(parent)) continue;
        visited.add(parent);
        distances.set(parent, distance);
        next.push(parent);
      }
    }
    frontier = next;
  }
  // A cycle can hide an ancestor from the walk. Put it after the others.
  const last = Math.max(0, ...distances.values()) + 1;
  for (const name of closure(stepName)) {
    if (name !== stepName && !distances.has(name)) distances.set(name, last);
  }
  return distances;
}

// Fixed field names of spec 017. They are the only tokens that the client builds.
const CONTEXT_FIELDS = [
  'dag.name',
  'run.id',
  'run.status',
  'run.scheduled_at',
  'run.root_name',
  'run.root_id',
  'attempt.id',
  'attempt.started_at',
  'trigger.type',
  'trigger.actor',
  'paths.log_file',
  'paths.work_dir',
  'paths.artifacts_dir',
] as const;

// Fields that exist only for the current step.
const CONTEXT_STEP_FIELDS = [
  'step.id',
  'step.name',
  'paths.step_stdout_file',
  'paths.step_stderr_file',
  'paths.step_output_file',
] as const;

function contextCandidates(stepName: string | null): ReferenceCandidate[] {
  const fields =
    stepName === null
      ? CONTEXT_FIELDS
      : [...CONTEXT_FIELDS, ...CONTEXT_STEP_FIELDS];
  return fields.map(
    (field): ReferenceCandidate => ({
      token: `\${context.${field}}`,
      label: field,
      group: 'context',
      known: 'runtime',
    })
  );
}

function stepCandidates(
  scope: ReferenceScope,
  stepName: string
): ReferenceCandidate[] {
  const distances = upstreamDistances(scope, stepName);
  return (scope.steps ?? [])
    .map((step, index) => ({ step, index, distance: distances.get(step.name) }))
    .filter(
      (entry): entry is typeof entry & { distance: number } =>
        entry.distance !== undefined
    )
    .sort((a, b) => a.distance - b.distance || a.index - b.index)
    .flatMap(({ step, distance }) =>
      step.outputs.map(
        (output): ReferenceCandidate => ({
          token: output.reference,
          label: `${step.id || step.name}.${output.name}`,
          group: 'steps',
          detail:
            [output.type, output.source].filter(Boolean).join(' \u00b7 ') ||
            undefined,
          stepName: step.name,
          distance,
          known: step.outputsKnown,
        })
      )
    );
}

/**
 * Candidates for a step input, in the order of REFERENCE_GROUP_ORDER.
 * stepName is the step that owns the field, or null when there is no owner step.
 * Only an owner step sees step outputs and the context of a step.
 * Without a scope, the list holds the context candidates only.
 */
export function buildReferenceCandidates(
  scope: ReferenceScope | undefined,
  stepName: string | null
): ReferenceCandidate[] {
  if (!scope) return contextCandidates(stepName);
  return [
    ...(stepName === null ? [] : stepCandidates(scope, stepName)),
    ...variableCandidates(scope, 'params'),
    ...variableCandidates(scope, 'consts'),
    ...variableCandidates(scope, 'env'),
    ...variableCandidates(scope, 'secrets'),
    ...contextCandidates(stepName),
  ];
}

/** Case-insensitive match of every space-separated word of query against token, label and detail. Keeps order. */
export function filterReferenceCandidates(
  candidates: ReferenceCandidate[],
  query: string
): ReferenceCandidate[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return candidates;
  return candidates.filter((c) => {
    const haystack = [c.token, c.label, c.detail ?? '']
      .join('\n')
      .toLowerCase();
    return words.every((word) => haystack.includes(word));
  });
}
