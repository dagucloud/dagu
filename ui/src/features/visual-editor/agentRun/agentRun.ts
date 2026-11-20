// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isRecord, type Rec } from '../record';

/** The `action` of an agent step. */
export const AGENT_RUN_ACTION = 'agent.run';

/**
 * The built-in CLI harness providers: the providers that an agent step can
 * name. A chat provider (`anthropic`, `openai`, `local`) is not valid.
 */
export const CLI_PROVIDERS = [
  'claude',
  'codex',
  'copilot',
  'opencode',
  'pi',
  'gemini',
  'cursor',
  'cline',
  'aider',
  'qwen',
  'goose',
  'kiro',
  'droid',
  'amp',
  'deepseek',
  'kilo',
] as const;

/**
 * The provider of an agent step from the plain value of the step, when it is
 * a literal name. A `${...}` reference gives none.
 */
export function readAgentProvider(step: Rec): string | undefined {
  const block = isRecord(step.with) ? step.with : {};
  if (typeof block.provider !== 'string') return undefined;
  const text = block.provider.trim();
  return text === '' || text.includes('${') ? undefined : text;
}

/** True when the step value is an agent step. */
export function isAgentRunValue(step: Rec, action?: string): boolean {
  return (step.action ?? action) === AGENT_RUN_ACTION;
}

/** What this module reads of a step: the kind, the action and the plain value. */
export interface TrustStep {
  kind: string;
  action?: string;
  value: Rec;
}

/** The steps and the handlers of a DAG. */
export interface TrustModel {
  steps: TrustStep[];
  handlers: TrustStep[];
}

/** True when the DAG has an agent step, in its steps or in its handlers. */
export function dagHasAgentRun(model: TrustModel): boolean {
  return [...model.steps, ...model.handlers].some((step) =>
    isAgentRunValue(step.value, step.action)
  );
}

/** The executor types of a step that run a process of the host. */
const PROCESS_TYPES = new Set(['', 'command', 'shell', 'harness']);

/**
 * True when the step runs a process of the host: a step with `run`,
 * `command` or `script`, or with the action `harness.run`, and no container.
 * A step with another action does not run a process (Spec 092, "The key").
 */
export function runsProcess(step: TrustStep): boolean {
  const { value } = step;
  if ('container' in value) return false;
  const executor = isRecord(value.executor) ? value.executor.type : undefined;
  const type = value.type ?? executor;
  if (typeof type === 'string' && !PROCESS_TYPES.has(type)) return false;
  return step.kind === 'run' || (step.action ?? value.action) === 'harness.run';
}

/**
 * Where the trust of a step comes from: the agent step, the key `untrusted`,
 * or the default of a DAG that has an agent step. `plain` is a trusted step
 * with no reason to look at.
 */
export type TrustSource =
  | 'agent'
  | 'marked'
  | 'default'
  | 'opted-out'
  | 'plain';

/**
 * The effective trust of a step (Spec 092, "Default of the key"). An agent
 * step is untrusted. In a DAG that has an agent step, a step that runs a
 * process is untrusted unless it has `untrusted: false`; `secrets` does not
 * make it trusted. In another DAG, a step is untrusted only with
 * `untrusted: true`.
 */
export function trustOf(
  step: TrustStep,
  agentDag: boolean
): { untrusted: boolean; source: TrustSource } {
  const { value } = step;
  if (isAgentRunValue(value, step.action)) {
    return { untrusted: true, source: 'agent' };
  }
  if (value.untrusted === true) return { untrusted: true, source: 'marked' };
  if (!agentDag || !runsProcess(step)) {
    return { untrusted: false, source: 'plain' };
  }
  if (value.untrusted === false) {
    return { untrusted: false, source: 'opted-out' };
  }
  return { untrusted: true, source: 'default' };
}

/**
 * The trust that the YAML of the step writes, and so the only one that the UI
 * marks: `marked` for `untrusted: true`, `opted-out` for `untrusted: false`.
 * An agent step is untrusted anyway, so its `false` shows no mark. A trust
 * that comes from a default shows no mark.
 */
export function writtenTrust(
  step: TrustStep
): 'marked' | 'opted-out' | undefined {
  const { untrusted } = step.value;
  if (untrusted === true) return 'marked';
  if (untrusted === false && !isAgentRunValue(step.value, step.action)) {
    return 'opted-out';
  }
  return undefined;
}

/**
 * True for a step that the DAG refuses: it runs a process and lists secrets in
 * a DAG that has an agent step, and has no key `untrusted`. The step is
 * untrusted by default, so the secrets can not reach it.
 */
export function secretsNeedOptOut(step: TrustStep, agentDag: boolean): boolean {
  const { secrets } = step.value;
  const listed = Array.isArray(secrets) ? secrets.length > 0 : secrets != null;
  return (
    agentDag && runsProcess(step) && listed && !('untrusted' in step.value)
  );
}
