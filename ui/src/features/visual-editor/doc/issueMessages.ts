// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** Messages of the client issues that a quick fix answers. */
export const NO_ID_MESSAGE = 'The step has no id, as the other steps have.';
export const OUTPUTS_ID_MESSAGE =
  'id is required when the step declares outputs.';

/** The error of a step that lists secrets and is untrusted by default (Spec 092). */
export const SECRETS_UNTRUSTED_MESSAGE =
  'step lists secrets but is untrusted by default in a DAG that has an agent.run step; add untrusted: false to run it as the process user, or remove the secrets';

/** The loader errors of an agent.run step that names no endpoint, or no provider. */
export const AGENT_NEEDS_ENDPOINT_MESSAGE =
  'agent.run needs an endpoint; add one on the AI page and name it here';
export const AGENT_NEEDS_PROVIDER_MESSAGE =
  'agent.run needs a provider; it names the CLI provider';

/**
 * The loader errors of a service DAG (Spec 080). The client says the same
 * text as the server, so that the merge keeps one issue.
 */
export const RESTART_WITH_SCHEDULE_MESSAGE =
  'restart_policy cannot be combined with schedule';
export const RESTART_WITH_RETRY_MESSAGE =
  'restart_policy cannot be combined with retry_policy';

export function invalidRestartPolicyMessage(value: string): string {
  return `invalid restart_policy ${JSON.stringify(value)}: must be "no", "always", or "unless-stopped"`;
}

export function restartWaitMessage(policy: string): string {
  return `restart_wait_sec must be at least 1 when restart_policy is "${policy}"`;
}

/**
 * The warning of a service DAG with no `restart_wait_sec` in its file: the
 * base configuration can give it, and only the server knows.
 */
export const RESTART_WAIT_MISSING_MESSAGE =
  'restart_wait_sec is missing: set it to 1 or more in the file, or set it in the base configuration';

/** The plain server error that confirms `RESTART_WAIT_MISSING_MESSAGE`. */
export const RESTART_WAIT_SERVER_ERROR =
  /^restart_wait_sec must be at least 1 when restart_policy is /;

/**
 * The warning of a step that names an endpoint which the AI page does not
 * list. The loader cannot know it, so the step may fail when it starts.
 */
export function missingEndpointMessage(name: string): string {
  return `endpoint "${name}" does not exist; add it on the AI page or choose another`;
}

const MISSING_ENDPOINT =
  /^endpoint "(.*)" does not exist; add it on the AI page or choose another$/;

/** The endpoint name that a `missingEndpointMessage` holds, or undefined. */
export function missingEndpointName(message: string): string | undefined {
  return MISSING_ENDPOINT.exec(message)?.[1];
}

/**
 * The message of a field that accepts one value only: `with.read must be
 * true.` A quick fix reads the value back with `mustBeValue`.
 */
export function mustBeMessage(where: string, value: unknown): string {
  return `${where} must be ${JSON.stringify(value)}.`;
}

// A path, then a JSON scalar: a string, a number, true, false or null.
const MUST_BE =
  /^\S+ must be (true|false|null|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|"(?:[^"\\]|\\.)*")\.$/;

/**
 * The value that a `mustBeMessage` names, or undefined for any other
 * message. The value is a string, a number, a boolean or null.
 */
export function mustBeValue(
  message: string
): { value: string | number | boolean | null } | undefined {
  const text = MUST_BE.exec(message)?.[1];
  if (text === undefined) return undefined;
  try {
    return { value: JSON.parse(text) as string | number | boolean | null };
  } catch {
    return undefined;
  }
}
