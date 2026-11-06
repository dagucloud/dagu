// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  DagDocModel,
  ReferenceCandidate,
  ReferenceGroup,
  ReferenceScope,
  StepView,
} from '../contracts';
import { publishCandidates } from './publishOutput';

/** The order of the groups in a candidate list. */
export const REFERENCE_GROUP_ORDER: readonly ReferenceGroup[] = [
  'foreach',
  'inputs',
  'outputs',
  'steps',
  'params',
  'consts',
  'stepEnv',
  'env',
  'secrets',
  'context',
];

/** English i18n key of each group title. The picker and the YAML hover show it. */
export const REFERENCE_GROUP_LABELS: Readonly<Record<ReferenceGroup, string>> =
  {
    foreach: 'Current item',
    inputs: 'Build inputs',
    outputs: 'Build outputs',
    steps: 'Step outputs',
    params: 'Parameters',
    consts: 'Constants',
    stepEnv: 'Step environment',
    env: 'Environment variables',
    secrets: 'Secrets',
    context: 'Run context',
  };

/** English i18n keys of what the lists say of a value that is not safe in a shell. */
export const SHELL_RISK_LABEL = 'Not safe in a shell';
/** The label with what to do, for a row that has no room for the note. */
export const SHELL_RISK_ROW = 'Unsafe here: use env';
export const SHELL_RISK_NOTE =
  'A model wrote this text, so a shell can run it as code. Pass it through env and read it from the environment instead.';

type ScopeStep = ReferenceScope['steps'][number];
type ScopeVariable = ReferenceScope['params'][number];
type ScopeForeach = NonNullable<ScopeStep['foreach']>;
type StepsOnly = Pick<ReferenceScope, 'steps'>;

// The token of a variable is the server `reference`. The client never builds it.
function variableCandidates(
  variables: ScopeVariable[] | undefined,
  group: ReferenceGroup,
  known: ReferenceCandidate['known'] = 'static'
): ReferenceCandidate[] {
  return (variables ?? []).map((variable) => {
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
      known,
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
function parentIndex(scope: StepsOnly): {
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
export function directParents(scope: StepsOnly): (name: string) => string[] {
  return parentIndex(scope).parentsOf;
}

/**
 * Ancestors of the step (by step name) with their distance: 1 = direct parent.
 * Empty map for an unknown step.
 */
export function upstreamDistances(
  scope: StepsOnly,
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
  'step.repeat_index',
  'paths.step_stdout_file',
  'paths.step_stderr_file',
  'paths.step_output_file',
] as const;

/** One foreach around a field, for `foreach.key`, `foreach.collect` or the item body. */
export interface ForeachFrame {
  foreach: ScopeForeach;
  field: 'key' | 'collect' | 'body';
  /** The body step that holds the field, when the scope knows it. */
  bodyStep?: ScopeStep;
}

/** What the document says of the field, beyond the scope. */
export interface ReferenceOwnerOptions {
  /** The step entry declares `repeat_policy`. */
  repeats?: boolean;
  /** The foreach bodies around the field, the outermost first. */
  frames?: readonly ForeachFrame[];
  /** The document, for outputs that a pick can make a step publish. Leave it out where nothing can edit. */
  doc?: DagDocModel;
}

/** True for a step entry that declares `repeat_policy`. */
export function declaresRepeat(step: StepView | undefined): boolean {
  return step?.value.repeat_policy !== undefined;
}

/**
 * The foreach frames of a field of `step`. `path` is the YAML path below the
 * step entry, such as ['foreach', 'steps', '0', 'run'].
 */
export function foreachFramesAt(
  step: ScopeStep,
  path: readonly string[]
): ForeachFrame[] {
  const frames: ForeachFrame[] = [];
  let current: ScopeStep | undefined = step;
  let at = 0;
  while (path[at] === 'foreach' && current?.foreach) {
    const foreach: ScopeForeach = current.foreach;
    const field = path[at + 1];
    if (field === 'key' || field === 'collect') {
      frames.push({ foreach, field });
      break;
    }
    const index = path[at + 2];
    if (field !== 'steps' || index === undefined) break;
    // A new body step is in the text before the scope comes back with it.
    current = foreach.steps[Number(index)];
    frames.push({ foreach, field: 'body', bodyStep: current });
    at += 3;
  }
  return frames;
}

// Only the item of the innermost foreach is in scope: an inner item replaces an
// outer one. `foreach.key` computes the key, which reads as empty there.
function itemCandidates(frames: readonly ForeachFrame[]): ReferenceCandidate[] {
  const frame = frames[frames.length - 1];
  if (!frame) return [];
  return variableCandidates(frame.foreach.item, 'foreach', 'runtime').filter(
    (candidate) => frame.field !== 'key' || candidate.label !== 'key'
  );
}

function contextCandidates(
  owner: ScopeStep | undefined,
  hasOwner: boolean,
  { repeats = false }: ReferenceOwnerOptions
): ReferenceCandidate[] {
  const handler = owner?.stepPath.startsWith('handler_on.') ?? false;
  // Spec 017: these fields are missing outside the scopes named here.
  const missing = new Set<string>();
  if (!handler) missing.add('run.status');
  if (handler || (owner && !owner.id)) missing.add('step.id');
  if (handler || !(repeats || owner?.repeats)) missing.add('step.repeat_index');
  const fields: readonly string[] = hasOwner
    ? [...CONTEXT_FIELDS, ...CONTEXT_STEP_FIELDS]
    : CONTEXT_FIELDS;
  return fields
    .filter((field) => !missing.has(field))
    .map(
      (field): ReferenceCandidate => ({
        token: `\${context.${field}}`,
        label: field,
        group: 'context',
        known: 'runtime',
      })
    );
}

function outputCandidates(
  step: ScopeStep,
  distance: number
): ReferenceCandidate[] {
  return step.outputs.map(
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
      ...(output.modelOutput ? { modelOutput: true } : {}),
    })
  );
}

function stepCandidates(
  scope: StepsOnly,
  stepName: string,
  doc?: DagDocModel
): ReferenceCandidate[] {
  const distances = upstreamDistances(scope, stepName);
  return (scope.steps ?? [])
    .map((step, index) => ({ step, index, distance: distances.get(step.name) }))
    .filter(
      (entry): entry is typeof entry & { distance: number } =>
        entry.distance !== undefined
    )
    .sort((a, b) => a.distance - b.distance || a.index - b.index)
    .flatMap(({ step, distance }) => [
      ...outputCandidates(step, distance),
      ...publishCandidates(step, distance, doc),
    ]);
}

// The outputs that one foreach body gives a field inside it: the ancestors of
// the body step, or every body step in `collect`, which runs after the body.
function frameOutputCandidates(frame: ForeachFrame): ReferenceCandidate[] {
  const { foreach, field, bodyStep } = frame;
  if (field === 'collect') {
    return foreach.steps.flatMap((step) => outputCandidates(step, 1));
  }
  return bodyStep ? stepCandidates(foreach, bodyStep.name) : [];
}

/**
 * Candidates for a step input, in the order of REFERENCE_GROUP_ORDER.
 * stepName is the step that owns the field, or null when there is no owner step.
 * Only an owner step sees step outputs and the context of a step. A field in a
 * foreach body belongs to its body step, and sees the outputs nearest to it
 * first. Context fields that spec 017 limits to a scope appear only in that
 * scope. Without a scope, the list holds the context candidates only.
 */
export function buildReferenceCandidates(
  scope: ReferenceScope | undefined,
  stepName: string | null,
  options: ReferenceOwnerOptions = {}
): ReferenceCandidate[] {
  const top = scope?.steps?.find((step) => step.name === stepName);
  const frames = options.frames ?? [];
  const bodyStep = frames[frames.length - 1]?.bodyStep;
  const owner = bodyStep ?? top;
  const context = contextCandidates(owner, stepName !== null, options);
  if (!scope) return context;
  const rootEnv = variableCandidates(scope.env, 'env');
  const rootTokens = new Set(rootEnv.map((candidate) => candidate.token));
  const stepEnv = variableCandidates(owner?.env, 'stepEnv').map(
    (candidate): ReferenceCandidate =>
      rootTokens.has(candidate.token)
        ? { ...candidate, alsoRoot: true }
        : candidate
  );
  const stepTokens = new Set(stepEnv.map((candidate) => candidate.token));
  return [
    ...itemCandidates(frames),
    ...variableCandidates(owner?.inputs, 'inputs', 'runtime'),
    ...variableCandidates(owner?.pathOutputs, 'outputs', 'runtime'),
    ...[...frames].reverse().flatMap(frameOutputCandidates),
    ...(stepName === null ? [] : stepCandidates(scope, stepName, options.doc)),
    ...variableCandidates(scope.params, 'params'),
    ...variableCandidates(scope.consts, 'consts'),
    ...stepEnv,
    ...rootEnv.filter((candidate) => !stepTokens.has(candidate.token)),
    ...variableCandidates(scope.secrets, 'secrets'),
    ...context,
  ];
}

/**
 * The candidates of a step env entry (spec 006). `declaredFromHere` holds the
 * names that this entry and the later ones declare: they do not exist yet, so
 * such a name reads the root value when the root env has it. Everything else
 * stays, including what the step inherits from `defaults`.
 */
export function envEntryCandidates(
  candidates: ReferenceCandidate[],
  declaredFromHere: ReadonlySet<string>
): ReferenceCandidate[] {
  return candidates.flatMap((candidate): ReferenceCandidate[] => {
    if (candidate.group !== 'stepEnv' || !declaredFromHere.has(candidate.label))
      return [candidate];
    return candidate.alsoRoot
      ? [{ ...candidate, group: 'env', alsoRoot: undefined }]
      : [];
  });
}

/**
 * The candidates of a field that a shell reads: a model's reply is marked,
 * because Dagu does not escape it and a shell can run it as code (spec 067).
 */
export function forShellField(
  candidates: ReferenceCandidate[]
): ReferenceCandidate[] {
  return candidates.map((candidate) =>
    candidate.modelOutput ? { ...candidate, shellRisk: true } : candidate
  );
}

/** The candidates of a field outside the command, as a build output path exists only there (spec 033). */
export function withoutBuildOutputs(
  candidates: ReferenceCandidate[]
): ReferenceCandidate[] {
  return candidates.filter((candidate) => candidate.group !== 'outputs');
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
