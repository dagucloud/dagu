// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  BUILTIN_TOOLS,
  type BuiltinEntryModel,
  type StepToolsModel,
  type ToolCategory,
  type ToolEntryKind,
  type ToolEntryModel,
  type ToolMcpModel,
  type ToolParamModel,
} from './model';
import { isRecord, type Rec } from '../record';

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== '' ? value : undefined;
}

function strings(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === 'string')
    : [];
}

function scalarText(value: unknown): string | undefined {
  return typeof value === 'string' ||
    typeof value === 'number' ||
    typeof value === 'boolean'
    ? String(value)
    : undefined;
}

/** True for `{from_ai: ...}`: a value that the model decides. */
function isFromAi(value: unknown): value is { from_ai: unknown } {
  return (
    isRecord(value) &&
    Object.keys(value).length === 1 &&
    Object.prototype.hasOwnProperty.call(value, 'from_ai')
  );
}

function readParams(raw: unknown): ToolParamModel[] {
  if (!isRecord(raw)) return [];
  return Object.entries(raw).map(([name, value]): ToolParamModel => {
    if (!isFromAi(value)) return { name, state: 'fixed', value };
    const description = isRecord(value.from_ai)
      ? text(value.from_ai.description)
      : undefined;
    return description
      ? { name, state: 'from_ai', description }
      : { name, state: 'from_ai' };
  });
}

/** The host of a URL, or the text itself when it is not a plain URL. */
export function hostOf(url: string): string {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
}

function readMcp(rec: Rec): ToolMcpModel {
  const url = typeof rec.url === 'string' ? rec.url : '';
  const headers: Record<string, string> = {};
  if (isRecord(rec.headers)) {
    for (const [key, value] of Object.entries(rec.headers)) {
      headers[key] = scalarText(value) ?? '';
    }
  }
  return {
    named: rec.url === undefined,
    url,
    host: hostOf(url),
    headers,
    allow: strings(rec.allow),
  };
}

const KINDS: Record<ToolCategory, ToolEntryKind> = {
  dags: 'dag',
  mcp: 'mcp',
};

function readEntry(
  raw: unknown,
  category: ToolCategory,
  index: number
): ToolEntryModel {
  if (!isRecord(raw)) {
    return {
      index,
      category,
      kind: 'invalid',
      label: '',
      params: [],
      problem: 'A tool entry is a mapping with a name.',
    };
  }
  const entry: ToolEntryModel = {
    index,
    category,
    kind: KINDS[category],
    label: text(raw.name) ?? '',
    params: category === 'mcp' ? [] : readParams(raw.params),
  };
  if (category === 'mcp') entry.mcp = readMcp(raw);
  return entry;
}

/** One item of the list form: the name of a DAG, with no params. */
function readListEntry(raw: unknown, index: number): ToolEntryModel {
  if (typeof raw !== 'string') {
    return {
      index,
      category: 'dags',
      kind: 'invalid',
      label: '',
      params: [],
      problem: 'A tool in the list form is the name of a DAG.',
      listForm: true,
    };
  }
  return {
    index,
    category: 'dags',
    kind: 'dag',
    label: raw,
    params: [],
    listForm: true,
  };
}

/**
 * Reads the tools of a step from the plain value of the step entry: the list
 * `with.dags`, the list `with.tools.mcp` and the two limits. The list form
 * `with.tools: [name]` of the released steps reads as `dags` entries with no
 * params. It never throws. A value of the wrong type gives no entry and no
 * limit. An unknown key of `with.tools` (`dags` is one) and the category
 * `builtin` give no entry: the validation of the file reports them.
 */
export function readStepTools(step: Rec): StepToolsModel {
  const withBlock = step.with;
  const model: StepToolsModel = { entries: [] };
  if (!isRecord(withBlock)) return model;
  const tools = withBlock.tools;
  if (Array.isArray(tools)) {
    model.listForm = true;
    tools.forEach((raw, index) => {
      model.entries.push(readListEntry(raw, index));
    });
  }
  const dags = withBlock.dags;
  if (Array.isArray(dags)) {
    dags.forEach((raw, index) => {
      model.entries.push(readEntry(raw, 'dags', index));
    });
  }
  const mcp = isRecord(tools) ? tools.mcp : undefined;
  if (Array.isArray(mcp)) {
    mcp.forEach((raw, index) => {
      model.entries.push(readEntry(raw, 'mcp', index));
    });
  }
  const max = withBlock.max_tools;
  if (typeof max === 'number' && Number.isFinite(max)) model.maxTools = max;
  const bytes = withBlock.tool_result_max_bytes;
  if (typeof bytes === 'number' && Number.isFinite(bytes)) {
    model.resultMaxBytes = bytes;
  }
  return model;
}

/**
 * Reads the built-in tools that an `agent.run` step turns on, from the plain
 * value of the step entry: `with.tools.builtin`, in the order `read`,
 * `write`, `edit`, `search`, `shell`. A key that is not `true`, a `shell`
 * that is not a map and an unknown key give no entry: the validation of the
 * file reports them. It never throws.
 */
export function readBuiltinTools(step: Rec): BuiltinEntryModel[] {
  const tools = isRecord(step.with) ? step.with.tools : undefined;
  const builtin = isRecord(tools) ? tools.builtin : undefined;
  if (!isRecord(builtin)) return [];
  const entries: BuiltinEntryModel[] = [];
  for (const name of BUILTIN_TOOLS) {
    const value = builtin[name];
    if (name !== 'shell') {
      if (value === true) entries.push({ name });
    } else if (isRecord(value)) {
      entries.push({
        name,
        allow: strings(value.allow),
        deny: strings(value.deny),
      });
    }
  }
  return entries;
}
