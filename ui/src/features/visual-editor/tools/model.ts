// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The categories of tool that a `chat.completion` or `agent.run` step shares.
 * The DAG tools have a key of their own, `with.dags`. The MCP servers are in
 * the tool map, `with.tools.mcp`.
 */
export type ToolCategory = 'dags' | 'mcp';

/** The categories in the order that the section lists them. */
export const TOOL_CATEGORIES: readonly ToolCategory[] = ['dags', 'mcp'];

/** The YAML path of the list of each category, from the step. */
const CATEGORY_PATH: Record<ToolCategory, readonly string[]> = {
  dags: ['with', 'dags'],
  mcp: ['with', 'tools', 'mcp'],
};

/** The YAML path of the list of a category, from the step: `with.dags` or `with.tools.mcp`. */
export function categoryPath(category: ToolCategory): string[] {
  return [...CATEGORY_PATH[category]];
}

/** The kind of an entry: its category, or `invalid` for an item that is not a mapping. */
export type ToolEntryKind = 'dag' | 'mcp' | 'invalid';

/** Who decides the value of one input of a tool. */
export type ToolParamState = 'fixed' | 'from_ai';

export interface ToolParamModel {
  /** The input name of the tool. */
  name: string;
  state: ToolParamState;
  /** The fixed value, as written. Only for `fixed`. */
  value?: unknown;
  /** The text for the model, from `from_ai: {description}`. */
  description?: string;
}

export interface ToolMcpModel {
  /**
   * True for an entry with no `url`: it names a MCP server entry of the AI page
   * (Spec 095). The URL and the credentials stay in the coordinator.
   */
  named: boolean;
  url: string;
  /** The host of the URL, or the URL itself when it is not a plain URL. */
  host: string;
  /** Header names and values, as written. */
  headers: Record<string, string>;
  allow: string[];
}

export interface ToolEntryModel {
  /** Position in the list of its category: `with.dags[index]` or `with.tools.mcp[index]`. */
  index: number;
  category: ToolCategory;
  kind: ToolEntryKind;
  /** The `name` of the entry: the DAG or the label of the MCP server. */
  label: string;
  params: ToolParamModel[];
  mcp?: ToolMcpModel;
  /** Set on an invalid entry: what is wrong. */
  problem?: string;
  /**
   * Set on an entry of the list form `tools: [name]`: it is a `dags` entry
   * with no params, at `with.tools[index]`.
   */
  listForm?: true;
}

/** The built-in tools of a CLI agent: the keys of `with.tools.builtin` (Spec 091). */
export const BUILTIN_TOOLS = [
  'read',
  'write',
  'edit',
  'search',
  'shell',
] as const;
export type BuiltinToolName = (typeof BUILTIN_TOOLS)[number];

/** One built-in tool that a step turns on, as the canvas shows it. */
export interface BuiltinEntryModel {
  name: BuiltinToolName;
  /** `shell`: the command patterns that the agent can run. */
  allow?: string[];
  /** `shell`: the command patterns that the agent cannot run. */
  deny?: string[];
}

/** The tools of one step. */
export interface StepToolsModel {
  /** The entries of `with.dags`, then of `with.tools.mcp`, each list in order. */
  entries: ToolEntryModel[];
  /**
   * The built-in tools of an `agent.run` step. `readStepTools` does not fill
   * it: only the canvas of an agent step reads `with.tools.builtin`.
   */
  builtin?: BuiltinEntryModel[];
  /** Set when `with.tools` is the list of DAG names of the released form. */
  listForm?: true;
  /** `with.max_tools`, when it is a number. */
  maxTools?: number;
  /** `with.tool_result_max_bytes`, when it is a number. */
  resultMaxBytes?: number;
}

/**
 * The identity of an entry in the view: its category and its place in it. An
 * entry of the list form has its own identity: `with.dags` can hold an entry
 * at the same place.
 */
export function entryKey(
  entry: Pick<ToolEntryModel, 'category' | 'index' | 'listForm'>
): string {
  return entry.listForm
    ? `tools[${entry.index}]`
    : `${entry.category}[${entry.index}]`;
}

/** The YAML path of an entry, from the step. */
export function entryPath(
  entry: Pick<ToolEntryModel, 'category' | 'index' | 'listForm'>
): string {
  return entry.listForm
    ? `with.tools[${entry.index}]`
    : `${CATEGORY_PATH[entry.category].join('.')}[${entry.index}]`;
}

/** The tool name that the model sees for a DAG: Spec 090 rules. */
export function toolNameOf(name: string): string {
  return name.replace(/[^A-Za-z0-9_-]/g, '_');
}

/** Default of `with.max_tools`. */
export const DEFAULT_MAX_TOOLS = 64;
/** Default of `with.max_tool_calls` of an agent step (Spec 091). */
export const DEFAULT_MAX_TOOL_CALLS = 100;
/** Default of `with.tool_result_max_bytes`. */
export const DEFAULT_TOOL_RESULT_MAX_BYTES = 524288;

/** Keys of `with` that the Tools section owns. */
export const TOOL_WITH_KEYS = [
  'dags',
  'tools',
  'max_tools',
  'tool_result_max_bytes',
] as const;

/** The `action` of a step that can offer tools. */
export const CHAT_ACTION = 'chat.completion';
