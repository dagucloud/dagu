// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  YAMLMap,
  YAMLSeq,
  isAlias,
  isMap,
  isNode,
  isScalar,
  isSeq,
} from 'yaml';
import type { Document, Node, Scalar } from 'yaml';
import type {
  ApplyEditOptions,
  DagEditOp,
  EditErrorCode,
  EditResult,
  StepKey,
} from '../../contracts';
import { validateStepId, validateStepName } from '../names';
import { editDocument } from '../patch';
import type { MutateOutcome } from '../patch';
import { parseDocumentAsServer } from '../serverYaml';
import { explicitNameOf, findStep, idOf, indexSteps } from '../stepIndex';
import type { IndexedStep } from '../stepIndex';
import { mentionsStep, rewriteStepReferences } from './references';

/** A failure that an operation reports with an error code. */
class OpError extends Error {
  constructor(
    readonly code: EditErrorCode,
    message: string
  ) {
    super(message);
  }
}

/** Runs `mutate` and turns an `OpError` into a failure outcome. */
function guarded(mutate: (doc: Document) => MutateOutcome) {
  return (doc: Document): MutateOutcome => {
    try {
      return mutate(doc);
    } catch (error) {
      if (error instanceof OpError) {
        return { ok: false, code: error.code, message: error.message };
      }
      throw error;
    }
  };
}

function requireStep(doc: Document, key: StepKey): IndexedStep {
  const step = findStep(doc, key);
  if (!step) throw new OpError('unknownStep', `No step has the key "${key}".`);
  return step;
}

function plainStringMessage(key: StepKey): string {
  return `Step "${key}" is a plain string. Use convertStepsToExplicitForm first.`;
}

/** Puts `node` in the place of the entry of the step. */
function replaceEntry(step: IndexedStep, node: Node): void {
  const loc = step.loc;
  switch (loc.form) {
    case 'block':
    case 'shorthand':
      loc.seq.items[loc.seqIndex] = node;
      break;
    case 'parallelGroup':
      loc.group.items[loc.groupIndex] = node;
      break;
    case 'mapEntry':
    case 'handler':
      loc.pair.value = node;
      break;
  }
}

/**
 * Returns the mapping of the step entry. An empty entry gets a new mapping
 * when `create` is true; else null is returned. A plain string entry fails.
 */
function entryMap(step: IndexedStep, create: boolean): YAMLMap | null {
  if (isMap(step.node)) return step.node;
  if (step.node === null) {
    if (!create) return null;
    const map = new YAMLMap();
    replaceEntry(step, map);
    return map;
  }
  throw new OpError('unsupportedForm', plainStringMessage(step.key));
}

/**
 * Sets the text of a scalar and keeps its quote or block style. A scalar that
 * held a number or a boolean loses its number format.
 */
function setScalarString(node: Scalar, value: string): void {
  if (node.value === value) return;
  if (typeof node.value !== 'string') {
    delete node.type;
    delete node.format;
    delete node.minFractionDigits;
  }
  node.value = value;
}

function stringScalar(node: unknown): Scalar<string> | null {
  return isScalar(node) && typeof node.value === 'string'
    ? (node as Scalar<string>)
    : null;
}

type Container = YAMLMap | YAMLSeq;

function childOf(container: Container, key: string | number): unknown {
  if (isMap(container)) {
    if (typeof key !== 'string') {
      throw new OpError(
        'invalidYaml',
        `Cannot use the index ${key} in a mapping.`
      );
    }
    return container.get(key, true);
  }
  if (typeof key !== 'number' || !Number.isInteger(key) || key < 0) {
    throw new OpError('invalidYaml', `Cannot use the key "${key}" in a list.`);
  }
  return container.items[key];
}

function putChild(
  container: Container,
  key: string | number,
  node: Node
): void {
  if (isMap(container)) {
    container.set(key as string, node);
    return;
  }
  const index = key as number;
  if (index > container.items.length) {
    throw new OpError(
      'invalidYaml',
      `The list index ${index} is out of range.`
    );
  }
  container.items[index] = node;
}

function copyLeadingTrivia(from: unknown, to: Node): void {
  if (!isNode(from)) return;
  if (from.commentBefore !== undefined && to.commentBefore === undefined) {
    to.commentBefore = from.commentBefore;
  }
  if (from.spaceBefore && !to.spaceBefore) to.spaceBefore = true;
}

function isNullScalar(node: unknown): boolean {
  return (
    node === undefined ||
    node === null ||
    (isScalar(node) && node.value === null)
  );
}

/**
 * Sets (or, with an undefined value, removes) one field of a step entry.
 * Missing maps on the path are created and new keys go last. A string that
 * replaces a string keeps the quote or block style of the old scalar.
 */
export function opSetStepField(
  text: string,
  op: Extract<DagEditOp, { kind: 'setStepField' }>,
  // Kept for the common op signature.
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  _options: ApplyEditOptions = {}
): EditResult {
  return editDocument(
    text,
    guarded((doc) => {
      const step = requireStep(doc, op.step);
      const { path, value } = op;
      const last = path[path.length - 1];
      if (last === undefined) {
        throw new OpError('unsupportedForm', 'The field path is empty.');
      }
      const removing = value === undefined;
      const root = entryMap(step, !removing);
      if (!root)
        return { ok: false, code: 'noop', message: 'The field is absent.' };

      const trail: Container[] = [root];
      let container: Container = root;
      for (let i = 0; i < path.length - 1; i++) {
        const key = path[i] as string | number;
        const child = childOf(container, key);
        if (isMap(child) || isSeq(child)) {
          container = child;
        } else if (isAlias(child)) {
          throw new OpError(
            'invalidYaml',
            `The path crosses an alias at "${key}".`
          );
        } else if (isNullScalar(child)) {
          if (removing) {
            return { ok: false, code: 'noop', message: 'The field is absent.' };
          }
          const next = path[i + 1];
          const created: Container =
            typeof next === 'number' ? new YAMLSeq() : new YAMLMap();
          putChild(container, key, created);
          container = created;
        } else {
          throw new OpError(
            'invalidYaml',
            `Cannot reach "${path.join('.')}": "${key}" is a scalar.`
          );
        }
        trail.push(container);
      }

      if (removing) {
        const deleted = isMap(container)
          ? container.delete(last as string)
          : container.delete(last as number);
        if (!deleted)
          return { ok: false, code: 'noop', message: 'The field is absent.' };
        // Remove maps that the removal left empty. The step entry stays.
        for (let i = trail.length - 1; i > 0; i--) {
          const parent = trail[i - 1] as Container;
          const current = trail[i] as Container;
          if (!isMap(current) || current.items.length > 0) break;
          if (isMap(parent)) parent.delete(path[i - 1] as string);
          else parent.delete(path[i - 1] as number);
        }
        return;
      }

      const existing = childOf(container, last);
      const newText = stringScalar(existing);
      if (typeof value === 'string' && newText) {
        setScalarString(newText, value);
        return;
      }
      const node = doc.createNode(value);
      copyLeadingTrivia(existing, node);
      putChild(container, last, node);
    })
  );
}

/**
 * Replaces the whole entry of a step with the mapping in `op.yaml`. The
 * comment lines and the blank line above the entry stay. A plain string
 * entry can be replaced by a mapping.
 */
export function opReplaceStepYaml(
  text: string,
  op: Extract<DagEditOp, { kind: 'replaceStepYaml' }>,
  // Kept for the common op signature.
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  _options: ApplyEditOptions = {}
): EditResult {
  const parsed = parseDocumentAsServer(op.yaml, { intAsBigInt: true });
  const error = parsed.errors[0];
  if (error) {
    const first = error.message.split('\n')[0] ?? error.message;
    return { ok: false, code: 'invalidYaml', message: first.replace(/:$/, '') };
  }
  if (!isMap(parsed.contents)) {
    return {
      ok: false,
      code: 'invalidYaml',
      message: 'The step YAML must be one mapping.',
    };
  }
  const replacement = parsed.contents;
  return editDocument(
    text,
    guarded((doc) => {
      const step = requireStep(doc, op.step);
      const node = replacement.clone() as YAMLMap;
      if (step.loc.form !== 'mapEntry' && step.loc.form !== 'handler') {
        // The comment above an array item belongs to the item node.
        copyLeadingTrivia(step.node, node);
        if (isNode(step.node) && step.node.commentBefore !== undefined) {
          node.commentBefore = step.node.commentBefore;
        }
      }
      replaceEntry(step, node);
    })
  );
}

/** The names that a reference can use before and after an edit of one step. */
interface RefChange {
  nameBefore?: string;
  nameAfter?: string;
  idBefore?: string;
  idAfter?: string;
}

// The text of a scalar that names a step. The server reads a number or a
// boolean in a list of names as its text (`StringOrArray` in
// internal/spec/types/stringarray.go).
function refText(node: unknown): string | undefined {
  if (!isScalar(node)) return undefined;
  const { value } = node;
  return typeof value === 'string' ||
    typeof value === 'number' ||
    typeof value === 'bigint' ||
    typeof value === 'boolean'
    ? String(value)
    : undefined;
}

/**
 * Calls `fn` for each scalar that names a step, with its text: `depends`,
 * `approval.rewind_to` and `with.push_back.rewind_to` (name or id, see
 * `resolveStepDependencies` in internal/spec/validator.go), and the targets
 * of `routes` and `with.routes` (name only, see `transformRouterSteps` in
 * internal/spec/dag.go).
 */
function visitStepRefs(
  entry: YAMLMap,
  fn: (scalar: Scalar, text: string, nameOnly: boolean) => void
): void {
  const one = (node: unknown, nameOnly: boolean) => {
    const text = refText(node);
    if (text !== undefined) fn(node as Scalar, text, nameOnly);
  };
  const list = (node: unknown, nameOnly: boolean) => {
    if (isSeq(node)) {
      for (const item of node.items) one(item, nameOnly);
    } else {
      one(node, nameOnly);
    }
  };
  list(entry.get('depends', true), false);
  list(entry.getIn(['approval', 'rewind_to'], true), false);
  list(entry.getIn(['with', 'push_back', 'rewind_to'], true), false);
  for (const routes of [
    entry.get('routes', true),
    entry.getIn(['with', 'routes'], true),
  ]) {
    if (!isMap(routes)) continue;
    for (const pair of routes.items) list(pair.value, true);
  }
}

/**
 * Returns the text that keeps a reference valid after the change, or null when
 * the reference stays valid or does not name the changed step.
 */
function retarget(
  value: string,
  nameOnly: boolean,
  c: RefChange
): string | null {
  if (nameOnly) {
    return c.nameBefore !== undefined &&
      value === c.nameBefore &&
      c.nameAfter !== undefined &&
      c.nameAfter !== value
      ? c.nameAfter
      : null;
  }
  const byId = c.idBefore !== undefined && value === c.idBefore;
  const byName = c.nameBefore !== undefined && value === c.nameBefore;
  if (!byId && !byName) return null;
  if (value === c.nameAfter || value === c.idAfter) return null;
  return (byId ? (c.idAfter ?? c.nameAfter) : c.nameAfter) ?? null;
}

function rewriteStepRefs(doc: Document, change: RefChange): void {
  for (const step of indexSteps(doc).steps) {
    if (!isMap(step.node)) continue;
    visitStepRefs(step.node, (scalar, text, nameOnly) => {
      const next = retarget(text, nameOnly, change);
      if (next !== null) setScalarString(scalar, next);
    });
  }
}

/** Names and ids that the other steps of the DAG use. Handlers have their own scope. */
function identitiesOfOthers(
  doc: Document,
  self: IndexedStep,
  options: ApplyEditOptions
): Set<string> {
  const taken = new Set<string>();
  for (const step of indexSteps(doc).steps) {
    if (step.key === self.key) continue;
    for (const value of [
      explicitNameOf(step),
      idOf(step),
      options.names?.byKey[step.key],
    ]) {
      if (value !== undefined) taken.add(value);
    }
  }
  return taken;
}

function keyText(node: unknown): string | undefined {
  return isScalar(node) ? String(node.value) : undefined;
}

/** Sets a string field of a step mapping at `index` when the key is new. */
function setMapString(
  doc: Document,
  map: YAMLMap,
  key: string,
  value: string,
  index: number
): void {
  const existing = map.get(key, true);
  if (isScalar(existing)) {
    setScalarString(existing, value);
  } else if (existing === undefined) {
    map.items.splice(index, 0, doc.createPair(key, value));
  } else {
    map.set(key, doc.createNode(value));
  }
}

function requireNamedForm(step: IndexedStep): void {
  if (step.loc.form === 'shorthand' || step.loc.form === 'parallelGroup') {
    throw new OpError(
      'unsupportedForm',
      `Step "${step.key}" is a plain string or a parallel group entry. Use convertStepsToExplicitForm first.`
    );
  }
}

/**
 * Renames a step. Array form sets `name:`; map form renames the key. The
 * entries of `depends`, the route targets and the rewind targets that match the
 * old name change too. Tokens like `${steps.<id>.outputs.x}` stay: they
 * resolve by id (specs/009-step-reference.md).
 */
export function opRenameStep(
  text: string,
  op: Extract<DagEditOp, { kind: 'renameStep' }>,
  options: ApplyEditOptions = {}
): EditResult {
  const invalid = validateStepName(op.newName);
  if (invalid) return { ok: false, code: 'invalidName', message: invalid };
  return editDocument(
    text,
    guarded((doc) => {
      const step = requireStep(doc, op.step);
      requireNamedForm(step);
      const isHandler = step.loc.form === 'handler';
      if (
        !isHandler &&
        identitiesOfOthers(doc, step, options).has(op.newName)
      ) {
        throw new OpError(
          'duplicateName',
          `Another step already uses "${op.newName}" as its name or ID.`
        );
      }
      const id = idOf(step);
      const nameBefore =
        explicitNameOf(step) ?? id ?? options.names?.byKey[step.key];

      if (step.loc.form === 'mapEntry') {
        const key = step.loc.pair.key;
        if (isScalar(key)) setScalarString(key, op.newName);
        else step.loc.pair.key = doc.createNode(op.newName);
      } else {
        setMapString(
          doc,
          entryMap(step, true) as YAMLMap,
          'name',
          op.newName,
          0
        );
      }

      if (isHandler) return;
      rewriteStepRefs(doc, {
        nameBefore,
        nameAfter: op.newName,
        idBefore: id,
        idAfter: id,
      });
      if (step.loc.form !== 'mapEntry') return;
      // Map form: the server numbers the steps by name, so the key can change.
      const pair = step.loc.pair;
      const moved = indexSteps(doc).steps.find(
        (s) => s.loc.form === 'mapEntry' && s.loc.pair === pair
      );
      return moved ? { ok: true, select: moved.key } : undefined;
    })
  );
}

/** Calls `fn` for each string value (not key) below `node`. */
function visitStringValues(
  node: unknown,
  fn: (scalar: Scalar<string>) => void
): void {
  if (isMap(node)) {
    for (const pair of node.items) visitStringValues(pair.value, fn);
  } else if (isSeq(node)) {
    for (const item of node.items) visitStringValues(item, fn);
  } else {
    const scalar = stringScalar(node);
    if (scalar) fn(scalar);
  }
}

/**
 * Sets, changes or removes (`id: null`) the `id` of a step. A new key goes
 * after `name:`, else first. Tokens that use the old id change in every
 * string value of the steps and the handlers. Entries of `depends`, the
 * rewind targets and the route targets keep naming the step.
 *
 * A step with no `name:` takes its name from the id. When the id appears or
 * goes away, the operation writes the current name as `name:` so that the name
 * does not change. A removal fails when a token still uses the old id.
 */
export function opSetStepId(
  text: string,
  op: Extract<DagEditOp, { kind: 'setStepId' }>,
  options: ApplyEditOptions = {}
): EditResult {
  const newId = op.id;
  if (newId !== null) {
    const invalid = validateStepId(newId);
    if (invalid) return { ok: false, code: 'invalidName', message: invalid };
  }
  return editDocument(
    text,
    guarded((doc) => {
      const step = requireStep(doc, op.step);
      requireNamedForm(step);
      const isHandler = step.loc.form === 'handler';
      if (
        newId !== null &&
        !isHandler &&
        identitiesOfOthers(doc, step, options).has(newId)
      ) {
        throw new OpError(
          'duplicateName',
          `Another step already uses "${newId}" as its name or ID.`
        );
      }
      const oldId = idOf(step);
      const explicit = explicitNameOf(step);

      if (newId === null && oldId !== undefined) {
        const { steps, handlers } = indexSteps(doc);
        for (const other of [...steps, ...handlers]) {
          let used = false;
          visitStringValues(other.node, (scalar) => {
            used ||= mentionsStep(scalar.value, oldId);
          });
          if (used) {
            const label =
              explicitNameOf(other) ??
              idOf(other) ??
              options.names?.byKey[other.key] ??
              other.key;
            throw new OpError(
              'invalidName',
              `Step "${label}" still uses references to the ID "${oldId}". Change them first.`
            );
          }
        }
      }

      let nameToWrite: string | undefined;
      if (!isHandler && explicit === undefined) {
        if (oldId === undefined && newId !== null) {
          nameToWrite = options.names?.byKey[step.key];
        } else if (oldId !== undefined && newId === null) {
          nameToWrite = oldId;
        }
      }

      const map = entryMap(step, newId !== null);
      if (!map)
        return { ok: false, code: 'noop', message: 'The step has no ID.' };
      if (nameToWrite !== undefined) {
        setMapString(doc, map, 'name', nameToWrite, 0);
      }
      if (newId === null) {
        map.delete('id');
      } else {
        const nameIndex = map.items.findIndex((p) => keyText(p.key) === 'name');
        setMapString(doc, map, 'id', newId, nameIndex + 1);
      }

      if (isHandler) return;
      rewriteStepRefs(doc, {
        nameBefore: explicit ?? oldId ?? options.names?.byKey[step.key],
        nameAfter: explicit ?? nameToWrite ?? newId ?? undefined,
        idBefore: oldId,
        idAfter: newId ?? undefined,
      });
      if (newId === null || oldId === undefined) return;
      const { steps, handlers } = indexSteps(doc);
      for (const other of [...steps, ...handlers]) {
        visitStringValues(other.node, (scalar) => {
          const next = rewriteStepReferences(scalar.value, oldId, newId);
          if (next !== scalar.value) setScalarString(scalar, next);
        });
      }
    })
  );
}
