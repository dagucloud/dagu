// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CST, Lexer } from 'yaml';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type {
  DagDocModel,
  JSONSchema,
  ReferenceScope,
  StepKey,
  StepNameMap,
  StepView,
  ValidationIssue,
} from '../contracts';
import { builtinActionNames } from '../inspector/actionRules';
import { checkStepSchemaErrors } from '../inspector/stepSchemaCheck';
import { nextRuns } from '../launchers/nextRuns';
import { findReferenceTokens } from '../references/tokens';
import { NO_ID_MESSAGE, OUTPUTS_ID_MESSAGE } from './issueMessages';
import { locateIssue, rangeFromOffsets } from './issues';
import { validateStepId, validateStepName } from './names';
import { readSchedule } from './ops/schedule';

/*
 * The server validation stops at the first error of the file. This module
 * is the second pass: it never stops at the first error, so that the file
 * shows every problem that it can find without the server. Every check here
 * is one that the server makes too, or one that cannot block a valid file:
 * the checks that may be wrong are warnings.
 */

/** The messages of the issues that this module makes, for the merge. */
export const EMPTY_ACTION_MESSAGE = 'Action is empty.';
export const NO_COMMAND_MESSAGE = 'The step has no action and no command.';
export { NO_ID_MESSAGE, OUTPUTS_ID_MESSAGE } from './issueMessages';
export {
  mergeQuickFixes,
  planQuickFixes,
  planQuickFixesInRange,
  quickFixesForMarker,
} from './quickFixes';
export type { QuickFix } from './quickFixes';

const EXECUTION_KEYS = [
  'run',
  'command',
  'script',
  'exec',
  'action',
  'call',
  'parallel',
  'type',
  'foreach',
  'routes',
];

const SCHEMA_ERROR_KEYWORDS = new Set(['required', 'type', 'enum', 'const']);
// Wrappers of the rules below them: their children say what is wrong.
const WRAPPER_KEYWORDS = new Set([
  'anyOf',
  'oneOf',
  'allOf',
  'not',
  'if',
  'then',
  'else',
]);

// The server names a step without name and id `<type>_<n>`
// (generateTypedStepName in internal/spec/builder.go).
const SERVER_NAME = /_\d+$/;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isBlank(value: unknown): boolean {
  return value === null || (typeof value === 'string' && value.trim() === '');
}

function everyStep(model: DagDocModel): StepView[] {
  return [...model.steps, ...model.handlers];
}

/** The field path of a schema path inside a step: `/with/url` gives `steps[1].with.url`. */
function fieldPathIn(
  key: StepKey,
  instancePath: string,
  property?: string
): string {
  const tokens = instancePath
    .split('/')
    .slice(1)
    .map((raw) => raw.replace(/~1/g, '/').replace(/~0/g, '~'));
  if (property !== undefined) tokens.push(property);
  return tokens.reduce(
    (path, token) =>
      /^\d+$/.test(token) ? `${path}[${token}]` : `${path}.${token}`,
    key
  );
}

function valueAtPath(value: unknown, instancePath: string): unknown {
  let node = value;
  for (const raw of instancePath.split('/').slice(1)) {
    const token = raw.replace(/~1/g, '/').replace(/~0/g, '~');
    if (Array.isArray(node)) node = node[Number(token)];
    else if (isRecord(node)) node = node[token];
    else return undefined;
  }
  return node;
}

interface Draft {
  step: StepView | null;
  severity: 'error' | 'warning';
  message: string;
  fieldPath?: string;
  /** The offset to put the issue at, when no field gives a place. */
  offset?: number;
}

/** The names that the `actions:` block of the file declares. */
function localActionNames(model: DagDocModel): string[] {
  const names: string[] = [];
  let inside = false;
  for (const line of model.text.split('\n')) {
    if (/^actions\s*:/.test(line)) {
      inside = true;
      continue;
    }
    if (!inside) continue;
    if (/^\S/.test(line)) break;
    const key = /^ {1,4}([A-Za-z0-9_][A-Za-z0-9._-]*)\s*:/.exec(line)?.[1];
    if (key) names.push(key);
  }
  return names;
}

/** What the checks of the file read besides the steps. */
export interface ClientIssueInput {
  model: DagDocModel;
  /** The DAG schema of the editor, with the custom actions; no schema skips the schema checks. */
  schema: JSONSchema | null;
  /** The server names of the steps, for `stepName`. */
  names: StepNameMap;
  /** The values that the server found; without it the references use the text. */
  scope?: ReferenceScope;
  /** The names of the custom actions: those of the base config and of the file. */
  customActions?: string[];
}

function schemaDrafts(step: StepView, schema: JSONSchema): Draft[] {
  const drafts: Draft[] = [];
  const errors = checkStepSchemaErrors(schema, step.value);
  const specific = errors.filter((e) => !WRAPPER_KEYWORDS.has(e.keyword));
  const seen = new Set<string>();
  for (const error of specific.length > 0 ? specific : errors) {
    const actual = valueAtPath(step.value, error.instancePath);
    // A `${...}` value is read when the DAG runs: it fits any type.
    if (
      error.keyword !== 'required' &&
      error.keyword !== 'additionalProperties' &&
      typeof actual === 'string' &&
      actual.includes('$')
    ) {
      continue;
    }
    const fieldPath = fieldPathIn(step.key, error.instancePath, error.property);
    const signature = `${error.keyword}|${fieldPath}`;
    if (seen.has(signature)) continue;
    seen.add(signature);
    const severity = SCHEMA_ERROR_KEYWORDS.has(error.keyword)
      ? 'error'
      : 'warning';
    if (error.keyword === 'required') {
      drafts.push({
        step,
        severity,
        message: `${error.property ?? 'The field'} is required.`,
        fieldPath,
      });
    } else if (error.keyword === 'additionalProperties') {
      drafts.push({
        step,
        severity,
        message: `Unknown field "${error.property ?? ''}".`,
        fieldPath,
      });
    } else {
      const where = fieldPath.slice(step.key.length + 1) || 'The step';
      drafts.push({
        step,
        severity,
        message: `${where} ${error.message}.`,
        fieldPath,
      });
    }
  }
  return drafts;
}

// Names that the schema accepts by their form, not by a list: `redis.<command>`
// and the references to a remote action (`name@version`, `owner/repo@version`,
// `source:...`).
const OPEN_ACTION_NAME = /^(?:redis\.[A-Za-z][A-Za-z0-9_-]*|.*@.+|source:.+)$/;

function actionDrafts(step: StepView, known: Set<string> | null): Draft[] {
  const { value } = step;
  const action = value.action;
  if (
    known &&
    typeof action === 'string' &&
    !isBlank(action) &&
    !action.includes('$') &&
    !known.has(action) &&
    !OPEN_ACTION_NAME.test(action)
  ) {
    return [
      {
        step,
        severity: 'error',
        message: `Unknown action "${action}".`,
        fieldPath: `${step.key}.action`,
      },
    ];
  }
  if ('action' in value && isBlank(value.action)) {
    return [
      {
        step,
        severity: 'error',
        message: EMPTY_ACTION_MESSAGE,
        fieldPath: `${step.key}.action`,
      },
    ];
  }
  if (!EXECUTION_KEYS.some((key) => key in value) && step.kind === 'other') {
    return [{ step, severity: 'warning', message: NO_COMMAND_MESSAGE }];
  }
  return [];
}

// The server checks the names and ids of the steps only: a handler takes
// the name of its event.
function identityDrafts(model: DagDocModel): Draft[] {
  const drafts: Draft[] = [];
  const steps = model.steps.filter((step) => step.broken === undefined);
  const names = new Map<string, StepView>();
  const ids = new Map<string, StepView>();
  for (const step of steps) {
    if (step.explicitName !== undefined) {
      const problem = validateStepName(step.explicitName);
      if (problem) {
        drafts.push({
          step,
          severity: 'error',
          message: problem,
          fieldPath: `${step.key}.name`,
        });
      }
      const first = names.get(step.explicitName);
      if (first) {
        drafts.push({
          step,
          severity: 'error',
          message: `Duplicate step name "${step.explicitName}".`,
          fieldPath: `${step.key}.name`,
        });
      } else {
        names.set(step.explicitName, step);
      }
    }
    if (step.id !== undefined) {
      const problem = validateStepId(step.id);
      if (problem) {
        drafts.push({
          step,
          severity: 'error',
          message: problem,
          fieldPath: `${step.key}.id`,
        });
      }
      if (ids.has(step.id)) {
        drafts.push({
          step,
          severity: 'error',
          message: `Duplicate step ID "${step.id}".`,
          fieldPath: `${step.key}.id`,
        });
      } else {
        ids.set(step.id, step);
      }
    }
  }
  for (const [id, owner] of ids) {
    const clash = names.get(id);
    if (clash && clash !== owner) {
      drafts.push({
        step: clash,
        severity: 'error',
        message: `The step name "${id}" is the ID of another step.`,
        fieldPath: `${clash.key}.name`,
      });
    }
  }
  // Steps with an id next to a step without one: the step cannot be
  // referenced by id, and renaming it breaks its references.
  if (ids.size > 0) {
    for (const step of model.steps) {
      if (step.id === undefined && step.broken === undefined) {
        drafts.push({
          step,
          severity: 'warning',
          message: NO_ID_MESSAGE,
          fieldPath:
            step.explicitName === undefined ? undefined : `${step.key}.name`,
        });
      }
    }
  }
  for (const step of model.steps) {
    if (
      step.broken === undefined &&
      step.id === undefined &&
      'outputs' in step.value
    ) {
      drafts.push({
        step,
        severity: 'error',
        message: OUTPUTS_ID_MESSAGE,
        fieldPath: `${step.key}.outputs`,
      });
    }
  }
  return drafts;
}

function dependencyDrafts(model: DagDocModel): Draft[] {
  const known = new Set<string>();
  for (const step of model.steps) {
    if (step.explicitName !== undefined) known.add(step.explicitName);
    if (step.id !== undefined) known.add(step.id);
  }
  const unnamed = model.steps.some(
    (step) => step.explicitName === undefined && step.id === undefined
  );
  const drafts: Draft[] = [];
  for (const step of model.steps) {
    if (step.broken !== undefined) continue;
    for (const target of step.depends) {
      if (known.has(target) || (unnamed && SERVER_NAME.test(target))) continue;
      drafts.push({
        step,
        severity: 'error',
        message: `Depends on the unknown step "${target}".`,
        fieldPath: `${step.key}.depends`,
      });
    }
  }
  return drafts;
}

function hasExecution(step: StepView): boolean {
  const { value } = step;
  return EXECUTION_KEYS.some((key) => key in value && !isBlank(value[key]));
}

/** The names and ids of the steps of the foreach bodies of a step value. */
function foreachStepRefs(value: Record<string, unknown>): Set<string> {
  const refs = new Set<string>();
  const visit = (item: unknown) => {
    if (!isRecord(item)) return;
    for (const key of ['id', 'name']) {
      if (typeof item[key] === 'string') refs.add(item[key]);
    }
    if (isRecord(item.foreach) && Array.isArray(item.foreach.steps)) {
      item.foreach.steps.forEach(visit);
    }
  };
  if (isRecord(value.foreach) && Array.isArray(value.foreach.steps)) {
    value.foreach.steps.forEach(visit);
  }
  return refs;
}

/** The ranges of the YAML comments of a text, as [from, to) offsets. */
function commentRanges(text: string): [number, number][] {
  const ranges: [number, number][] = [];
  let offset = 0;
  for (const token of new Lexer().lex(text)) {
    // The lexer marks the start of a document, a scalar and a flow error
    // with a control character that is not in the text.
    if (
      token === CST.DOCUMENT ||
      token === CST.SCALAR ||
      token === CST.FLOW_END
    ) {
      continue;
    }
    if (CST.tokenType(token) === 'comment') {
      ranges.push([offset, offset + token.length]);
    }
    offset += token.length;
  }
  return ranges;
}

/** The names of the outputs of a step that the text declares. */
function declaredOutputs(step: StepView): string[] | null {
  const outputs = step.value.outputs;
  return isRecord(outputs) ? Object.keys(outputs) : null;
}

function referenceDrafts(
  model: DagDocModel,
  scope: ReferenceScope | undefined
): Draft[] {
  const byRef = new Map<string, StepView>();
  for (const step of model.steps) {
    if (step.id !== undefined) byRef.set(step.id, step);
    if (step.explicitName !== undefined && !byRef.has(step.explicitName)) {
      byRef.set(step.explicitName, step);
    }
  }
  const comments = commentRanges(model.text);
  const inComment = (offset: number) =>
    comments.some(([from, to]) => offset >= from && offset < to);
  const drafts: Draft[] = [];
  for (const step of everyStep(model)) {
    if (step.broken !== undefined) continue;
    const text = model.text.slice(step.range.from, step.range.to);
    // A body step of a foreach can reference the steps of its body.
    const body = foreachStepRefs(step.value);
    const seen = new Set<string>();
    for (const token of findReferenceTokens(text)) {
      if (token.namespace !== 'steps') continue;
      const offset = step.range.from + token.from;
      if (inComment(offset)) continue;
      const [, id = '', , output = ''] = token.path.split('.');
      if (seen.has(token.path) || body.has(id)) continue;
      seen.add(token.path);
      const target = byRef.get(id);
      const known = scope?.steps.find(
        (item) => item.id === id || item.name === id
      );
      if (!target && !known) {
        drafts.push({
          step,
          severity: 'error',
          message: `The reference \${steps.${id}.outputs.${output}} names the unknown step "${id}".`,
          offset,
        });
        continue;
      }
      let missing = false;
      if (known?.outputsKnown === 'static') {
        missing = !known.outputs.some((item) => item.name === output);
      } else if (!known && target) {
        const declared = declaredOutputs(target);
        missing = declared ? !declared.includes(output) : !hasExecution(target);
      }
      if (missing) {
        drafts.push({
          step,
          severity: 'error',
          message: `The step "${id}" has no output "${output}".`,
          offset,
        });
      }
    }
  }
  return drafts;
}

const CRON_PREFIX = /^(?:CRON_TZ|TZ)=\S+\s+/;

function scheduleDrafts(model: DagDocModel): Draft[] {
  const schedule = readSchedule(model.text);
  if (schedule.expressions.length === 0) return [];
  const lines = model.text.split('\n');
  const anchor = lines.findIndex((line) => /^schedule\s*:/.test(line));
  const drafts: Draft[] = [];
  const used = new Set<number>();
  for (const expression of schedule.expressions) {
    const bare = expression.replace(CRON_PREFIX, '').trim();
    // The server reads `@every` and the descriptors.
    if (bare.startsWith('@') || nextRuns(bare, 1) !== null) continue;
    const index = lines.findIndex(
      (line, i) => i > anchor && !used.has(i) && line.includes(expression)
    );
    if (index >= 0) used.add(index);
    let offset = 0;
    for (let i = 0; i < Math.max(index, 0); i++) {
      offset += (lines[i] ?? '').length + 1;
    }
    drafts.push({
      step: null,
      severity: 'warning',
      message: `The schedule "${expression}" is not a cron expression that the editor can read.`,
      offset:
        index >= 0 ? offset + (lines[index]?.indexOf(expression) ?? 0) : 0,
    });
  }
  return drafts;
}

function toIssue(draft: Draft, input: ClientIssueInput): ValidationIssue {
  const { model, names } = input;
  const issue: ValidationIssue = {
    severity:
      draft.severity === 'error'
        ? ValidationIssueSeverity.error
        : ValidationIssueSeverity.warning,
    source:
      draft.severity === 'error'
        ? ValidationIssueSource.build
        : ValidationIssueSource.warning,
    message: draft.message,
  };
  if (draft.step) {
    issue.stepPath = draft.step.key;
    const name = names.byKey[draft.step.key] ?? draft.step.explicitName;
    if (name !== undefined) issue.stepName = name;
  }
  if (draft.fieldPath) issue.fieldPath = draft.fieldPath;
  const range =
    draft.offset !== undefined
      ? rangeFromOffsets(model.text, draft.offset, draft.offset)
      : locateIssue(model, issue, names);
  if (range) {
    issue.line = range.startLine;
    issue.column = range.startColumn;
  }
  return issue;
}

/**
 * Every problem of the file that the editor can find without the server,
 * with the step, the field and the line of each. A step with a YAML error
 * is left to the parser. The order is the order of the checks; `mergeIssues`
 * sorts it.
 */
export function collectClientIssues(
  input: ClientIssueInput
): ValidationIssue[] {
  const { model, schema, scope } = input;
  if (!model.ok) return [];
  const drafts: Draft[] = [];
  const known = schema
    ? new Set([
        ...builtinActionNames(schema),
        ...(input.customActions ?? []),
        ...localActionNames(model),
      ])
    : null;
  for (const step of everyStep(model)) {
    if (step.broken !== undefined) continue;
    const own = actionDrafts(step, known);
    drafts.push(...own);
    // An empty or unknown action: the rules of its `with` have nothing to check.
    if (schema && !own.some((draft) => draft.severity === 'error')) {
      drafts.push(...schemaDrafts(step, schema));
    }
  }
  drafts.push(
    ...identityDrafts(model),
    ...dependencyDrafts(model),
    ...referenceDrafts(model, scope),
    ...scheduleDrafts(model)
  );
  return drafts.map((draft) => toIssue(draft, input));
}

const EMPTY_COMMAND = /\bstep command is empty\b/;
const REQUIRED = /(?:^|[\s:])([A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*) is required/;

/**
 * Two issues about the same problem have the same signature: the same step
 * and field for a missing field, the same step for an empty command, the
 * same text otherwise.
 */
function signatureOf(issue: ValidationIssue): string {
  const step = issue.stepPath ?? issue.stepName ?? '';
  if (
    EMPTY_COMMAND.test(issue.message) ||
    issue.message === EMPTY_ACTION_MESSAGE
  ) {
    return `empty|${step}`;
  }
  const required = REQUIRED.exec(issue.message)?.[1];
  if (required && issue.fieldPath) {
    // The server gives the parent field (`steps[0].with`) and the dotted
    // name (`with.url`); the client gives the field itself.
    const parts = required.split('.');
    const last = issue.fieldPath.split('.').pop();
    const field =
      parts.length > 1 && parts[0] === last
        ? `${issue.fieldPath}.${parts.slice(1).join('.')}`
        : issue.fieldPath;
    return `required|${step}|${field}`;
  }
  return `${issue.severity}|${step}|${issue.fieldPath ?? ''}|${issue.message}`;
}

const REFERENCE_NAMES =
  /steps\.([A-Za-z0-9_]+)\.outputs\.([A-Za-z0-9_]+)|"([^"]+)"/g;

/** True when a server value-reference issue already says what `issue` says. */
function coveredByServerReference(
  issue: ValidationIssue,
  server: ValidationIssue[]
): boolean {
  const mentioned = [...issue.message.matchAll(REFERENCE_NAMES)].flatMap(
    (match) => [match[1], match[2], match[3]].filter(Boolean) as string[]
  );
  if (
    mentioned.length === 0 ||
    !/^The (?:step|reference)/.test(issue.message)
  ) {
    return false;
  }
  return server.some(
    (other) =>
      other.source === ValidationIssueSource.valueReference &&
      (other.stepPath ?? other.stepName) !== undefined &&
      (other.stepPath === issue.stepPath ||
        other.stepName === issue.stepName) &&
      mentioned.every((name) => other.message.includes(name))
  );
}

/**
 * The issues of the server and the issues of the client in one list. A
 * client issue that the server has said already is dropped. The list is
 * sorted by line; an issue without a line comes first (it is about the
 * whole file), and errors come before warnings on one line.
 */
export function mergeIssues(
  model: DagDocModel,
  names: StepNameMap,
  server: ValidationIssue[],
  client: ValidationIssue[]
): ValidationIssue[] {
  if (client.length === 0) return server;
  const taken = new Set(server.map(signatureOf));
  const merged = [...server];
  for (const issue of client) {
    const signature = signatureOf(issue);
    if (taken.has(signature) || coveredByServerReference(issue, server)) {
      continue;
    }
    taken.add(signature);
    merged.push(issue);
  }
  const lineOf = new Map<ValidationIssue, number>();
  for (const issue of merged) {
    lineOf.set(
      issue,
      issue.line ?? locateIssue(model, issue, names)?.startLine ?? 0
    );
  }
  const rank = (issue: ValidationIssue) =>
    issue.severity === ValidationIssueSeverity.error
      ? 0
      : issue.severity === ValidationIssueSeverity.warning
        ? 1
        : 2;
  return merged
    .map((issue, index) => ({ issue, index }))
    .sort(
      (a, b) =>
        (lineOf.get(a.issue) ?? 0) - (lineOf.get(b.issue) ?? 0) ||
        rank(a.issue) - rank(b.issue) ||
        a.index - b.index
    )
    .map((item) => item.issue);
}

/** The line of an issue in the text, or null when it has no place. */
export function issueLine(
  model: DagDocModel,
  names: StepNameMap,
  issue: ValidationIssue
): number | null {
  return issue.line ?? locateIssue(model, issue, names)?.startLine ?? null;
}
