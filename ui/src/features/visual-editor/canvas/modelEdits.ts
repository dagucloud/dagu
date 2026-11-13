// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { modelNotListed } from '@/features/ai-endpoints/modelCheck';
import {
  modelInfos,
  offeredEfforts,
  type ModelInfo,
} from '@/features/ai-endpoints/modelInfo';
import type { AIEndpointChoice } from '@/features/ai-endpoints/useEndpointChoices';
import type { DagEditOp, StepKey } from '../contracts';
import type { ModelEditData, ModelEditRow } from './chatModel';

function setField(
  step: StepKey,
  path: (string | number)[],
  value: unknown
): DagEditOp {
  return { kind: 'setStepField', step, path, value };
}

/** A change of the block `with.endpoint` of a step: a key with the value undefined is removed, a key that is absent stays. */
type EndpointPatch = Partial<
  Record<'name' | 'model' | 'effort', string | undefined>
>;

const ENDPOINT_KEYS = ['name', 'model', 'effort'] as const;

/**
 * True when the node writes the block `with.endpoint`: an agent step, and a
 * chat step in the block form or with no model information yet. A chat step of
 * the old method and a list of models keep their own keys.
 */
function blockForm(edit: ModelEditData): boolean {
  return (
    edit.kind === 'agent' || edit.shape === 'block' || edit.shape === 'none'
  );
}

/**
 * The edits of a change of the block `with.endpoint`. Each key of the patch
 * is a field of its own, and the block is created when absent.
 */
function blockEdits(step: StepKey, patch: EndpointPatch): DagEditOp[] {
  return ENDPOINT_KEYS.filter((key) => key in patch).map((key) =>
    setField(step, ['with', 'endpoint', key], patch[key])
  );
}

/**
 * The models that a step can use from an endpoint: its models with their
 * infos, and for an agent step the aliases of its CLI. The Model picker and
 * the change of endpoint read the same list.
 */
export function listedModels(
  infos: readonly ModelInfo[],
  aliases: readonly string[] | undefined,
  agent: boolean
): ModelInfo[] {
  return [
    ...infos,
    ...(agent ? (aliases ?? []) : []).map((id) => ({ id })),
  ].filter(
    (info, index, all) => all.findIndex((i) => i.id === info.id) === index
  );
}

/**
 * The effort levels that a step can offer for the model `name` of an
 * endpoint: the levels of its info. An agent step offers every level of the
 * CLI for a model with no info, a chat step offers none.
 */
export function modelLevels(
  infos: readonly ModelInfo[],
  aliases: readonly string[] | undefined,
  agent: boolean,
  name: string
): string[] {
  return offeredEfforts(
    listedModels(infos, aliases, agent).find((info) => info.id === name),
    agent
  );
}

/** What the change of the endpoint of a step reads about the new endpoint. */
export interface EndpointLists {
  /** The endpoints of the DAG with their models. */
  choices: readonly AIEndpointChoice[];
  /** The aliases of the CLI claude from the server. Undefined: no model check. */
  aliases: readonly string[] | undefined;
}

/**
 * The edits of a choice in the Endpoint select of the Model node and of the
 * form. For an agent step and for a chat step in the block form they write
 * `with.endpoint.name`, and in the same edit they remove `with.endpoint.model`
 * and `with.endpoint.effort` when the new endpoint does not list the model (the
 * levels of effort belong to the model), and `with.endpoint.effort` alone when
 * the new endpoint describes the model that stays and that model does not
 * support the effort. A `${...}` reference, an alias of the CLI claude and an
 * endpoint with no list of models keep the model and the effort.
 *
 * The first endpoint of a chat step of the old method converts it to the block
 * in the same edit: the model moves to `with.endpoint.model` when the endpoint
 * lists it, the effort of `with.thinking` moves to `with.endpoint.effort` when
 * the model stays and supports it, and the keys of the old method go. An entry of a list
 * of models gets the key `endpoint`: an endpoint gives the provider type, so a
 * provider of the old method goes.
 */
export function endpointEdits(
  step: StepKey,
  edit: ModelEditData,
  row: ModelEditRow,
  value: string | undefined,
  lists: EndpointLists
): DagEditOp[] {
  if (row.index === null) {
    const agent = edit.kind === 'agent';
    const patch: EndpointPatch = { name: value };
    if (value !== undefined && row.name !== '') {
      const choice = lists.choices.find((item) => item.name === value);
      const dropModel = modelNotListed({
        models: choice?.models ?? [],
        model: row.name,
        cliProvider: edit.cli,
        aliases: lists.aliases,
      });
      if (dropModel) {
        // The levels of effort belong to the model: the model goes, so the
        // effort goes with it.
        patch.model = undefined;
        if (edit.effort) patch.effort = undefined;
      } else if (edit.effort) {
        const info = listedModels(
          modelInfos(choice),
          lists.aliases,
          agent
        ).find((item) => item.id === row.name);
        if (dropsEffort(info, edit.effort)) patch.effort = undefined;
      }
    }
    if (blockForm(edit)) return blockEdits(step, patch);
    if (value === undefined) return [];
    // The old method: the whole conversion is one edit.
    const ops = [setField(step, ['with', 'endpoint', 'name'], value)];
    if (row.name !== '' && !('model' in patch)) {
      ops.push(setField(step, ['with', 'endpoint', 'model'], row.name));
    }
    if (edit.effort && !('effort' in patch)) {
      ops.push(setField(step, ['with', 'endpoint', 'effort'], edit.effort));
    }
    for (const key of edit.oldKeys ?? []) {
      ops.push(setField(step, ['with', key], undefined));
    }
    return ops;
  }
  const at = ['with', 'model', row.index];
  const ops = [setField(step, [...at, 'endpoint'], value)];
  if (value !== undefined && row.provider !== '') {
    ops.push(setField(step, [...at, 'provider'], undefined));
  }
  return ops;
}

/**
 * The edit of the text of the Model field of the node: `with.endpoint.model`
 * for an agent step and for a chat step in the block form, `with.model` for a
 * chat step of the old method, and the name of the entry for a list, as the
 * form writes them. An empty text removes the key, and leaves an empty name in
 * a list.
 */
export function modelEdits(
  step: StepKey,
  edit: ModelEditData,
  row: ModelEditRow,
  text: string | undefined
): DagEditOp[] {
  if (blockForm(edit)) return blockEdits(step, { model: text });
  if (row.index === null) return [setField(step, ['with', 'model'], text)];
  return [setField(step, ['with', 'model', row.index, 'name'], text ?? '')];
}

/**
 * True when the step writes the block `with.endpoint`, which holds the effort:
 * an agent step, and a chat step in the block form or with no model key yet.
 * A chat step of the old method and a list of models have no effort control.
 */
export function hasEffort(edit: ModelEditData): boolean {
  return blockForm(edit);
}

/**
 * The edit of the effort level of the step; undefined removes the key. This
 * is the only place that knows the key: `with.endpoint.effort`. A step that
 * has no effort control gets no edit.
 */
export function effortEdits(
  step: StepKey,
  edit: ModelEditData,
  level: string | undefined
): DagEditOp[] {
  return blockForm(edit) ? blockEdits(step, { effort: level }) : [];
}

/**
 * The edits of a pick in the Model picker: the name of the model, and the
 * effort of the step goes in the same edit when the new model does not support
 * it. `info` describes the new model.
 */
export function modelPickEdits(
  step: StepKey,
  edit: ModelEditData,
  row: ModelEditRow,
  name: string | undefined,
  info: ModelInfo | undefined
): DagEditOp[] {
  if (!blockForm(edit)) return modelEdits(step, edit, row, name);
  return blockEdits(step, {
    model: name,
    ...(dropsEffort(info, edit.effort) ? { effort: undefined } : {}),
  });
}

/**
 * True when the effort of the step goes with a change of its model: the levels
 * of the model are known and lack the effort. A model with no known levels
 * keeps the effort, because nothing tells that the effort is wrong.
 */
function dropsEffort(
  info: ModelInfo | undefined,
  effort: string | undefined
): boolean {
  return !!effort && !offeredEfforts(info, true).includes(effort);
}

/** The endpoints that the node offers for a step: the protocols that it accepts. */
export function endpointProtocols(edit: ModelEditData): readonly string[] {
  return edit.kind === 'agent' ? ['anthropic'] : ['anthropic', 'openai'];
}
