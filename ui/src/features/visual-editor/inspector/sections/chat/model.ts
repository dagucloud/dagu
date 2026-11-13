// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ValidationIssue } from '../../../contracts';
import { TOOL_WITH_KEYS } from '../../../tools/model';
import { SKILL_WITH_KEYS } from '../../../tools/skills';
import { issuesUnder } from '../../issues';
import { isPlainObject } from '../../paramsForm';

/** Providers of `definitions.llmProvider` in `dag.schema.json`. */
export const LLM_PROVIDERS = [
  'openai',
  'anthropic',
  'gemini',
  'google',
  'openrouter',
  'local',
  'ollama',
  'vllm',
  'llama',
  'zai',
  'zhipu',
  'zhipuai',
  'glm',
  'opencode',
] as const;

/** Providers that talk to an endpoint that the user runs: they take a base URL. */
const ENDPOINT_PROVIDERS: readonly string[] = [
  'local',
  'ollama',
  'vllm',
  'llama',
];

export function takesBaseUrl(provider: unknown): boolean {
  return typeof provider === 'string' && ENDPOINT_PROVIDERS.includes(provider);
}

/** Keys of `with` for each section of the chat form. */
export const MODEL_KEYS = [
  'endpoint',
  'provider',
  'model',
  'base_url',
  'api_key_name',
] as const;
export const SYSTEM_KEYS = ['system'] as const;
export const PROMPT_KEYS = ['prompt', 'messages'] as const;
export const OPTION_KEYS = [
  'stream',
  'temperature',
  'max_tokens',
  'top_p',
  'max_tool_iterations',
  'thinking',
  'web_search',
] as const;

/** The step-level key that the Output section edits. */
export const OUTPUT_KEY = 'output_schema';

const OWNED: readonly string[] = [
  ...MODEL_KEYS,
  ...SYSTEM_KEYS,
  ...PROMPT_KEYS,
  ...OPTION_KEYS,
  ...TOOL_WITH_KEYS,
  ...SKILL_WITH_KEYS,
];

/** The `with` of a step as a map, or an empty map. */
export function withOf(
  value: Record<string, unknown>
): Record<string, unknown> {
  return isPlainObject(value.with) ? value.with : {};
}

/** The entries of `with` that no section of the chat form edits. */
export function otherChatKeys(
  value: Record<string, unknown>
): [string, unknown][] {
  return Object.entries(withOf(value)).filter(([key]) => !OWNED.includes(key));
}

function sectionPaths(keys: readonly string[]): string[] {
  return keys.map((key) => `with.${key}`);
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

/**
 * Issue counts of the sections of the chat form, except Tools, which the
 * inspector counts. An issue on `with` itself (such as "prompt or messages
 * is required") belongs to Prompt; an issue on a key that no section edits
 * belongs to Other keys.
 */
export function chatIssueCounts(
  fieldIssues: Record<string, ValidationIssue[]>
): {
  model: number;
  system: number;
  prompt: number;
  output: number;
  options: number;
  skills: number;
  other: number;
} {
  const own = fieldIssues.with?.length ?? 0;
  const model = count(fieldIssues, sectionPaths(MODEL_KEYS));
  const system = count(fieldIssues, sectionPaths(SYSTEM_KEYS));
  const prompt = count(fieldIssues, sectionPaths(PROMPT_KEYS));
  const options = count(fieldIssues, sectionPaths(OPTION_KEYS));
  const tools = count(fieldIssues, sectionPaths(TOOL_WITH_KEYS));
  const skills = count(fieldIssues, sectionPaths(SKILL_WITH_KEYS));
  const all = count(fieldIssues, ['with']);
  return {
    model,
    system,
    prompt: prompt + own,
    options,
    skills,
    output: count(fieldIssues, [OUTPUT_KEY]),
    other: all - own - model - system - prompt - options - tools - skills,
  };
}

export interface NumberRule {
  integer: boolean;
  min?: number;
  max?: number;
}

/**
 * Reads the text of a number field. Empty text removes the key (value
 * undefined). A `${...}` reference stays a string. Else the text must be a
 * number in the range; the value is not changed to make it fit.
 */
export function parseNumberText(
  text: string,
  rule: NumberRule
): { ok: true; value: number | string | undefined } | { ok: false } {
  const trimmed = text.trim();
  if (trimmed === '') return { ok: true, value: undefined };
  if (/^\$\{[^}]+\}$/.test(trimmed)) return { ok: true, value: trimmed };
  const pattern = rule.integer ? /^-?\d+$/ : /^-?(\d+\.?\d*|\.\d+)$/;
  if (!pattern.test(trimmed)) return { ok: false };
  const value = Number(trimmed);
  if (!Number.isFinite(value)) return { ok: false };
  if (rule.min !== undefined && value < rule.min) return { ok: false };
  if (rule.max !== undefined && value > rule.max) return { ok: false };
  return { ok: true, value };
}

/** The text that a number field shows for a value of the YAML. */
export function numberText(value: unknown): string {
  if (value === undefined || value === null) return '';
  return typeof value === 'string' ? value : JSON.stringify(value);
}

/** One row of a `messages` list, as far as the form reads it. */
export interface ChatMessage {
  role: string;
  content: string;
}

export const MESSAGE_ROLES = ['system', 'user', 'assistant'] as const;

/** The items of `with.messages`; null when it is absent or not a list. */
export function readMessages(value: unknown): ChatMessage[] | null {
  if (!Array.isArray(value)) return null;
  return value.map((item) => {
    const row = isPlainObject(item) ? item : {};
    return {
      role: typeof row.role === 'string' ? row.role : '',
      content: typeof row.content === 'string' ? row.content : '',
    };
  });
}
