// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { parseAllDocuments } from 'yaml';

/** A task of an agent DAG, as written in the YAML. */
export interface AgentTaskDef {
  name: string;
  description?: string;
}

/** What the agent hub shows of the `llm` block and the `tasks` of the YAML. */
export interface AgentHeader {
  provider?: string;
  model?: string;
  /** First non-empty line of `llm.system`. */
  systemFirstLine?: string;
  /** Limit of tool calls: `llm.max_tool_iterations`. */
  maxIterations?: number;
  tasks: AgentTaskDef[];
}

const EMPTY: AgentHeader = { tasks: [] };

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== ''
    ? value.trim()
    : undefined;
}

function firstLine(value: unknown): string | undefined {
  if (typeof value !== 'string') {
    return undefined;
  }
  return value
    .split('\n')
    .map((line) => line.trim())
    .find((line) => line !== '');
}

function readHeader(source: string): AgentHeader {
  const root = parseAllDocuments(source)[0]?.toJS({ maxAliasCount: 100 });
  if (!isRecord(root)) {
    return EMPTY;
  }
  const header: AgentHeader = { tasks: [] };
  const llm = root.llm;
  if (isRecord(llm)) {
    const provider = text(llm.provider);
    const model = text(llm.model);
    const system = firstLine(llm.system);
    if (provider) header.provider = provider;
    if (model) header.model = model;
    if (system) header.systemFirstLine = system;
    const limit = llm.max_tool_iterations;
    if (typeof limit === 'number' && Number.isFinite(limit)) {
      header.maxIterations = limit;
    }
  }
  if (Array.isArray(root.tasks)) {
    for (const entry of root.tasks) {
      if (!isRecord(entry)) continue;
      const name = text(entry.name);
      if (!name) continue;
      const description = text(entry.description);
      header.tasks.push(description ? { name, description } : { name });
    }
  }
  return header;
}

let last: { source: string; header: AgentHeader } | null = null;

/**
 * Reads the `llm` block and the `tasks` of an agent DAG from its text. It
 * never throws: text that does not parse gives no model and no tasks. The
 * last result is kept, because the graph is built again on each run update.
 */
export function readAgentHeader(source: string): AgentHeader {
  if (last?.source === source) {
    return last.header;
  }
  let header: AgentHeader;
  try {
    header = readHeader(source);
  } catch {
    header = EMPTY;
  }
  last = { source, header };
  return header;
}
