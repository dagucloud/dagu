// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ValidationIssue } from '../../../contracts';
import { issuesUnder } from '../../issues';
import { OUTPUT_KEY, withOf } from '../chat/model';

/**
 * The keys of the `with` of an `agent.run` step (Spec 091), by the section
 * of the form that edits them.
 */
export const AGENT_MODEL_KEYS = ['provider', 'endpoint'] as const;
export const AGENT_SYSTEM_KEYS = ['system'] as const;
export const AGENT_PROMPT_KEYS = ['prompt'] as const;
export const AGENT_TOOL_KEYS = ['dags', 'tools'] as const;
export const AGENT_SKILL_KEYS = ['skills'] as const;
export const AGENT_LIMIT_KEYS = [
  'max_tool_iterations',
  'max_tool_calls',
] as const;
/** The step-level key that the Limits section edits besides the keys of `with`. */
export const TIMEOUT_KEY = 'timeout_sec';

const OWNED: readonly string[] = [
  ...AGENT_MODEL_KEYS,
  ...AGENT_SYSTEM_KEYS,
  ...AGENT_PROMPT_KEYS,
  ...AGENT_TOOL_KEYS,
  ...AGENT_SKILL_KEYS,
  ...AGENT_LIMIT_KEYS,
];

/** The entries of `with` that no section of an agent form edits. */
export function otherAgentKeys(
  value: Record<string, unknown>
): [string, unknown][] {
  return Object.entries(withOf(value)).filter(([key]) => !OWNED.includes(key));
}

function count(
  fieldIssues: Record<string, ValidationIssue[]>,
  paths: string[]
): number {
  return paths.reduce(
    (total, path) => total + issuesUnder(fieldIssues, path).length,
    0
  );
}

function withPaths(keys: readonly string[]): string[] {
  return keys.map((key) => `with.${key}`);
}

/**
 * Issue counts of the sections of the agent form. An issue on `with` itself
 * (such as "prompt is required") belongs to Prompt; an issue on a key that
 * no section edits belongs to Other keys. `timeout_sec` is a step key that
 * Limits edits.
 */
export function agentRunIssueCounts(
  fieldIssues: Record<string, ValidationIssue[]>
): {
  model: number;
  system: number;
  prompt: number;
  tools: number;
  skills: number;
  limits: number;
  output: number;
  other: number;
} {
  const own = fieldIssues.with?.length ?? 0;
  const modelKeys = count(fieldIssues, withPaths(AGENT_MODEL_KEYS));
  const system = count(fieldIssues, withPaths(AGENT_SYSTEM_KEYS));
  const prompt = count(fieldIssues, withPaths(AGENT_PROMPT_KEYS));
  const tools = count(fieldIssues, withPaths(AGENT_TOOL_KEYS));
  const skills = count(fieldIssues, withPaths(AGENT_SKILL_KEYS));
  const limitKeys = count(fieldIssues, withPaths(AGENT_LIMIT_KEYS));
  const all = count(fieldIssues, ['with']);
  return {
    model: modelKeys,
    system,
    prompt: prompt + own,
    tools,
    skills,
    limits: limitKeys + count(fieldIssues, [TIMEOUT_KEY]),
    output: count(fieldIssues, [OUTPUT_KEY]),
    other: all - own - modelKeys - system - prompt - tools - skills - limitKeys,
  };
}
