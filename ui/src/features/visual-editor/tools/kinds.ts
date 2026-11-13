// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  CircleAlert,
  Plug,
  Workflow,
  Wrench,
  type LucideIcon,
} from 'lucide-react';
import { builtinLabel } from './builtin';
import {
  BUILTIN_TOOLS,
  type BuiltinEntryModel,
  type BuiltinToolName,
  type ToolCategory,
  type ToolEntryKind,
  type ToolEntryModel,
} from './model';

/**
 * The three kinds of tool. A DAG tool is called by the model. An MCP server
 * gives the tools of a remote server. A built-in tool is a tool of the CLI of
 * an agent step. The canvas, the Tools section, the picker and the tool panel
 * show a kind with the same icon, the same label and the same colour.
 */
export type ToolKind = 'dag' | 'mcp' | 'builtin';

/** The kinds in the order that every list shows them. */
export const TOOL_KINDS: readonly ToolKind[] = ['dag', 'mcp', 'builtin'];

/**
 * What a dock of a step lists and edits. The DAGs dock (opened from the DAGs
 * card) has the DAG tools only. The Tools dock (opened from the Tools card)
 * has the MCP servers and the built-in tools.
 */
export type DockScope = 'tools' | 'dags';

/** The kinds of tool that each dock lists, in the order that it shows them. */
export const SCOPE_KINDS: Record<DockScope, readonly ToolKind[]> = {
  dags: ['dag'],
  tools: ['mcp', 'builtin'],
};

/** The dock that lists a kind of tool. */
export function scopeOfKind(kind: ToolKind): DockScope {
  return kind === 'dag' ? 'dags' : 'tools';
}

/** The two steps that take tools: a chat step and an agent step. */
export type ToolStepKind = 'chat' | 'agent';

/**
 * The kinds of tool that each step accepts. It is the one table that the
 * add panel and the Tools sections read. A chat step takes the tools that
 * the model calls (DAG, MCP server). An agent step takes the same kinds and
 * the built-in tools of its CLI. The validation of the file is the
 * authority: a kind that this table lists as valid can still get an issue.
 */
export const VALID_KINDS: Record<ToolStepKind, readonly ToolKind[]> = {
  chat: ['dag', 'mcp'],
  agent: ['dag', 'mcp', 'builtin'],
};

/** True when a step of this kind accepts tools of this kind. */
export function stepAccepts(step: ToolStepKind, kind: ToolKind): boolean {
  return VALID_KINDS[step].includes(kind);
}

type Translate = (
  text: string,
  params?: Record<string, string | number>
) => string;

const KIND_OF_CATEGORY: Record<ToolCategory | 'builtin', ToolKind> = {
  dags: 'dag',
  mcp: 'mcp',
  builtin: 'builtin',
};

/** The `action` of each step that takes tools. */
const ACTION_OF_STEP: Record<ToolStepKind, string> = {
  chat: 'chat.completion',
  agent: 'agent.run',
};

/**
 * Whether the add panel offers a kind on a step. `note` is the one line that
 * the panel shows under a kind that is off: a kind that the step does not
 * accept, or built-in tools that are all on already.
 */
export interface KindAvailability {
  enabled: boolean;
  note: string | null;
}

/**
 * The availability of each kind on a step, for the first step of the add
 * panel. `taken` are the built-in tools that the step has already.
 */
export function kindAvailability(
  ts: Translate,
  step: ToolStepKind,
  taken: ReadonlySet<BuiltinToolName>
): Record<ToolKind, KindAvailability> {
  const result = {} as Record<ToolKind, KindAvailability>;
  for (const kind of TOOL_KINDS) {
    if (!stepAccepts(step, kind)) {
      const steps = (Object.keys(VALID_KINDS) as ToolStepKind[])
        .filter((other) => stepAccepts(other, kind))
        .map((other) => ACTION_OF_STEP[other])
        .join(' and ');
      result[kind] = {
        enabled: false,
        note: ts('Valid on {steps} steps only.', { steps }),
      };
    } else if (
      kind === 'builtin' &&
      BUILTIN_TOOLS.every((name) => taken.has(name))
    ) {
      result[kind] = {
        enabled: false,
        note: ts('All built-in tools are enabled.'),
      };
    } else {
      result[kind] = { enabled: true, note: null };
    }
  }
  return result;
}

/** The kind of the entries of a category of the tool map. */
export function kindOfCategory(category: ToolCategory | 'builtin'): ToolKind {
  return KIND_OF_CATEGORY[category];
}

const ICONS: Record<ToolKind, LucideIcon> = {
  dag: Workflow,
  mcp: Plug,
  builtin: Wrench,
};

/** Icon of a kind of tool. */
export function kindIcon(kind: ToolKind): LucideIcon {
  return ICONS[kind];
}

/** Icon of a tool entry: the icon of its kind, or an alert for an invalid entry. */
export function entryIcon(kind: ToolEntryKind): LucideIcon {
  return kind === 'invalid' ? CircleAlert : ICONS[kind];
}

/** Tailwind classes of a kind, on its `--tool-*` colour token. */
export interface KindClasses {
  /** Text and icon colour. */
  text: string;
  /** Tinted surface of a badge. */
  bg: string;
}

// Literal strings, so that Tailwind finds every class in this file.
const CLASSES: Record<ToolKind, KindClasses> = {
  dag: { text: 'text-tool-dag', bg: 'bg-tool-dag/10' },
  mcp: { text: 'text-tool-mcp', bg: 'bg-tool-mcp/10' },
  builtin: { text: 'text-tool-builtin', bg: 'bg-tool-builtin/10' },
};

export function kindClasses(kind: ToolKind): KindClasses {
  return CLASSES[kind];
}

/** The name of a kind: "DAG tool", "MCP server", "Built-in tool". */
export function kindLabel(ts: Translate, kind: ToolKind): string {
  switch (kind) {
    case 'dag':
      return ts('DAG tool');
    case 'mcp':
      return ts('MCP server');
    case 'builtin':
      return ts('Built-in tool');
  }
}

/** The short text of the badge of a kind. */
export function kindBadge(ts: Translate, kind: ToolKind): string {
  switch (kind) {
    case 'dag':
      return ts('DAG');
    case 'mcp':
      return ts('MCP');
    case 'builtin':
      return ts('Built-in');
  }
}

/** The heading of a group of tools of one kind. */
export function kindGroupTitle(ts: Translate, kind: ToolKind): string {
  switch (kind) {
    case 'dag':
      return ts('DAG tools');
    case 'mcp':
      return ts('MCP servers');
    case 'builtin':
      return ts('Built-in tools');
  }
}

/**
 * The lines of the tooltip of a tool: its kind, then its source: the name of
 * the DAG, or the host of the URL of an MCP server.
 */
export function entryTooltip(ts: Translate, entry: ToolEntryModel): string[] {
  switch (entry.kind) {
    case 'dag':
      return [
        kindLabel(ts, 'dag'),
        ts('DAG: {name}', { name: entry.label || '…' }),
      ];
    case 'mcp':
      return [
        kindLabel(ts, 'mcp'),
        entry.mcp?.named
          ? ts('MCP server entry: {name}', { name: entry.label || '…' })
          : ts('Server: {host}', { host: entry.mcp?.host || '…' }),
      ];
    case 'invalid':
      return [ts('Invalid tool entry')];
  }
}

/**
 * The lines of the tooltip of a built-in tool: its kind, its name and, for
 * the shell tool, the command patterns that it allows and denies.
 */
export function builtinTooltip(
  ts: Translate,
  entry: BuiltinEntryModel
): string[] {
  return [
    kindLabel(ts, 'builtin'),
    builtinLabel(ts, entry.name),
    ...(entry.allow ?? []).map((pattern) => `${ts('Allow')}: ${pattern}`),
    ...(entry.deny ?? []).map((pattern) => `${ts('Deny')}: ${pattern}`),
  ];
}
