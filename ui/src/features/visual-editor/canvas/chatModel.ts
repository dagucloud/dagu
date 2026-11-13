// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { readStepSkills } from '../tools/skills';
import { isRecord, type Rec } from '../record';

/** One model of a `chat.completion` step, as the Model sub-node names it. */
export interface ModelRef {
  name: string;
  provider?: string;
  /**
   * The AI endpoint of the model (Spec 094): `with.endpoint.name` of an
   * `agent.run` step or of a chat step, or the endpoint of a model of a list.
   */
  endpoint?: string;
}

/**
 * One model of a step as the Model node edits it (Spec 093, "The UI contract").
 * The node writes the keys that the form writes: `with.endpoint.name` and
 * `with.endpoint.model` for an agent step and for a chat step in the block
 * form, `with.model` for a chat step of the old method, and the keys of the
 * entry of `with.model` for a list.
 */
export interface ModelEditRow {
  /** The place in `with.model` when it is a list. Null for the single form. */
  index: number | null;
  /** The model text of the YAML. It is empty when the YAML has none. */
  name: string;
  /** The endpoint of the row. It is empty when the row has none. */
  endpoint: string;
  /** The provider of the old method that the row holds. It is empty when none. */
  provider: string;
}

/**
 * How a step holds its endpoint, model and effort: `block` (`with.endpoint`
 * is a map), `legacy` (a chat step of the old method: `provider`, `model`,
 * `base_url`, `api_key_name` or `thinking`) or `none` (no key of these).
 */
export type EndpointShape = 'none' | 'block' | 'legacy';

/** What the Model node edits: one row for each model, and the CLI of an agent step. */
export interface ModelEditData {
  /** An agent step takes the endpoints of the protocol anthropic, and one model. */
  kind: 'chat' | 'agent';
  rows: ModelEditRow[];
  /** The CLI provider of an agent step, which the form edits. It is empty for a chat step. */
  cli: string;
  /** How the step holds its endpoint. Absent for a list of models. */
  shape?: EndpointShape;
  /**
   * The keys of `with` that a chat step of the old method holds, in the order
   * that the first choice of an endpoint removes them. Empty for the other
   * shapes.
   */
  oldKeys?: string[];
  /**
   * The effort level of the step: `with.endpoint.effort`, or for the old
   * method `with.thinking.effort` while the thinking is not off, which the
   * first choice of an endpoint moves. Absent when the step has none.
   */
  effort?: string;
}

/**
 * What the canvas shows of the model of a chat step or of an agent step:
 * `with.model` and `with.provider`, and the AI endpoint of each model.
 */
export interface ChatInfo {
  /** The models in order: more than one means a fallback list. */
  models: ModelRef[];
  /** The names of `with.skills` in order. */
  skills: SkillEntry[];
  /** The limits of the tool loop, for the header of the Tools card. */
  limits?: ToolLimits;
  /** The system message and the prompt of an `agent.run` step, for its Prompt card. */
  texts?: PromptTexts;
  /**
   * The models as the Model node edits them. It is absent when a value of the
   * model is not text or a list of models, which only the YAML edits.
   */
  edit?: ModelEditData;
}

/** `with.system` and `with.prompt` as text: a value that is not text reads as empty. */
export interface PromptTexts {
  system: string;
  prompt: string;
}

/**
 * The limits of the tool loop of a step. A value is a number, or the text of
 * a `${...}` reference. `callsDefault` is set when `calls` is the default of
 * the key, not a value of the step.
 */
export interface ToolLimits {
  turns?: number | string;
  calls?: number | string;
  callsDefault?: true;
}

/** Default of `with.max_tool_calls` of an `agent.run` step. */
export const DEFAULT_MAX_TOOL_CALLS = 100;

/** One name of `with.skills`, with its place in the list. */
export interface SkillEntry {
  name: string;
  index: number;
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== ''
    ? value.trim()
    : undefined;
}

function limitOf(value: unknown): number | string | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  return typeof value === 'string' && value.includes('${')
    ? value.trim()
    : undefined;
}

function modelOf(item: unknown, provider: string | undefined): ModelRef | null {
  const ref = (
    name: string,
    own: string | undefined,
    endpoint: string | undefined
  ): ModelRef => {
    const model: ModelRef = { name };
    if (own !== undefined) model.provider = own;
    if (endpoint !== undefined) model.endpoint = endpoint;
    return model;
  };
  if (isRecord(item)) {
    const name = text(item.name) ?? text(item.model);
    if (name === undefined) return null;
    const own = text(item.provider);
    return ref(name, own ?? provider, text(item.endpoint));
  }
  const name = text(item);
  if (name === undefined) return null;
  return ref(name, provider, undefined);
}

/**
 * Reads the models and the skills of a chat step from the plain value of the step entry.
 * With the block `with.endpoint` the step has one model: `endpoint.model` of
 * the endpoint `endpoint.name`. Else `with.model` is a name or a list of names
 * (or of `{provider, name}` or `{endpoint, name}` maps), and `with.provider` is
 * the provider of an entry that names none. It never throws: a value of the
 * wrong type gives no model.
 */
export function readChatInfo(step: Rec): ChatInfo {
  const withBlock = step.with;
  if (!isRecord(withBlock)) return { models: [], skills: [] };
  const models: ModelRef[] = [];
  if (isRecord(withBlock.endpoint)) {
    const { name, model } = blockFields(withBlock.endpoint);
    if (model !== undefined) {
      models.push({
        name: model,
        ...(name !== undefined ? { endpoint: name } : {}),
      });
    }
  } else {
    const provider = text(withBlock.provider);
    const raw = withBlock.model;
    const list = Array.isArray(raw) ? raw : raw === undefined ? [] : [raw];
    for (const item of list) {
      const model = modelOf(item, provider);
      if (model) models.push(model);
    }
  }
  const info: ChatInfo = { models, skills: readSkillEntries(step) };
  const edit = readChatEdit(withBlock);
  if (edit) info.edit = edit;
  const turns = limitOf(withBlock.max_tool_iterations);
  if (turns !== undefined) info.limits = { turns };
  return info;
}

/** The edit data with the effort of the step, when it is a text. */
function withEffort(edit: ModelEditData, effort: unknown): ModelEditData {
  return typeof effort === 'string' && effort !== ''
    ? { ...edit, effort }
    : edit;
}

/** The keys of the old method of a chat step, in the order that the conversion removes them. */
const OLD_METHOD_KEYS = [
  'provider',
  'base_url',
  'api_key_name',
  'model',
  'thinking',
] as const;

/**
 * The rows that the Model node edits in a chat step, or undefined for a value
 * that only the YAML edits (an endpoint that is no map, or a model that is no
 * text and no list).
 */
function readChatEdit(withBlock: Rec): ModelEditData | undefined {
  if (withBlock.endpoint !== undefined) {
    return isRecord(withBlock.endpoint)
      ? readBlockEdit('chat', '', withBlock.endpoint)
      : undefined;
  }
  const raw = withBlock.model;
  if (raw === undefined || typeof raw === 'string') {
    const oldKeys = OLD_METHOD_KEYS.filter(
      (key) => withBlock[key] !== undefined
    );
    const thinking = isRecord(withBlock.thinking) ? withBlock.thinking : {};
    return withEffort(
      {
        kind: 'chat',
        cli: '',
        shape: oldKeys.length > 0 ? 'legacy' : 'none',
        oldKeys,
        rows: [
          {
            index: null,
            name: raw ?? '',
            endpoint: '',
            provider:
              typeof withBlock.provider === 'string' ? withBlock.provider : '',
          },
        ],
      },
      thinking.enabled === false ? undefined : thinking.effort
    );
  }
  if (!Array.isArray(raw)) return undefined;
  const rows: ModelEditRow[] = [];
  for (const [index, item] of raw.entries()) {
    // An entry of the list is a map: only the YAML edits a list with another value.
    if (!isRecord(item)) return undefined;
    rows.push({
      index,
      name: typeof item.name === 'string' ? item.name : '',
      endpoint: typeof item.endpoint === 'string' ? item.endpoint : '',
      provider: typeof item.provider === 'string' ? item.provider : '',
    });
  }
  return { kind: 'chat', cli: '', rows };
}

function readSkillEntries(step: Rec): SkillEntry[] {
  const skills: SkillEntry[] = [];
  for (const item of readStepSkills(step)) {
    if (item.name === '') continue;
    skills.push({ name: item.name, index: item.index });
  }
  return skills;
}

/** `name`, `model` and `effort` of the block `with.endpoint`: a value that is no text is absent. */
function blockFields(block: Rec): {
  name?: string;
  model?: string;
  effort?: string;
} {
  const name = text(block.name);
  const model = text(block.model);
  const effort = text(block.effort);
  return {
    ...(name !== undefined ? { name } : {}),
    ...(model !== undefined ? { model } : {}),
    ...(effort !== undefined ? { effort } : {}),
  };
}

/**
 * The edit data of the block `with.endpoint` of an agent step or of a chat
 * step, or undefined when a value of the block is not text: only the YAML
 * edits it.
 */
function readBlockEdit(
  kind: ModelEditData['kind'],
  cli: string,
  block: Rec
): ModelEditData | undefined {
  const optional = (value: unknown): string | undefined =>
    value === undefined || typeof value === 'string'
      ? (value ?? '')
      : undefined;
  const name = optional(block.name);
  const model = optional(block.model);
  const effort = optional(block.effort);
  if (name === undefined || model === undefined || effort === undefined) {
    return undefined;
  }
  return withEffort(
    {
      kind,
      cli,
      shape: 'block',
      rows: [{ index: null, name: model, endpoint: name, provider: '' }],
    },
    effort
  );
}

/**
 * The edit data of the endpoint of an agent step: the block `with.endpoint`,
 * or an empty row while the step has no endpoint. It is undefined when the
 * endpoint is not a map or a value of the block is not text: only the YAML
 * edits it.
 */
function readAgentEdit(block: Rec): ModelEditData | undefined {
  const raw = block.endpoint;
  const provider = typeof block.provider === 'string' ? block.provider : '';
  if (isRecord(raw)) return readBlockEdit('agent', provider, raw);
  if (raw !== undefined) return undefined;
  return {
    kind: 'agent',
    cli: provider,
    shape: 'none',
    rows: [{ index: null, name: '', endpoint: '', provider: '' }],
  };
}

/**
 * Reads the model and the skills of an `agent.run` step from the plain value
 * of the step entry. The step has one model: `with.endpoint.model` with its
 * `with.provider`, else the provider alone. It never throws.
 */
export function readAgentRunInfo(step: Rec): ChatInfo {
  const block = isRecord(step.with) ? step.with : {};
  const provider = text(block.provider);
  const { name: endpoint, model } = isRecord(block.endpoint)
    ? blockFields(block.endpoint)
    : {};
  const ref: ModelRef | null =
    model !== undefined
      ? { name: model }
      : provider !== undefined
        ? { name: provider }
        : null;
  if (ref && model !== undefined && provider !== undefined) {
    ref.provider = provider;
  }
  if (ref && endpoint !== undefined) ref.endpoint = endpoint;
  const limits: ToolLimits = {};
  const turns = limitOf(block.max_tool_iterations);
  if (turns !== undefined) limits.turns = turns;
  const calls = limitOf(block.max_tool_calls);
  if (calls !== undefined) {
    limits.calls = calls;
  } else {
    limits.calls = DEFAULT_MAX_TOOL_CALLS;
    limits.callsDefault = true;
  }
  const edit = readAgentEdit(block);
  return {
    models: ref ? [ref] : [],
    skills: readSkillEntries(step),
    ...(edit ? { edit } : {}),
    limits,
    texts: {
      system: typeof block.system === 'string' ? block.system : '',
      prompt: typeof block.prompt === 'string' ? block.prompt : '',
    },
  };
}
